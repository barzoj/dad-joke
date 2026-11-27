package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

// AC1/AC2/AC5 at the Mongo DAL: versioning, conflict, missing get.
func TestIntegration_PolicyVersioningAndConflict(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ten := &store.Tenant{Name: "pol-ver-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()

	pol := &store.Policy{TenantID: ten.ID, MachineID: machineID, Document: json.RawMessage(`{"v":1}`)}
	if err := st.Policies().Put(ctx, pol, 0); err != nil || pol.Version != 1 {
		t.Fatalf("first put: %v %+v", err, pol)
	}
	if err := st.Policies().Put(ctx, pol, 1); err != nil || pol.Version != 2 {
		t.Fatalf("second put: %v %+v", err, pol)
	}
	stale := &store.Policy{TenantID: ten.ID, MachineID: machineID, Document: json.RawMessage(`{"v":0}`)}
	if err := st.Policies().Put(ctx, stale, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	got, err := st.Policies().Get(ctx, ten.ID, machineID)
	if err != nil || got.Version != 2 {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := st.Policies().Get(ctx, ten.ID, uuid.NewString()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := st.Policies().Get(ctx, "other-tenant", machineID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
}

// AC6: concurrent Put with same expected_version under -race.
func TestIntegration_PolicyConcurrentPutRace(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ten := &store.Tenant{Name: "pol-race-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()
	seed := &store.Policy{TenantID: ten.ID, MachineID: machineID, Document: json.RawMessage(`{"n":0}`)}
	if err := st.Policies().Put(ctx, seed, 0); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var (
		wg       sync.WaitGroup
		okCount  atomic.Int32
		conflict atomic.Int32
		start    = make(chan struct{})
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			p := &store.Policy{
				TenantID:  ten.ID,
				MachineID: machineID,
				Document:  json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
			}
			err := st.Policies().Put(ctx, p, 1)
			switch {
			case err == nil:
				okCount.Add(1)
			case errors.Is(err, store.ErrConflict):
				conflict.Add(1)
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if okCount.Load() != 1 {
		t.Fatalf("ok=%d, want 1", okCount.Load())
	}
	if conflict.Load() != int32(n-1) {
		t.Fatalf("conflict=%d, want %d", conflict.Load(), n-1)
	}
	got, err := st.Policies().Get(ctx, ten.ID, machineID)
	if err != nil || got.Version != 2 {
		t.Fatalf("final: %v %+v", err, got)
	}
}

func TestIntegration_PolicyConcurrentFirstWrite(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ten := &store.Tenant{Name: "pol-first-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()

	const n = 8
	var (
		wg       sync.WaitGroup
		okCount  atomic.Int32
		conflict atomic.Int32
		start    = make(chan struct{})
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			p := &store.Policy{
				TenantID:  ten.ID,
				MachineID: machineID,
				Document:  json.RawMessage(fmt.Sprintf(`{"n":%d}`, i)),
			}
			err := st.Policies().Put(ctx, p, 0)
			switch {
			case err == nil:
				okCount.Add(1)
			case errors.Is(err, store.ErrConflict):
				conflict.Add(1)
			default:
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if okCount.Load() != 1 {
		t.Fatalf("ok=%d, want 1", okCount.Load())
	}
	if conflict.Load() != int32(n-1) {
		t.Fatalf("conflict=%d, want %d", conflict.Load(), n-1)
	}
	got, err := st.Policies().Get(ctx, ten.ID, machineID)
	if err != nil || got.Version != 1 {
		t.Fatalf("final: %v %+v", err, got)
	}
}
