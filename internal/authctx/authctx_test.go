package authctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/barzoj/dad-joke/internal/authctx"
)

func TestWith_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := authctx.With(context.Background(), "ten-1", "tok-1")
	ten, err := authctx.TenantID(ctx)
	if err != nil || ten != "ten-1" {
		t.Fatalf("TenantID = %q %v", ten, err)
	}
	tok, err := authctx.TokenID(ctx)
	if err != nil || tok != "tok-1" {
		t.Fatalf("TokenID = %q %v", tok, err)
	}
}

func TestMissing(t *testing.T) {
	t.Parallel()
	if _, err := authctx.TenantID(context.Background()); !errors.Is(err, authctx.ErrMissing) {
		t.Fatalf("TenantID err = %v", err)
	}
	if _, err := authctx.TokenID(context.Background()); !errors.Is(err, authctx.ErrMissing) {
		t.Fatalf("TokenID err = %v", err)
	}
}
