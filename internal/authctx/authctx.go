// Package authctx stores authenticated tenant/token/user identity on context.Context.
package authctx

import (
	"context"
	"errors"
)

type ctxKey int

const (
	keyTenantID ctxKey = iota + 1
	keyTokenID
	keyUserID
	keySessionID
)

// ErrMissing is returned when tenant/token identity is absent from ctx.
var ErrMissing = errors.New("authctx: missing identity")

// With returns a child context carrying tenantID and tokenID (PAT/agent actor).
func With(ctx context.Context, tenantID, tokenID string) context.Context {
	ctx = context.WithValue(ctx, keyTenantID, tenantID)
	ctx = context.WithValue(ctx, keyTokenID, tokenID)
	return ctx
}

// WithUser returns a child context for an authenticated user session.
// actorID for audits is the session id; UserID is also available.
func WithUser(ctx context.Context, tenantID, userID, sessionID string) context.Context {
	ctx = context.WithValue(ctx, keyTenantID, tenantID)
	ctx = context.WithValue(ctx, keyTokenID, sessionID) // actor id for existing audit callers
	ctx = context.WithValue(ctx, keyUserID, userID)
	ctx = context.WithValue(ctx, keySessionID, sessionID)
	return ctx
}

// TenantID returns the authenticated tenant id, or ErrMissing.
func TenantID(ctx context.Context) (string, error) {
	v, _ := ctx.Value(keyTenantID).(string)
	if v == "" {
		return "", ErrMissing
	}
	return v, nil
}

// TokenID returns the authenticated token/session actor id, or ErrMissing.
func TokenID(ctx context.Context) (string, error) {
	v, _ := ctx.Value(keyTokenID).(string)
	if v == "" {
		return "", ErrMissing
	}
	return v, nil
}

// UserID returns the authenticated user id when logged in via session, or ErrMissing.
func UserID(ctx context.Context) (string, error) {
	v, _ := ctx.Value(keyUserID).(string)
	if v == "" {
		return "", ErrMissing
	}
	return v, nil
}

// SessionID returns the authenticated session id when logged in via session, or ErrMissing.
func SessionID(ctx context.Context) (string, error) {
	v, _ := ctx.Value(keySessionID).(string)
	if v == "" {
		return "", ErrMissing
	}
	return v, nil
}
