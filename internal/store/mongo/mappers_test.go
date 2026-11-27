package mongo

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
)

func TestTokenRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	used := now.Add(time.Minute)
	tok := &store.Token{
		ID: "t1", TenantID: "ten", Kind: store.TokenKindPAT,
		Name: "bootstrap", Prefix: "dj_pat_abcd1234", Hash: "abc",
		CreatedAt: now, LastUsedAt: &used,
	}
	got := tokenFromDoc(tokenToDoc(tok))
	if got.ID != tok.ID || got.Hash != tok.Hash || got.Kind != tok.Kind {
		t.Fatalf("got %+v", got)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Fatalf("last used = %v", got.LastUsedAt)
	}
}

func TestTenantMachineAuditRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	ten := tenantFromDoc(tenantToDoc(&store.Tenant{ID: "1", Name: "n", CreatedAt: now}))
	if ten.Name != "n" {
		t.Fatal(ten)
	}
	doc := machineToDoc(&store.Machine{
		ID: "m", TenantID: "1", Hostname: "h", OSVersion: "w",
		AgentVersion: "1", LastSeenAt: now, AppliedPolicyVersion: 2, DesiredPolicyVersion: 3,
	})
	if doc.ID != "1|m" || doc.MachineID != "m" {
		t.Fatalf("machine doc key = %+v", doc)
	}
	mach := machineFromDoc(doc)
	if mach.ID != "m" || mach.DesiredPolicyVersion != 3 {
		t.Fatal(mach)
	}
	aud := auditFromDoc(auditToDoc(&store.AuditEntry{
		ID: "a", TenantID: "1", Action: "token.issue", ActorID: "bootstrap",
		Resource: "token:x", Details: map[string]string{"prefix": "dj_pat_ab"}, CreatedAt: now,
	}))
	if aud.Details["prefix"] != "dj_pat_ab" {
		t.Fatal(aud)
	}
}

func TestPolicyJobEventRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	doc := json.RawMessage(`{"a":1}`)
	pol := policyFromDoc(policyToDoc(&store.Policy{
		TenantID: "t", MachineID: "m", Version: 2, Document: doc, UpdatedAt: now,
	}))
	if string(pol.Document) != `{"a":1}` || pol.Version != 2 {
		t.Fatal(pol)
	}
	code := 0
	dur := int64(12)
	claimed := now
	job := jobFromDoc(jobToDoc(&store.Job{
		ID: "j", TenantID: "t", MachineID: "m", Status: store.JobSucceeded,
		Payload: "echo", ExitCode: &code, Stdout: "ok", DurationMS: &dur,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), ClaimedAt: &claimed, FinishedAt: &claimed,
	}))
	if job.Status != store.JobSucceeded || job.Stdout != "ok" {
		t.Fatal(job)
	}
	ev := eventFromDoc(eventToDoc(&store.Event{
		ID: "e", TenantID: "t", MachineID: "m", EventID: "cid",
		Type: "start", TS: now, Payload: doc,
	}))
	if ev.EventID != "cid" || string(ev.Payload) != `{"a":1}` {
		t.Fatal(ev)
	}
}

func TestPolicyKey(t *testing.T) {
	t.Parallel()
	if got := policyKey("a", "b"); got != "a|b" {
		t.Fatalf("got %q", got)
	}
}
