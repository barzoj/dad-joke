// Package bootstrap creates the first tenant and control-plane PAT.
package bootstrap

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
)

// Result is returned after a successful bootstrap. RawKey must be shown once
// to the operator and must never be logged or persisted.
type Result struct {
	TenantID string
	TokenID  string
	Prefix   string
	RawKey   string
}

// Run creates a tenant named tenantName and its first dj_pat_ API key.
// The raw key is returned only in Result.RawKey (and may be printed by the CLI).
func Run(ctx context.Context, s store.Store, tenantName string) (*Result, error) {
	name := strings.TrimSpace(tenantName)
	if name == "" {
		return nil, fmt.Errorf("bootstrap: tenant name is required")
	}
	if s == nil {
		return nil, fmt.Errorf("bootstrap: store is required")
	}

	ten := &store.Tenant{Name: name}
	if err := s.Tenants().Create(ctx, ten); err != nil {
		return nil, fmt.Errorf("bootstrap: create tenant: %w", err)
	}

	raw, prefix, hash, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: generate key: %w", err)
	}

	tok := &store.Token{
		TenantID:  ten.ID,
		Kind:      store.TokenKindPAT,
		Name:      "bootstrap",
		Prefix:    prefix,
		Hash:      hash,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.Tokens().Create(ctx, tok); err != nil {
		return nil, fmt.Errorf("bootstrap: create token: %w", err)
	}

	if err := s.Audits().Append(ctx, &store.AuditEntry{
		TenantID:  ten.ID,
		Action:    "token.issue",
		ActorID:   "bootstrap",
		Resource:  "token:" + tok.ID,
		Details:   map[string]string{"kind": string(store.TokenKindPAT), "prefix": prefix, "name": tok.Name},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("bootstrap: audit append: %w", err)
	}

	return &Result{
		TenantID: ten.ID,
		TokenID:  tok.ID,
		Prefix:   prefix,
		RawKey:   raw,
	}, nil
}

// PrintRawKey writes the raw key once to w. Callers must not log the key.
func PrintRawKey(w io.Writer, raw string) error {
	_, err := fmt.Fprintln(w, raw)
	return err
}
