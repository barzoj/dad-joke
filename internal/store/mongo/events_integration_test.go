package mongo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

func TestIntegration_EventsIdempotencyOrderCursorTenant(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	tenA := &store.Tenant{Name: "ev-a-" + uuid.NewString()[:8]}
	tenB := &store.Tenant{Name: "ev-b-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, tenA); err != nil {
		t.Fatal(err)
	}
	if err := st.Tenants().Create(ctx, tenB); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()
	ts := time.Date(2026, 11, 27, 16, 0, 0, 0, time.UTC)

	batch := []store.Event{
		{TenantID: tenA.ID, MachineID: machineID, EventID: "same", Type: "start", TS: ts, Payload: json.RawMessage(`{"n":1}`)},
		{TenantID: tenA.ID, MachineID: machineID, EventID: "same", Type: "start", TS: ts, Payload: json.RawMessage(`{"n":2}`)},
		{TenantID: tenA.ID, MachineID: machineID, EventID: "b", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
		{TenantID: tenA.ID, MachineID: machineID, EventID: "a", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
		{TenantID: tenA.ID, MachineID: machineID, EventID: "c", Type: "update", TS: ts.Add(time.Second), Payload: json.RawMessage(`{}`)},
	}
	if err := st.Events().InsertBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().InsertBatch(ctx, []store.Event{batch[0]}); err != nil {
		t.Fatal(err) // across-batch idempotent
	}
	if err := st.Events().InsertBatch(ctx, []store.Event{
		{TenantID: tenB.ID, MachineID: machineID, EventID: "other", Type: "start", TS: ts, Payload: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}

	page, err := st.Events().List(ctx, store.EventQuery{TenantID: tenA.ID, MachineID: machineID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("A events = %#v", page.Events)
	}
	want := []string{"a", "b", "same", "c"}
	for i, id := range want {
		if page.Events[i].EventID != id {
			t.Fatalf("order[%d]=%s want %s (%#v)", i, page.Events[i].EventID, id, page.Events)
		}
	}

	pageB, err := st.Events().List(ctx, store.EventQuery{TenantID: tenB.ID, MachineID: machineID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(pageB.Events) != 1 || pageB.Events[0].EventID != "other" {
		t.Fatalf("tenant isolation: %#v", pageB.Events)
	}

	p1, err := st.Events().List(ctx, store.EventQuery{TenantID: tenA.ID, MachineID: machineID, Limit: 2})
	if err != nil || len(p1.Events) != 2 || p1.NextCursor == "" {
		t.Fatalf("page1: %v %#v", err, p1)
	}
	p2, err := st.Events().List(ctx, store.EventQuery{
		TenantID: tenA.ID, MachineID: machineID, Limit: 2, Cursor: p1.NextCursor,
	})
	if err != nil || len(p2.Events) != 2 {
		t.Fatalf("page2: %v %#v", err, p2)
	}
	if p2.Events[0].EventID != "same" || p2.Events[1].EventID != "c" {
		t.Fatalf("page2 order: %#v", p2.Events)
	}
}

func TestIntegration_EventsTTLIndexExact(t *testing.T) {
	// Alias coverage for AC3/AC7 naming in the issue; body shared with more_integration_test.
	TestIntegration_EventsTTLIndex(t)
}
