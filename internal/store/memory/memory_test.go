package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
)

func TestTenantTokenMachineAudit(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()

	if err := st.Tenants().Create(ctx, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("nil tenant: %v", err)
	}
	ten := &store.Tenant{Name: "acme"}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	if err := st.Tenants().Create(ctx, &store.Tenant{Name: "acme"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("dup: %v", err)
	}
	byID, err := st.Tenants().GetByID(ctx, ten.ID)
	if err != nil || byID.Name != "acme" {
		t.Fatalf("GetByID: %v %+v", err, byID)
	}
	if _, err := st.Tenants().GetByID(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := st.Tenants().GetByName(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing name: %v", err)
	}

	tok := &store.Token{TenantID: ten.ID, Kind: store.TokenKindPAT, Name: "k", Prefix: "dj_pat_xx", Hash: "h1"}
	if err := st.Tokens().Create(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := st.Tokens().Create(ctx, &store.Token{TenantID: ten.ID, Kind: store.TokenKindPAT, Hash: "h1"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("dup hash: %v", err)
	}
	if err := st.Tokens().Create(ctx, &store.Token{}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid token: %v", err)
	}
	gotTok, err := st.Tokens().GetByID(ctx, ten.ID, tok.ID)
	if err != nil || gotTok.Hash != "h1" {
		t.Fatalf("GetByID token: %v %+v", err, gotTok)
	}
	if _, err := st.Tokens().GetByID(ctx, "other", tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
	byHash, err := st.Tokens().GetByHash(ctx, store.TokenKindPAT, "h1")
	if err != nil || byHash.ID != tok.ID {
		t.Fatalf("GetByHash: %v %+v", err, byHash)
	}
	list, err := st.Tokens().ListByTenant(ctx, ten.ID, store.TokenKindPAT)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	now := time.Now().UTC()
	if err := st.Tokens().TouchLastUsed(ctx, ten.ID, tok.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Tokens().Revoke(ctx, ten.ID, tok.ID, now); err != nil {
		t.Fatal(err)
	}
	revoked, _ := st.Tokens().GetByID(ctx, ten.ID, tok.ID)
	if revoked.RevokedAt == nil {
		t.Fatal("expected revoked")
	}

	mach := &store.Machine{ID: "m1", TenantID: ten.ID, Hostname: "h"}
	if err := st.Machines().Upsert(ctx, mach); err != nil {
		t.Fatal(err)
	}
	if err := st.Machines().Upsert(ctx, &store.Machine{}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid machine: %v", err)
	}
	got, err := st.Machines().Get(ctx, ten.ID, "m1")
	if err != nil || got.Hostname != "h" {
		t.Fatalf("%v %+v", err, got)
	}
	machines, err := st.Machines().ListByTenant(ctx, ten.ID)
	if err != nil || len(machines) != 1 {
		t.Fatalf("list machines: %v %+v", err, machines)
	}

	if err := st.Audits().Append(ctx, &store.AuditEntry{TenantID: ten.ID, Action: "x", ActorID: "a", Resource: "r", Details: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Audits().Append(ctx, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("nil audit: %v", err)
	}
	audits, err := st.Audits().ListByTenant(ctx, ten.ID, 10)
	if err != nil || len(audits) != 1 || audits[0].Details["k"] != "v" {
		t.Fatalf("audits: %v %+v", err, audits)
	}
}

func TestPolicyJobEventUser(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()

	pol := &store.Policy{TenantID: "t", MachineID: "m", Document: json.RawMessage(`{}`)}
	if err := st.Policies().Put(ctx, pol, 0); err != nil || pol.Version != 1 {
		t.Fatalf("%v %+v", err, pol)
	}
	gotPol, err := st.Policies().Get(ctx, "t", "m")
	if err != nil || gotPol.Version != 1 {
		t.Fatalf("get policy: %v %+v", err, gotPol)
	}
	if _, err := st.Policies().Get(ctx, "t", "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing policy: %v", err)
	}
	if err := st.Policies().Put(ctx, pol, 99); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	if err := st.Policies().Put(ctx, pol, 1); err != nil || pol.Version != 2 {
		t.Fatalf("%v %+v", err, pol)
	}
	if err := st.Policies().Put(ctx, &store.Policy{}, 0); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid policy: %v", err)
	}

	job := &store.Job{TenantID: "t", MachineID: "m", Payload: "x", ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Jobs().Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Create(ctx, &store.Job{}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid job: %v", err)
	}
	gotJob, err := st.Jobs().Get(ctx, "t", job.ID)
	if err != nil || gotJob.Payload != "x" {
		t.Fatalf("get job: %v %+v", err, gotJob)
	}
	jobs, err := st.Jobs().ListByMachine(ctx, "t", "m")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list jobs: %v %+v", err, jobs)
	}
	pending, err := st.Jobs().ListPendingIDs(ctx, "t", "m", time.Now())
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %v %+v", err, pending)
	}
	claimed, err := st.Jobs().Claim(ctx, "t", "m", job.ID, time.Now())
	if err != nil || claimed.Status != store.JobRunning {
		t.Fatalf("%v %+v", err, claimed)
	}
	if _, err := st.Jobs().Claim(ctx, "t", "m", job.ID, time.Now()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
	code := 0
	dur := int64(5)
	if err := st.Jobs().Complete(ctx, "t", job.ID, store.JobResult{
		Status: store.JobSucceeded, ExitCode: &code, Stdout: "ok", DurationMS: &dur, FinishedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Complete(ctx, "t", job.ID, store.JobResult{Status: store.JobFailed, FinishedAt: time.Now()}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("complete twice: %v", err)
	}

	job2 := &store.Job{TenantID: "t", MachineID: "m", Payload: "y", ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.Jobs().Create(ctx, job2); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Cancel(ctx, "t", job2.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().Cancel(ctx, "t", job2.ID, time.Now()); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel twice: %v", err)
	}

	evs := []store.Event{{TenantID: "t", MachineID: "m", EventID: "e1", Type: "start", TS: time.Now()}}
	if err := st.Events().InsertBatch(ctx, evs); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().InsertBatch(ctx, evs); err != nil {
		t.Fatal(err)
	}
	if err := st.Events().InsertBatch(ctx, []store.Event{{}}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("invalid event: %v", err)
	}
	list, err := st.Events().List(ctx, store.EventQuery{TenantID: "t", MachineID: "m", Type: "start", Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %+v", err, list)
	}

	user := &store.User{TenantID: "t", Email: "a@b.c", PasswordHash: "x"}
	if err := st.Users().Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().Create(ctx, &store.User{TenantID: "t", Email: "A@B.C"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("email: %v", err)
	}
	gotUser, err := st.Users().GetByID(ctx, "t", user.ID)
	if err != nil || gotUser.Email != "a@b.c" {
		t.Fatalf("get user: %v %+v", err, gotUser)
	}
	byEmail, err := st.Users().GetByEmail(ctx, "a@b.c")
	if err != nil || byEmail.ID != user.ID {
		t.Fatalf("by email: %v %+v", err, byEmail)
	}
	ident := &store.UserIdentity{UserID: user.ID, Provider: "google", Subject: "sub"}
	if err := st.Users().CreateIdentity(ctx, ident); err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateIdentity(ctx, ident); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("dup identity: %v", err)
	}
	if err := st.Tenants().Delete(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete missing tenant: %v", err)
	}
	ten := &store.Tenant{Name: "to-delete"}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatal(err)
	}
	if err := st.Tenants().Delete(ctx, ten.ID); err != nil {
		t.Fatal(err)
	}

	ses := &store.Session{UserID: user.ID, TenantID: "t", Hash: "abc", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := st.Sessions().Create(ctx, ses); err != nil {
		t.Fatal(err)
	}
	gotSes, err := st.Sessions().GetByHash(ctx, "abc")
	if err != nil || gotSes.ID != ses.ID {
		t.Fatalf("get session: %v %+v", err, gotSes)
	}
	if err := st.Sessions().Revoke(ctx, ses.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().Revoke(ctx, ses.ID, time.Now().UTC()); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	gotSes, err = st.Sessions().GetByID(ctx, ses.ID)
	if err != nil || gotSes.RevokedAt == nil {
		t.Fatalf("revoked: %v %+v", err, gotSes)
	}

	gotIdent, err := st.Users().GetIdentity(ctx, "google", "sub")
	if err != nil || gotIdent.UserID != user.ID {
		t.Fatalf("get identity: %v %+v", err, gotIdent)
	}
}

func TestPingErrorAndLifecycle(t *testing.T) {
	t.Parallel()
	st := memory.New()
	if err := st.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	st.SetPingError(errors.New("down"))
	if err := st.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error")
	}
	if err := st.EnsureIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
