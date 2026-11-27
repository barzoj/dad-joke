package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func postEvents(t *testing.T, base, agentToken string, body any) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodPost, base+"/v1/events", agentToken, body)
	return code, raw
}

func postEventsRaw(t *testing.T, base, agentToken string, raw []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/events", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agentToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, out
}

func listEvents(t *testing.T, base, bearer, machineID string, q url.Values) (int, []byte) {
	t.Helper()
	u := base + "/v1/machines/" + machineID + "/events"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	code, _, raw := doJSON(t, http.MethodGet, u, bearer, nil)
	return code, raw
}

func eventItem(id string, ts time.Time, typ string, payload any) map[string]any {
	return map[string]any{
		"event_id": id,
		"ts":       ts.UTC().Format(time.RFC3339Nano),
		"type":     typ,
		"payload":  payload,
	}
}

func eventsBody(machineID string, items ...map[string]any) map[string]any {
	if items == nil {
		items = []map[string]any{}
	}
	return map[string]any{"machine_id": machineID, "events": items}
}

type eventListResp struct {
	Events []struct {
		EventID string          `json:"event_id"`
		TS      time.Time       `json:"ts"`
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	} `json:"events"`
	NextCursor *string `json:"next_cursor"`
}

func decodeEventList(t *testing.T, raw []byte) eventListResp {
	t.Helper()
	var out eventListResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	return out
}

// AC1: resubmitting the same event_id is idempotent (across batches and within a batch).
func TestEvents_AC1_IdempotentEventID(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "ev-idem-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	ts := now.Add(-time.Minute)

	body := eventsBody(machineID,
		eventItem("same", ts, "start", map[string]any{"n": 1}),
		eventItem("same", ts, "start", map[string]any{"n": 2}), // within-batch dup
	)
	code, raw := postEvents(t, base, agent, body)
	if code != http.StatusNoContent {
		t.Fatalf("ingest: %d %s", code, raw)
	}

	code, raw = postEvents(t, base, agent, eventsBody(machineID, eventItem("same", ts, "start", map[string]any{"n": 3})))
	if code != http.StatusNoContent {
		t.Fatalf("resubmit: %d %s", code, raw)
	}

	code, raw = listEvents(t, base, pat, machineID, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, raw)
	}
	page := decodeEventList(t, raw)
	if len(page.Events) != 1 || page.Events[0].EventID != "same" {
		t.Fatalf("want 1 event, got %#v", page.Events)
	}
}

// AC2: batch over limit (>100 events or >256 KiB) → 400 / 413.
func TestEvents_AC2_BatchLimits(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "ev-lim-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	ts := now.Add(-time.Minute)

	items := make([]map[string]any, 0, store.EventBatchMaxCount+1)
	for i := 0; i < store.EventBatchMaxCount+1; i++ {
		items = append(items, eventItem(fmt.Sprintf("e-%d", i), ts, "start", map[string]any{}))
	}
	code, raw := postEvents(t, base, agent, eventsBody(machineID, items...))
	if code != http.StatusBadRequest {
		t.Fatalf("101 events: %d %s", code, raw)
	}
	assertErrorResponse(t, raw)

	// Oversized body → 413 (MaxBytesReader on POST /v1/events).
	bigPayload := strings.Repeat("x", store.EventBatchMaxBytes)
	oversized := fmt.Sprintf(
		`{"machine_id":%q,"events":[{"event_id":"big","ts":%q,"type":"start","payload":{"blob":%q}}]}`,
		machineID, ts.Format(time.RFC3339Nano), bigPayload,
	)
	code, raw = postEventsRaw(t, base, agent, []byte(oversized))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d %s", code, raw)
	}
	assertErrorResponse(t, raw)
}

