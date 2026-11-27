package tokenhash_test

import (
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
)

func TestGeneratePAT(t *testing.T) {
	t.Parallel()
	raw, prefix, hash, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(raw, "dj_pat_") {
		t.Fatalf("raw prefix = %q", raw[:7])
	}
	if len(raw) != len("dj_pat_")+64 {
		t.Fatalf("raw len = %d, want %d", len(raw), len("dj_pat_")+64)
	}
	if !strings.HasPrefix(raw, prefix) {
		t.Fatalf("prefix %q is not a prefix of raw", prefix)
	}
	if len(prefix) != len("dj_pat_")+8 {
		t.Fatalf("prefix len = %d", len(prefix))
	}
	if hash != tokenhash.Hash(raw) {
		t.Fatalf("hash mismatch")
	}
	if strings.Contains(hash, raw) {
		t.Fatalf("hash unexpectedly contains raw secret")
	}
}

func TestGenerateAgent(t *testing.T) {
	t.Parallel()
	raw, prefix, hash, err := tokenhash.Generate(store.TokenKindAgent)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(raw, "dj_agt_") {
		t.Fatalf("raw = %q", raw)
	}
	if !strings.HasPrefix(raw, prefix) {
		t.Fatalf("prefix mismatch")
	}
	if !tokenhash.EqualHash(hash, raw) {
		t.Fatalf("Equal hash failed")
	}
}

func TestGenerateUnknownKind(t *testing.T) {
	t.Parallel()
	_, _, _, err := tokenhash.Generate(store.TokenKind("nope"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEqualHashRejectsMismatch(t *testing.T) {
	t.Parallel()
	raw, _, hash, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		t.Fatal(err)
	}
	if tokenhash.EqualHash(hash, raw+"x") {
		t.Fatal("expected mismatch")
	}
	if tokenhash.EqualHash("abc", raw) {
		t.Fatal("expected length mismatch reject")
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	if got := tokenhash.KindOf("dj_pat_abc"); got != store.TokenKindPAT {
		t.Fatalf("got %q", got)
	}
	if got := tokenhash.KindOf("dj_agt_abc"); got != store.TokenKindAgent {
		t.Fatalf("got %q", got)
	}
	if got := tokenhash.KindOf("other"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestGenerateSession(t *testing.T) {
	t.Parallel()
	raw, hash, err := tokenhash.GenerateSession()
	if err != nil {
		t.Fatal(err)
	}
	if !tokenhash.IsSession(raw) {
		t.Fatalf("raw = %q", raw)
	}
	if !tokenhash.EqualHash(hash, raw) {
		t.Fatal("hash mismatch")
	}
	if tokenhash.KindOf(raw) != "" {
		t.Fatal("session must not be a TokenKind")
	}
}

func TestGenerateUnique(t *testing.T) {
	t.Parallel()
	a, _, ah, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		t.Fatal(err)
	}
	b, _, bh, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || ah == bh {
		t.Fatal("expected unique secrets")
	}
}
