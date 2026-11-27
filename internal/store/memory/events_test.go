package memory_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
)

func TestMemoryEvents_IdempotencyOrderCursorTenant(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	ts := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

	batch := []store.Event{
		{TenantID: "t1", MachineID: "m", EventID: "same", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
		{TenantID: "t1", MachineID: "m", EventID: "same", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
		{TenantID: "t1", MachineID: "m", EventID: "b", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
		{TenantID: "t1", MachineID: "m", EventID: "a", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
		{TenantID: "t1", MachineID: "m", EventID: "c", Type: "update", TS: ts.Add(time.Second), Payload: json.RawMessage(`{}`)},
	}
	if err := st.Events().InsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().InsertBatch(ctx, []store.Event{batch[0]}); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().InsertBatch(ctx, []store.Event{
		{TenantID: "t2", MachineID: "m", EventID: "other", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}

	page, err := st.Events().List(ctx, store.EventQuery{TenantID: "t1", MachineID: "m", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "same", "c"}
	if len(page.Events) != len(want) {
		t.Fatalf("got %#v", page.Events)
	}
	for i, id := range want {
		if page.Events[i].EventID != id {
			t.Fatalf("order[%d]=%s want %s", i, page.Events[i].EventID, id)
		}
	}

	pageB, err := st.Events().List(ctx, store.EventQuery{TenantID: "t2", MachineID: "m", Limit: 10})
	if err != nil || len(pageB.Events) != 1 || pageB.Events[0].EventID != "other" {
		t.Fatalf("isolation: %v %#v", err, pageB)
	}

	p1, err := st.Events().List(ctx, store.EventQuery{TenantID: "t1", MachineID: "m", Limit: 2})
	if err != nil || p1.NextCursor == "" {
		t.Fatalf("p1: %v %#v", err, p1)
	}
	p2, err := st.Events().List(ctx, store.EventQuery{
		TenantID: "t1", MachineID: "m", Limit: 2, Cursor: p1.NextCursor,
	})
	if err != nil || len(p2.Events) != 2 || p2.Events[0].EventID != "same" {
		t.Fatalf("p2: %v %#v", err, p2)
	}

	_, err = st.Events().List(ctx, store.EventQuery{TenantID: "t1", MachineID: "m", Cursor: "bad"})
	if err == nil {
		t.Fatal("expected invalid cursor error")
	}
}