// AC4 + AC9: ControlAPI reads are tenant-isolated and ordered by ts (+ event_id tie-breaker).
func TestEvents_AC4_AC9_TenantIsolationAndOrder(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, patA := seedTenantPAT(t, st, "ev-a-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "ev-b-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agentA := createAgentToken(t, base, patA)
	agentB := createAgentToken(t, base, patB)
	machineID := uuid.NewString()
	ts := now.Add(-time.Minute)

	code, raw := postEvents(t, base, agentA, eventsBody(machineID,
		eventItem("b", ts, "start", map[string]any{}),
		eventItem("a", ts, "start", map[string]any{}), // same ts; tie-break by event_id
		eventItem("c", ts.Add(time.Second), "update", map[string]any{}),
	))
	if code != http.StatusNoContent {
		t.Fatalf("A ingest: %d %s", code, raw)
	}
	code, raw = postEvents(t, base, agentB, eventsBody(machineID,
		eventItem("other", ts, "start", map[string]any{"tenant": "b"}),
	))
	if code != http.StatusNoContent {
		t.Fatalf("B ingest: %d %s", code, raw)
	}

	code, raw = listEvents(t, base, patA, machineID, nil)
	if code != http.StatusOK {
		t.Fatalf("A list: %d %s", code, raw)
	}
	page := decodeEventList(t, raw)
	if len(page.Events) != 3 {
		t.Fatalf("A events = %#v", page.Events)
	}
	ids := []string{page.Events[0].EventID, page.Events[1].EventID, page.Events[2].EventID}
	if ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Fatalf("order = %v, want a,b,c", ids)
	}

	// AC9: tenant B listing the shared machine_id must not see A's events.
	code, raw = listEvents(t, base, patB, machineID, nil)
	if code != http.StatusOK {
		t.Fatalf("B list: %d %s", code, raw)
	}
	pageB := decodeEventList(t, raw)
	if len(pageB.Events) != 1 || pageB.Events[0].EventID != "other" {
		t.Fatalf("B leaked/wrong: %#v", pageB.Events)
	}
}

// AC5: cursor pagination is deterministic; limit bounds enforced.
func TestEvents_AC5_CursorAndLimitBounds(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "ev-cur-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	ts := now.Add(-time.Minute)

	items := make([]map[string]any, 0, 5)
	for i := 0; i < 5; i++ {
		items = append(items, eventItem(fmt.Sprintf("e%d", i), ts.Add(time.Duration(i)*time.Second), "start", map[string]any{}))
	}
	code, raw := postEvents(t, base, agent, eventsBody(machineID, items...))
	if code != http.StatusNoContent {
		t.Fatalf("ingest: %d %s", code, raw)
	}

	code, raw = listEvents(t, base, pat, machineID, url.Values{"limit": {"2"}})
	if code != http.StatusOK {
		t.Fatalf("page1: %d %s", code, raw)
	}
	p1 := decodeEventList(t, raw)
	if len(p1.Events) != 2 || p1.NextCursor == nil || *p1.NextCursor == "" {
		t.Fatalf("page1: %#v", p1)
	}
	if p1.Events[0].EventID != "e0" || p1.Events[1].EventID != "e1" {
		t.Fatalf("page1 ids: %#v", p1.Events)
	}

	code, raw = listEvents(t, base, pat, machineID, url.Values{
		"limit":  {"2"},
		"cursor": {*p1.NextCursor},
	})
	if code != http.StatusOK {
		t.Fatalf("page2: %d %s", code, raw)
	}
	p2 := decodeEventList(t, raw)
	if len(p2.Events) != 2 || p2.Events[0].EventID != "e2" || p2.Events[1].EventID != "e3" {
		t.Fatalf("page2: %#v", p2)
	}

	code, raw = listEvents(t, base, pat, machineID, url.Values{
		"limit":  {"2"},
		"cursor": {*p2.NextCursor},
	})
	if code != http.StatusOK {
		t.Fatalf("page3: %d %s", code, raw)
	}
	p3 := decodeEventList(t, raw)
	if len(p3.Events) != 1 || p3.Events[0].EventID != "e4" || p3.NextCursor != nil {
		t.Fatalf("page3: %#v", p3)
	}

	for _, bad := range []string{"0", "101", "-1"} {
		code, raw = listEvents(t, base, pat, machineID, url.Values{"limit": {bad}})
		if code != http.StatusBadRequest {
			t.Fatalf("limit %s: %d %s", bad, code, raw)
		}
		assertErrorResponse(t, raw)
	}
	code, raw = listEvents(t, base, pat, machineID, url.Values{"cursor": {"not-a-cursor"}})
	if code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d %s", code, raw)
	}
	assertErrorResponse(t, raw)
}

