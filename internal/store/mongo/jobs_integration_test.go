package mongo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

func TestIntegration_JobStore_ExpiryClaimRace(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ten := &store.Tenant{Name: "job-race-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()
	now := time.Date(2026, 11, 27, 21, 0, 0, 0, time.UTC)

	alive := &store.Job{TenantID: ten.ID, MachineID: machineID, Payload: "alive", ExpiresAt: now.Add(time.Hour)}
	expired := &store.Job{TenantID: ten.ID, MachineID: machineID, Payload: "exp", ExpiresAt: now.Add(-time.Minute)}
	if err := st.Jobs().Create(ctx, alive); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Create(ctx, expired); err != nil {
		t.Fatal(err)
	}

	pending, err := st.Jobs().ListPendingIDs(ctx, ten.ID, machineID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != alive.ID {
		t.Fatalf("pending = %#v", pending)
	}
	got, err := st.Jobs().Get(ctx, ten.ID, expired.ID)
	if err != nil || got.Status != store.JobTimedOut {
		t.Fatalf("timed_out: %v %+v", err, got)
	}
	if _, err := st.Jobs().Claim(ctx, ten.ID, machineID, expired.ID, now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("claim expired: %v", err)
	}

	// Wrong machine → not found
	if _, err := st.Jobs().Claim(ctx, ten.ID, uuid.NewString(), alive.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong machine: %v", err)
	}

	race := &store.Job{TenantID: ten.ID, MachineID: machineID, Payload: "race", ExpiresAt: now.Add(time.Hour)}
	if err := st.Jobs().Create(ctx, race); err != nil {
		t.Fatal(err)
	}
	var ok, conflict atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.Jobs().Claim(ctx, ten.ID, machineID, race.ID, now)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, store.ErrConflict):
				conflict.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("ok claims = %d, want 1", ok.Load())
	}
	if conflict.Load() != 31 {
		t.Fatalf("conflicts = %d, want 31", conflict.Load())
	}

	code := 0
	if err := st.Jobs().Complete(ctx, ten.ID, machineID, race.ID, store.JobResult{
		Status: store.JobSucceeded, ExitCode: &code, FinishedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Complete(ctx, ten.ID, machineID, race.ID, store.JobResult{
		Status: store.JobFailed, FinishedAt: now,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second complete: %v", err)
	}
}
