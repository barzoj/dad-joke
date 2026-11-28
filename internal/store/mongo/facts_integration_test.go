package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestFacts_InsertBatchIdempotentAndMixed(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	stream := uuid.NewString()
	mk := func(idx int32) store.Fact {
		return store.Fact{
			TenantID: "t1", MachineID: "m1", StreamID: stream, Seq: 1, Idx: idx,
			TS: time.Now().UTC(), TSServer: time.Now().UTC(), Kind: "proc.sample", Plugin: "p",
		}
	}
	n, err := st.Facts().InsertBatch(ctx, []store.Fact{mk(0), mk(1)})
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, err = st.Facts().InsertBatch(ctx, []store.Fact{mk(0), mk(1), mk(2)})
	if err != nil || n != 1 {
		t.Fatalf("mixed n=%d err=%v", n, err)
	}
}

func TestUsage_GuardedIncOutOfOrderAndStreams(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	d := []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 10}}
	n, err := st.Usage().Apply(ctx, "t", "m", "s", 1, d)
	if err != nil || n != 1 {
		t.Fatalf("first n=%d err=%v", n, err)
	}
	n, err = st.Usage().Apply(ctx, "t", "m", "s", 1, d)
	if err != nil || n != 0 {
		t.Fatalf("dup n=%d err=%v", n, err)
	}
	n, err = st.Usage().Apply(ctx, "t", "m", "s", 5, []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 20}})
	if err != nil || n != 1 {
		t.Fatalf("seq5 n=%d err=%v", n, err)
	}
	// Out-of-order: persist nothing to usage (facts would still store separately).
	n, err = st.Usage().Apply(ctx, "t", "m", "s", 1, d)
	if err != nil || n != 0 {
		t.Fatalf("ooo n=%d err=%v", n, err)
	}
	n, err = st.Usage().Apply(ctx, "t", "m", "other", 1, d)
	if err != nil || n != 1 {
		t.Fatalf("other stream n=%d err=%v", n, err)
	}
	rows, err := st.Usage().ListByMachine(ctx, "t", "m", "2026-11-28")
	if err != nil || len(rows) != 1 || rows[0].Seconds != 40 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestFacts_IndexTTLDefaultAndCollMod(t *testing.T) {
	uri := mongoURI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	dbName := "dadjoke_ttl_" + uuid.NewString()[:8]
	st, err := Open(ctx, Config{URI: uri, Database: dbName, FactsRetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.db.Drop(context.Background())
		_ = st.Close(context.Background())
	})
	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	expire := readFactsTTL(t, st)
	if expire != store.FactTTLSecondsFor(30) {
		t.Fatalf("default TTL=%d", expire)
	}

	// Re-open with 7 days and EnsureIndexes → collMod
	st7, err := Open(ctx, Config{URI: uri, Database: dbName, FactsRetentionDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st7.Close(context.Background()) })
	if err := st7.EnsureIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	expire = readFactsTTL(t, st7)
	if expire != store.FactTTLSecondsFor(7) {
		t.Fatalf("collMod TTL=%d want %d", expire, store.FactTTLSecondsFor(7))
	}
}

func readFactsTTL(t *testing.T, st *Store) int32 {
	t.Helper()
	cur, err := st.coll(collFacts).Indexes().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closeCursor(context.Background(), cur)
	for cur.Next(context.Background()) {
		var idx bson.M
		if err := cur.Decode(&idx); err != nil {
			t.Fatal(err)
		}
		if idx["name"] != "ttl_facts_ts_server" {
			continue
		}
		return expireAfterSecondsFromBSON(idx["expireAfterSeconds"])
	}
	t.Fatal("ttl index missing")
	return 0
}

func countFacts(t *testing.T, st *Store, tenantID, machineID, streamID string) int64 {
	t.Helper()
	n, err := st.coll(collFacts).CountDocuments(context.Background(), bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "machine_id", Value: machineID},
		{Key: "stream_id", Value: streamID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFacts_OutOfOrderSeqPersistedNoUsageInc(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	stream := uuid.NewString()
	now := time.Now().UTC()
	mk := func(seq int64) store.Fact {
		return store.Fact{
			TenantID: "t", MachineID: "m", StreamID: stream, Seq: seq, Idx: 0,
			TS: now, TSServer: now, Kind: "proc.sample", Plugin: "p", DurS: int32(seq),
		}
	}
	delta := func(sec int64) []store.UsageDelta {
		return []store.UsageDelta{{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: sec}}
	}

	if n, err := st.Facts().InsertBatch(ctx, []store.Fact{mk(5)}); err != nil || n != 1 {
		t.Fatalf("insert seq5 n=%d err=%v", n, err)
	}
	if n, err := st.Usage().Apply(ctx, "t", "m", stream, 5, delta(50)); err != nil || n != 1 {
		t.Fatalf("apply seq5 n=%d err=%v", n, err)
	}
	if countFacts(t, st, "t", "m", stream) != 1 {
		t.Fatal("want 1 fact after seq5")
	}

	// NEW (S,1) after (S,5): facts grow; usage seconds unchanged.
	if n, err := st.Facts().InsertBatch(ctx, []store.Fact{mk(1)}); err != nil || n != 1 {
		t.Fatalf("insert seq1 n=%d err=%v", n, err)
	}
	if n, err := st.Usage().Apply(ctx, "t", "m", stream, 1, delta(10)); err != nil || n != 0 {
		t.Fatalf("apply seq1 ooo n=%d err=%v", n, err)
	}
	if countFacts(t, st, "t", "m", stream) != 2 {
		t.Fatalf("want 2 facts after ooo seq1, got %d", countFacts(t, st, "t", "m", stream))
	}
	rows, err := st.Usage().ListByMachine(ctx, "t", "m", "2026-11-28")
	if err != nil || len(rows) != 1 || rows[0].Seconds != 50 {
		t.Fatalf("usage must stay 50: %v err=%v", rows, err)
	}
}

func TestFacts_TenantIsolationDAL(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_, err := st.Facts().InsertBatch(ctx, []store.Fact{{
		TenantID: "a", MachineID: "m", StreamID: "s", Seq: 1, Idx: 0,
		TS: time.Now().UTC(), TSServer: time.Now().UTC(), Kind: "x",
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Usage().Apply(ctx, "a", "m", "s", 1, []store.UsageDelta{
		{Date: "2026-11-28", TimeZone: "UTC", Bucket: "app:a.exe", Seconds: 1},
	})
	rows, err := st.Usage().ListByMachine(ctx, "b", "m", "2026-11-28")
	if err != nil || len(rows) != 0 {
		t.Fatalf("%v %v", rows, err)
	}
}
