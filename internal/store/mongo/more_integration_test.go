package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
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
	code := 0
	dur := int64(3)
	if err := st.Jobs().Complete(ctx, ten.ID, job.ID, store.JobResult{
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
	if err != nil || len(listed) != 1 {
		t.Fatalf("list events: %v %+v", err, listed)
	}

	if err := st.Tokens().TouchLastUsed(ctx, ten.ID, "missing", time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("touch missing: %v", err)
	}
	if err := st.Tokens().Revoke(ctx, ten.ID, "missing", time.Now().UTC()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoke missing: %v", err)
	}
}