// AC6: malformed ts and excessively future-dated timestamps → 400.
func TestEvents_AC6_TimestampValidation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "ev-ts-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	future := now.Add(store.EventMaxFutureSkew + time.Minute)
	code, raw := postEvents(t, base, agent, eventsBody(machineID,
		eventItem("future", future, "start", map[string]any{}),
	))
	if code != http.StatusBadRequest {
		t.Fatalf("future: %d %s", code, raw)
	}
	assertErrorResponse(t, raw)

	malformed := fmt.Sprintf(
		`{"machine_id":%q,"events":[{"event_id":"bad","ts":"not-a-timestamp","type":"start","payload":{}}]}`,
		machineID,
	)
	code, raw = postEventsRaw(t, base, agent, []byte(malformed))
	if code != http.StatusBadRequest {
		t.Fatalf("malformed: %d %s", code, raw)
	}
	assertErrorResponse(t, raw)

	// Exactly at skew boundary is accepted.
	edge := now.Add(store.EventMaxFutureSkew)
	code, raw = postEvents(t, base, agent, eventsBody(machineID,
		eventItem("edge", edge, "start", map[string]any{}),
	))
	if code != http.StatusNoContent {
		t.Fatalf("edge: %d %s", code, raw)
	}
}

// AC8: partial-batch failure is all-or-nothing (nothing persisted on validation error).
func TestEvents_AC8_AllOrNothingValidation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "ev-aon-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	ts := now.Add(-time.Minute)
	future := now.Add(store.EventMaxFutureSkew + time.Minute)

	code, raw := postEvents(t, base, agent, eventsBody(machineID,
		eventItem("ok1", ts, "start", map[string]any{}),
		eventItem("bad", future, "start", map[string]any{}),
		eventItem("ok2", ts, "start", map[string]any{}),
	))
	if code != http.StatusBadRequest {
		t.Fatalf("partial: %d %s", code, raw)
	}
	assertErrorResponse(t, raw)

	code, raw = listEvents(t, base, pat, machineID, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, raw)
	}
	page := decodeEventList(t, raw)
	if len(page.Events) != 0 {
		t.Fatalf("expected nothing persisted, got %#v", page.Events)
	}
}

func TestEvents_TypeAndTimeFilters(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "ev-filt-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	t0 := now.Add(-3 * time.Minute)
	t1 := now.Add(-2 * time.Minute)
	t2 := now.Add(-1 * time.Minute)

	code, raw := postEvents(t, base, agent, eventsBody(machineID,
		eventItem("a", t0, "start", map[string]any{}),
		eventItem("b", t1, "update", map[string]any{}),
		eventItem("c", t2, "start", map[string]any{}),
	))
	if code != http.StatusNoContent {
		t.Fatalf("ingest: %d %s", code, raw)
	}

	code, raw = listEvents(t, base, pat, machineID, url.Values{"type": {"start"}})
	if code != http.StatusOK {
		t.Fatalf("type filter: %d %s", code, raw)
	}
	page := decodeEventList(t, raw)
	if len(page.Events) != 2 || page.Events[0].EventID != "a" || page.Events[1].EventID != "c" {
		t.Fatalf("type filter: %#v", page.Events)
	}

	code, raw = listEvents(t, base, pat, machineID, url.Values{
		"from": {t1.Format(time.RFC3339Nano)},
		"to":   {t2.Format(time.RFC3339Nano)},
	})
	if code != http.StatusOK {
		t.Fatalf("range: %d %s", code, raw)
	}
	page = decodeEventList(t, raw)
	if len(page.Events) != 1 || page.Events[0].EventID != "b" {
		t.Fatalf("range: %#v", page.Events)
	}
}
