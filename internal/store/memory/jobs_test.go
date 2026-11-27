package memory_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestJobStore_ExpiryAndRace(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)
	machineID := uuid.NewString()

	alive := &store.Job{TenantID: "t", MachineID: machineID, Payload: "a", ExpiresAt: now.Add(time.Hour)}
	expired := &store.Job{TenantID: "t", MachineID: machineID, Payload: "e", ExpiresAt: now.Add(-time.Second)}
	if err := st.Jobs().Create(ctx, alive); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Create(ctx, expired); err != nil {
		t.Fatal(err)
	}

	pending, err := st.Jobs().ListPendingIDs(ctx, "t", machineID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != alive.ID {
		t.Fatalf("pending = %#v", pending)
	}
	got, err := st.Jobs().Get(ctx, "t", expired.ID)
	if err != nil || got.Status != store.JobTimedOut {
		t.Fatalf("expired transition: %v %+v", err, got)
	}
	if _, err := st.Jobs().Claim(ctx, "t", machineID, expired.ID, now); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("claim expired: %v", err)
	}

	race := &store.Job{TenantID: "t", MachineID: machineID, Payload: "r", ExpiresAt: now.Add(time.Hour)}
	if err := st.Jobs().Create(ctx, race); err != nil {
		t.Fatal(err)
	}
	var ok, conflict atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.Jobs().Claim(ctx, "t", machineID, race.ID, now)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, store.ErrConflict):
				conflict.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || conflict.Load() != 63 {
		t.Fatalf("ok=%d conflict=%d", ok.Load(), conflict.Load())
	}
}
