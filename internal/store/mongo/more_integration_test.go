package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestIntegration_UsersPoliciesJobsEvents(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	ten := &store.Tenant{Name: "full-crud"}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}

	user := &store.User{TenantID: ten.ID, Email: "u@example.com", PasswordHash: "hash"}
	if err := st.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().Create(ctx, &store.User{TenantID: ten.ID, Email: "u@example.com"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("dup email: %v", err)
	}
	gotUser, err := st.Users().GetByEmail(ctx, "u@example.com")
	if err != nil || gotUser.ID != user.ID {
		t.Fatalf("GetByEmail: %v %+v", err, gotUser)
	}
	ident := &store.UserIdentity{UserID: user.ID, Provider: "google", Subject: "sub-1"}
	if err := st.Users().CreateIdentity(ctx, ident); err != nil {
		t.Fatal(err)
	}
	gotIdent, err := st.Users().GetIdentity(ctx, "google", "sub-1")
	if err != nil || gotIdent.UserID != user.ID {
		t.Fatalf("GetIdentity: %v %+v", err, gotIdent)
	}

	machineID := uuid.NewString()
	pol := &store.Policy{TenantID: ten.ID, MachineID: machineID, Document: json.RawMessage(`{"v":1}`)}
	if err := st.Policies().Put(ctx, pol, 0); err != nil || pol.Version != 1 {
		t.Fatalf("put policy: %v %+v", err, pol)
	}
	if err := st.Policies().Put(ctx, pol, 1); err != nil || pol.Version != 2 {
		t.Fatalf("update policy: %v %+v", err, pol)
	}
	if err := st.Policies().Put(ctx, pol, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale policy: %v", err)
	}
	gotPol, err := st.Policies().Get(ctx, ten.ID, machineID)
	if err != nil || gotPol.Version != 2 {
		t.Fatalf("get policy: %v %+v", err, gotPol)
	}

	job := &store.Job{
		TenantID: ten.ID, MachineID: machineID, Payload: "echo hi",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := st.Jobs().Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	pending, err := st.Jobs().ListPendingIDs(ctx, ten.ID, machineID, time.Now().UTC())
	if err != nil || len(pending) != 1 || pending[0] != job.ID {
		t.Fatalf("pending: %v %+v", err, pending)
	}
	claimed, err := st.Jobs().Claim(ctx, ten.ID, machineID, job.ID, time.Now().UTC())
	if err != nil || claimed.Status != store.JobRunning {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	if _, err := st.Jobs().Claim(ctx, ten.ID, machineID, job.ID, time.Now().UTC()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
	code := int32(0)
	dur := int64(3)
	if err := st.Jobs().Complete(ctx, ten.ID, machineID, job.ID, store.JobResult{
		Status: store.JobSucceeded, ExitCode: &code, Stdout: "hi", DurationMS: &dur, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	job2 := &store.Job{
		TenantID: ten.ID, MachineID: machineID, Payload: "cancel me",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := st.Jobs().Create(ctx, job2); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Cancel(ctx, ten.ID, job2.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	evs := []store.Event{{
		TenantID: ten.ID, MachineID: machineID, EventID: "e-1",
		Type: "start", TS: time.Now().UTC(), Payload: json.RawMessage(`{}`),
	}}
	if err := st.Events().InsertBatch(ctx, evs); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().InsertBatch(ctx, evs); err != nil {
		t.Fatal(err) // idempotent
	}
	listed, err := st.Events().List(ctx, store.EventQuery{TenantID: ten.ID, MachineID: machineID, Limit: 10})
	if err != nil || len(listed.Events) != 1 {
		t.Fatalf("list events: %v %+v", err, listed)
	}

	if err := st.Tokens().TouchLastUsed(ctx, ten.ID, "missing", time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("touch missing: %v", err)
	}
	if err := st.Tokens().Revoke(ctx, ten.ID, "missing", time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoke missing: %v", err)
	}
}

func TestIntegration_EventsTTLIndex(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	cur, err := st.coll(collEvents).Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer closeCursor(ctx, cur)

	wantExpire := int64(store.EventTTLSeconds)
	byName := make(map[string]bson.M)
	for cur.Next(ctx) {
		var spec bson.M
		if err := cur.Decode(&spec); err != nil {
			t.Fatal(err)
		}
		name, _ := spec["name"].(string)
		byName[name] = spec
	}
	if err := cur.Err(); err != nil {
		t.Fatal(err)
	}

	// Explicitly list collection index specs (AC3 / AC7).
	required := []string{"_id_", "uniq_event_idempotency", "idx_events_tenant_machine_ts", "ttl_events_ts"}
	for _, name := range required {
		if _, ok := byName[name]; !ok {
			t.Fatalf("events index %q missing; have %#v", name, byName)
		}
	}

	ttl := byName["ttl_events_ts"]
	expire, ok := ttl["expireAfterSeconds"]
	if !ok {
		t.Fatalf("ttl index missing expireAfterSeconds: %#v", ttl)
	}
	var got int64
	switch v := expire.(type) {
	case int32:
		got = int64(v)
	case int64:
		got = v
	case int:
		got = int64(v)
	default:
		t.Fatalf("unexpected expireAfterSeconds type %T (%v)", expire, expire)
	}
	if got != wantExpire {
		t.Fatalf("expireAfterSeconds = %d, want %d (30*24*3600)", got, wantExpire)
	}
}

func TestIntegration_ListPendingIDsExcludesExpired(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ten := &store.Tenant{Name: "pending-expire-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()
	now := time.Now().UTC()

	alive := &store.Job{
		TenantID: ten.ID, MachineID: machineID, Payload: "alive",
		ExpiresAt: now.Add(time.Hour),
	}
	expired := &store.Job{
		TenantID: ten.ID, MachineID: machineID, Payload: "expired",
		ExpiresAt: now.Add(-time.Minute),
	}
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
		t.Fatalf("pending = %#v, want only %q", pending, alive.ID)
	}
	got, err := st.Jobs().Get(ctx, ten.ID, expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.JobTimedOut {
		t.Fatalf("expired status = %s, want timed_out", got.Status)
	}
}

func TestIntegration_SessionRevokeIdempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	ten := &store.Tenant{Name: "ses-revoke-" + uuid.NewString()[:8]}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	user := &store.User{TenantID: ten.ID, Email: "ses-" + uuid.NewString()[:8] + "@example.com", PasswordHash: "x"}
	if err := st.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	_, hash, err := tokenhash.GenerateSession()
	if err != nil {
		t.Fatal(err)
	}
	ses := &store.Session{
		UserID: user.ID, TenantID: ten.ID, Hash: hash,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := st.Sessions().Create(ctx, ses); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := st.Sessions().Revoke(ctx, ses.ID, at); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := st.Sessions().Revoke(ctx, ses.ID, at.Add(time.Second)); err != nil {
		t.Fatalf("second revoke (idempotent): %v", err)
	}
	got, err := st.Sessions().GetByID(ctx, ses.ID)
	if err != nil || got.RevokedAt == nil {
		t.Fatalf("revoked session: %v %+v", err, got)
	}
}
