// Package createuser creates a tenant + email/password user (CLI / ops).
package createuser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/password"
	"github.com/barzoj/dad-joke/internal/store"
)

// Result is returned after a successful create-user.
type Result struct {
	TenantID string
	UserID   string
	Email    string
}

// Run creates a tenant and user with an argon2id password hash.
// Works even when HTTP ALLOW_SIGNUP is false.
func Run(ctx context.Context, s store.Store, email, pass, tenantName string) (*Result, error) {
	if s == nil {
		return nil, fmt.Errorf("createuser: store is required")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, fmt.Errorf("createuser: email is required")
	}
	if len(pass) < 8 {
		return nil, fmt.Errorf("createuser: password must be at least 8 characters")
	}
	name := strings.TrimSpace(tenantName)
	if name == "" {
		name = email
	}

	hash, err := password.Hash(pass)
	if err != nil {
		return nil, fmt.Errorf("createuser: hash password: %w", err)
	}

	now := time.Now().UTC()
	ten := &store.Tenant{Name: name, CreatedAt: now}
	if err := s.Tenants().Create(ctx, ten); err != nil {
		return nil, fmt.Errorf("createuser: create tenant: %w", err)
	}

	user := &store.User{
		TenantID:     ten.ID,
		Email:        email,
		PasswordHash: hash,
		CreatedAt:    now,
	}
	if err := s.Users().Create(ctx, user); err != nil {
		_ = s.Tenants().Delete(ctx, ten.ID)
		return nil, fmt.Errorf("createuser: create user: %w", err)
	}

	return &Result{TenantID: ten.ID, UserID: user.ID, Email: user.Email}, nil
}
