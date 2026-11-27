package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

// Mongo-backed coverage for all T-13 acceptance criteria (HTTP + DAL).
func TestIntegration_Events_AllAcceptanceCriteria(t *testing.T) {
	st := openMongoStore(t)
	a, err := bootstrap.Run(context.Background(), st, "mongo-ev-a-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	b, err := bootstrap.Run(context.Background(), st, "mongo-ev-b-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 16, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agentA := createAgentToken(t, base, a.RawKey)
	agentB := createAgentToken(t, base, b.RawKey)
	machineID := uuid.NewString()
	ts := now.Add(-time.Minute)

	// AC1: within-batch + across-batch idempotency
	code, raw := postEvents(t, base, agentA, eventsBody(machineID,
		eventItem("same", ts, "start", map[string]any{"n": 1}),
		eventItem("same", ts, "start", map[string]any{"n": 2}),
	))
	if code != http.StatusNoContent {
		t.Fatalf("AC1 ingest: %d %s", code, raw)
	}
	code, raw = postEvents(t, base, agentA, eventsBody(machineID, eventItem("same", ts, "start", map[string]any{"n": 3})))
	if code != http.StatusNoContent {
		t.Fatalf("AC1 resubmit: %d %s", code, raw)
	}

	// AC2: >100 events → 400; >256 KiB → 413
	items := make([]map[string]any, 0, store.EventBatchMaxCount+1)
	for i := 0; i < store.EventBatchMaxCount+1; i++ {
		items = append(items, eventItem(fmt.Sprintf("big-%d", i), ts, "start", map[string]any{}))
	}
	code, raw = postEvents(t, base, agentA, eventsBody(machineID, items...))
	if code != http.StatusBadRequest {
		t.Fatalf("AC2 count: %d %s", code, raw)
	}
	bigPayload := strings.Repeat("y", store.EventBatchMaxBytes)
	oversized := fmt.Sprintf(
		`{"machine_id":%q,"events":[{"event_id":"huge","ts":%q,"type":"start","payload":{"blob":%q}}]}`,
		machineID, ts.Format(time.RFC3339Nano), bigPayload,
	)
	code, raw = postEventsRaw(t, base, agentA, []byte(oversized))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("AC2 size: %d %s", code, raw)
	}

	// Seed ordered events for AC4/AC5 (same ts + later)
	code, raw = postEvents(t, base, agentA, eventsBody(machineID,
		eventItem("b", ts, "start", map[string]any{}),
		eventItem("a", ts, "start", map[string]any{}),
		eventItem("c", ts.Add(time.Second), "update", map[string]any{}),
		eventItem("d", ts.Add(2*time.Second), "start", map[string]any{}),
		eventItem("e", ts.Add(3*time.Second), "start", map[string]any{}),
	))
	if code != http.StatusNoContent {
		t.Fatalf("seed: %d %s", code, raw)
	}

	// AC4: ordered by ts, event_id tie-breaker; tenant-scoped
	code, raw = listEvents(t, base, a.RawKey, machineID, nil)
	if code != http.StatusOK {
		t.Fatalf("AC4 list: %d %s", code, raw)
	}
	page := decodeEventList(t, raw)
	// same + a,b,c,d,e = 6
	if len(page.Events) != 6 {
		t.Fatalf("AC4 count = %d %#v", len(page.Events), page.Events)
	}
	wantOrder := []string{"a", "b", "same", "c", "d", "e"} // ts asc, event_id tie-break
	for i, id := range wantOrder {
		if page.Events[i].EventID != id {
			t.Fatalf("AC4 order[%d]=%s want %s", i, page.Events[i].EventID, id)
		}
	}

	// AC9: other tenant with same machine_id never sees A's events
	code, raw = postEvents(t, base, agentB, eventsBody(machineID, eventItem("b-only", ts, "start", map[string]any{})))
	if code != http.StatusNoContent {
		t.Fatalf("B ingest: %d %s", code, raw)
	}
	code, raw = listEvents(t, base, b.RawKey, machineID, nil)
	if code != http.StatusOK {
		t.Fatalf("AC9 list: %d %s", code, raw)
	}
	pageB := decodeEventList(t, raw)
	if len(pageB.Events) != 1 || pageB.Events[0].EventID != "b-only" {
		t.Fatalf("AC9 leak: %#v", pageB.Events)
	}

	// AC5: cursor pagination + limit bounds
	code, raw = listEvents(t, base, a.RawKey, machineID, url.Values{"limit": {"2"}})
	if code != http.StatusOK {
		t.Fatalf("AC5 p1: %d %s", code, raw)
	}
	p1 := decodeEventList(t, raw)
	if len(p1.Events) != 2 || p1.NextCursor == nil {
		t.Fatalf("AC5 p1: %#v", p1)
	}
	code, raw = listEvents(t, base, a.RawKey, machineID, url.Values{
		"limit": {"2"}, "cursor": {*p1.NextCursor},
	})
	if code != http.StatusOK {
		t.Fatalf("AC5 p2: %d %s", code, raw)
	}
	p2 := decodeEventList(t, raw)
	if len(p2.Events) != 2 || p2.Events[0].EventID == p1.Events[0].EventID {
		t.Fatalf("AC5 cursor did not advance: p1=%#v p2=%#v", p1.Events, p2.Events)
	}
	for _, bad := range []string{"0", "101"} {
		code, raw = listEvents(t, base, a.RawKey, machineID, url.Values{"limit": {bad}})
		if code != http.StatusBadRequest {
			t.Fatalf("AC5 limit %s: %d %s", bad, code, raw)
		}
	}

	// AC6: future + malformed ts
	future := now.Add(store.EventMaxFutureSkew + time.Minute)
	code, raw = postEvents(t, base, agentA, eventsBody(machineID, eventItem("fut", future, "start", map[string]any{})))
	if code != http.StatusBadRequest {
		t.Fatalf("AC6 future: %d %s", code, raw)
	}
	malformed := fmt.Sprintf(
		`{"machine_id":%q,"events":[{"event_id":"bad","ts":"nope","type":"start","payload":{}}]}`,
		machineID,
	)
	code, raw = postEventsRaw(t, base, agentA, []byte(malformed))
	if code != http.StatusBadRequest {
		t.Fatalf("AC6 malformed: %d %s", code, raw)
	}

	// AC8: all-or-nothing — nothing new persisted when one event is invalid
	before, err := st.Events().List(context.Background(), store.EventQuery{
		TenantID: a.TenantID, MachineID: machineID, Limit: store.EventListMaxLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, raw = postEvents(t, base, agentA, eventsBody(machineID,
		eventItem("new-ok", ts, "start", map[string]any{}),
		eventItem("new-bad", future, "start", map[string]any{}),
	))
	if code != http.StatusBadRequest {
		t.Fatalf("AC8: %d %s", code, raw)
	}
	after, err := st.Events().List(context.Background(), store.EventQuery{
		TenantID: a.TenantID, MachineID: machineID, Limit: store.EventListMaxLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Events) != len(before.Events) {
		t.Fatalf("AC8 persisted partial batch: before=%d after=%d", len(before.Events), len(after.Events))
	}
	// AC3 / AC7 (TTL index ttl_events_ts + expireAfterSeconds) are asserted in
	// internal/store/mongo.TestIntegration_EventsTTLIndex.
}
