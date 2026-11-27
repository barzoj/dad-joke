package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/audit"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
)

func TestLogger_TokenIssueAndRevoke(t *testing.T) {
	t.Parallel()
	st := memory.New()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	log := audit.Logger{Store: st.Audits(), Clock: func() time.Time { return now }}

	if err := log.TokenIssue(context.Background(), "ten", "actor", "tok1", store.TokenKindAgent, "dj_agt_abcd", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := log.TokenRevoke(context.Background(), "ten", "actor", "tok1", store.TokenKindAgent); err != nil {
		t.Fatal(err)
	}
	entries, err := st.Audits().ListByTenant(context.Background(), "ten", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d", len(entries))
	}
	if entries[0].Action != audit.ActionTokenIssue || entries[1].Action != audit.ActionTokenRevoke {
		t.Fatalf("actions = %q %q", entries[0].Action, entries[1].Action)
	}
}

func TestLogger_PolicyAndScriptHooks(t *testing.T) {
	t.Parallel()
	st := memory.New()
	log := audit.Logger{Store: st.Audits()}
	if err := log.PolicyCreate(context.Background(), "ten", "actor", "mach", 2); err != nil {
		t.Fatal(err)
	}
	if err := log.ScriptLaunch(context.Background(), "ten", "actor", "job1", "mach"); err != nil {
		t.Fatal(err)
	}
	entries, err := st.Audits().ListByTenant(context.Background(), "ten", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d", len(entries))
	}
}
