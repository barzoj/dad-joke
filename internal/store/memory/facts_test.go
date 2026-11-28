package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestFactStore_InsertBatchIdempotent(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	f := store.Fact{
		TenantID: "t", MachineID: "m", StreamID: "s", Seq: 1, Idx: 0,
		TS: time.Now().UTC(), TSServer: time.Now().UTC(), Kind: "proc.sample",
	}
	n, err := st.Facts().InsertBatch(ctx, []store.Fact{f, f})
	if err != nil || n != 1 {
		t.Fatalf("within-batch n=%d err=%v", n, err)
	}
	n, err = st.Facts().InsertBatch(ctx, []store.Fact{f})
	if err != nil || n != 0 {
		t.Fatalf("replay n=%d err=%v", n, err)
	}
	if st.FactCount() != 1 {
		t.Fatal(st.FactCount())
	}
}

func TestUsageStore_ApplyGuardedAndOutOfOrder(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	deltas := []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 10}}
	n, err := st.Usage().Apply(ctx, "t", "m", "s", 1, deltas)
	if err != nil || n != 1 {
		t.Fatalf("first apply n=%d err=%v", n, err)
	}
	n, err = st.Usage().Apply(ctx, "t", "m", "s", 1, deltas)
	if err != nil || n != 0 {
		t.Fatalf("replay n=%d err=%v", n, err)
	}
	n, err = st.Usage().Apply(ctx, "t", "m", "s", 5, []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 20}})
	if err != nil || n != 1 {
		t.Fatalf("seq5 n=%d err=%v", n, err)
	}
	// Out-of-order seq=1 after seq=5: no increment
	n, err = st.Usage().Apply(ctx, "t", "m", "s", 1, deltas)
	if err != nil || n != 0 {
		t.Fatalf("ooo n=%d err=%v", n, err)
	}
	rows, err := st.Usage().ListByMachine(ctx, "t", "m", "2026-11-28")
	if err != nil || len(rows) != 1 || rows[0].Seconds != 30 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	// Independent stream
	n, err = st.Usage().Apply(ctx, "t", "m", "s2", 1, deltas)
	if err != nil || n != 1 {
		t.Fatalf("stream2 n=%d err=%v", n, err)
	}
	rows, _ = st.Usage().ListByMachine(ctx, "t", "m", "2026-11-28")
	if rows[0].Seconds != 40 {
		t.Fatalf("seconds=%d", rows[0].Seconds)
	}
}

func TestFactUsage_ConcurrentSameBatch(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	stream := uuid.NewString()
	facts := []store.Fact{{
		TenantID: "t", MachineID: "m", StreamID: stream, Seq: 1, Idx: 0,
		Kind: "proc.sample", TS: time.Now().UTC(), TSServer: time.Now().UTC(),
	}}
	deltas := []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 7}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = st.Facts().InsertBatch(ctx, append([]store.Fact(nil), facts...))
			_, _ = st.Usage().Apply(ctx, "t", "m", stream, 1, deltas)
		}()
	}
	wg.Wait()
	if st.FactCount() != 1 {
		t.Fatalf("facts=%d", st.FactCount())
	}
	rows, _ := st.Usage().ListByMachine(ctx, "t", "m", "2026-11-28")
	if len(rows) != 1 || rows[0].Seconds != 7 {
		t.Fatalf("rows=%v", rows)
	}
}

func TestFactStore_FaultInjectionHook(t *testing.T) {
	t.Parallel()
	st := memory.New()
	st.AfterFactInsertErr = errors.New("boom")
	ctx := context.Background()
	n, err := st.Facts().InsertBatch(ctx, []store.Fact{{
		TenantID: "t", MachineID: "m", StreamID: "s", Seq: 1, Idx: 0, Kind: "x",
	}})
	if n != 1 || err == nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if st.FactCount() != 1 {
		t.Fatal("should keep inserted rows")
	}
}

func TestFacts_OutOfOrderSeqPersistedNoUsageInc(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	stream := uuid.NewString()
	now := time.Now().UTC()
	mk := func(seq int64) store.Fact {
		return store.Fact{
			TenantID: "t", MachineID: "m", StreamID: stream, Seq: seq, Idx: 0,
			TS: now, TSServer: now, Kind: "proc.sample", DurS: int32(seq),
		}
	}
	delta := func(sec int64) []store.UsageDelta {
		return []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: sec}}
	}

	if n, err := st.Facts().InsertBatch(ctx, []store.Fact{mk(5)}); err != nil || n != 1 {
		t.Fatalf("seq5 insert n=%d err=%v", n, err)
	}
	if n, err := st.Usage().Apply(ctx, "t", "m", stream, 5, delta(50)); err != nil || n != 1 {
		t.Fatalf("seq5 apply n=%d err=%v", n, err)
	}
	if st.FactCount() != 1 {
		t.Fatal(st.FactCount())
	}

	if n, err := st.Facts().InsertBatch(ctx, []store.Fact{mk(1)}); err != nil || n != 1 {
		t.Fatalf("ooo seq1 insert n=%d err=%v", n, err)
	}
	if n, err := st.Usage().Apply(ctx, "t", "m", stream, 1, delta(10)); err != nil || n != 0 {
		t.Fatalf("ooo seq1 apply n=%d err=%v", n, err)
	}
	if st.FactCount() != 2 {
		t.Fatalf("facts must grow to 2, got %d", st.FactCount())
	}
	rows, err := st.Usage().ListByMachine(ctx, "t", "m", "2026-11-28")
	if err != nil || len(rows) != 1 || rows[0].Seconds != 50 {
		t.Fatalf("usage must stay 50: %v err=%v", rows, err)
	}
}

func TestUsage_TenantIsolationAndEmpty(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	_, _ = st.Usage().Apply(ctx, "a", "m", "s", 1, []store.UsageDelta{
		{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:z.exe", Seconds: 1},
		{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 2},
	})
	rows, err := st.Usage().ListByMachine(ctx, "b", "m", "2026-11-28")
	if err != nil || len(rows) != 0 {
		t.Fatalf("cross tenant: %v %v", rows, err)
	}
	rows, err = st.Usage().ListByMachine(ctx, "a", "m", "2026-11-28")
	if err != nil || len(rows) != 2 || rows[0].Bucket != "app:a.exe" {
		t.Fatalf("sorted=%v", rows)
	}
}
