package bootstrap_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/barzoj/dad-joke/internal/tokenhash"
)

func TestRun_Success(t *testing.T) {
	t.Parallel()
	st := memory.New()
	res, err := bootstrap.Run(context.Background(), st, "Acme")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.TenantID == "" || res.TokenID == "" {
		t.Fatalf("missing ids: %+v", res)
	}
	if !strings.HasPrefix(res.RawKey, "dj_pat_") {
		t.Fatalf("raw key prefix = %q", res.RawKey)
	}
	if !strings.HasPrefix(res.RawKey, res.Prefix) {
		t.Fatalf("prefix not part of raw key")
	}

	ten, err := st.Tenants().GetByName(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if ten.ID != res.TenantID {
		t.Fatalf("tenant id mismatch")
	}

	tok, err := st.Tokens().GetByID(context.Background(), res.TenantID, res.TokenID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if tok.Hash != tokenhash.Hash(res.RawKey) {
		t.Fatalf("stored hash does not match raw key")
	}
	if tok.Hash == res.RawKey || strings.Contains(tok.Hash, res.RawKey) {
		t.Fatalf("raw key leaked into hash field")
	}
	if tok.Kind != store.TokenKindPAT {
		t.Fatalf("kind = %q", tok.Kind)
	}

	audits, err := st.Audits().ListByTenant(context.Background(), res.TenantID, 10)
	if err != nil {
		t.Fatalf("audits: %v", err)
	}
	if len(audits) != 1 || audits[0].Action != "token.issue" {
		t.Fatalf("audits = %+v", audits)
	}
	for _, a := range audits {
		for _, v := range a.Details {
			if strings.Contains(v, res.RawKey) {
				t.Fatalf("raw key leaked into audit details")
			}
		}
	}
}

func TestRun_EmptyTenant(t *testing.T) {
	t.Parallel()
	_, err := bootstrap.Run(context.Background(), memory.New(), "  ")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRun_NilStore(t *testing.T) {
	t.Parallel()
	_, err := bootstrap.Run(context.Background(), nil, "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRun_DuplicateTenant(t *testing.T) {
	t.Parallel()
	st := memory.New()
	if _, err := bootstrap.Run(context.Background(), st, "dup"); err != nil {
		t.Fatal(err)
	}
	_, err := bootstrap.Run(context.Background(), st, "dup")
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err = %v, want conflict", err)
	}
}

func TestPrintRawKey(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := bootstrap.PrintRawKey(&buf, "dj_pat_abc"); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "dj_pat_abc\n" {
		t.Fatalf("got %q", got)
	}
}
