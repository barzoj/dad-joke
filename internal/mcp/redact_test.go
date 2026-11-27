package mcp_test

import (
	"strings"
	"testing"

	djmcp "github.com/barzoj/dad-joke/internal/mcp"
	"github.com/barzoj/dad-joke/internal/config"
)

func TestRedactSecrets_PatternsAndKnown(t *testing.T) {
	t.Parallel()
	pat := "dj_pat_" + strings.Repeat("a", 64)
	agt := "dj_agt_" + strings.Repeat("b", 64)
	ses := "dj_ses_" + strings.Repeat("c", 64)
	in := "auth failed for " + pat + " and " + agt + " cookie=" + ses
	out := djmcp.RedactSecrets(in, pat)
	if strings.Contains(out, pat) || strings.Contains(out, agt) || strings.Contains(out, ses) {
		t.Fatalf("raw token leaked: %q", out)
	}
	if !strings.Contains(out, config.RedactedToken) {
		t.Fatalf("expected redaction marker in %q", out)
	}
}
