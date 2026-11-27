package authctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/barzoj/dad-joke/internal/authctx"
)

func TestWithTenantToken(t *testing.T) {
	t.Parallel()
	ctx := authctx.With(context.Background(), "ten", "tok")
	tid, err := authctx.TenantID(ctx)
	if err != nil || tid != "ten" {
		t.Fatalf("tenant: %q %v", tid, err)
	}
	tok, err := authctx.TokenID(ctx)
	if err != nil || tok != "tok" {
		t.Fatalf("token: %q %v", tok, err)
	}
	if _, err := authctx.UserID(ctx); !errors.Is(err, authctx.ErrMissing) {
		t.Fatalf("user: %v", err)
	}
}

func TestWithUser(t *testing.T) {
	t.Parallel()
	ctx := authctx.WithUser(context.Background(), "ten", "user", "ses")
	tid, err := authctx.TenantID(ctx)
	if err != nil || tid != "ten" {
		t.Fatalf("tenant: %q %v", tid, err)
	}
	uid, err := authctx.UserID(ctx)
	if err != nil || uid != "user" {
		t.Fatalf("user: %q %v", uid, err)
	}
	sid, err := authctx.SessionID(ctx)
	if err != nil || sid != "ses" {
		t.Fatalf("session: %q %v", sid, err)
	}
	actor, err := authctx.TokenID(ctx)
	if err != nil || actor != "ses" {
		t.Fatalf("actor: %q %v", actor, err)
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()
	if _, err := authctx.TenantID(context.Background()); !errors.Is(err, authctx.ErrMissing) {
		t.Fatalf("got %v", err)
	}
}
