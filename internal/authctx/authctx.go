// Package authctx stores authenticated tenant/token identity on context.Context.
package authctx

import (
	"context"
	"errors"
)

type ctxKey int

const (
	keyTenantID ctxKey = iota + 1
	keyTokenID
)

// ErrMissing is returned when tenant/token identity is absent from ctx.
var ErrMissing = errors.New("authctx: missing identity")

// With returns a child context carrying tenantID and tokenID.
func With(ctx context.Context, tenantID, tokenID string) context.Context {
	ctx = context.WithValue(ctx, keyTenantID, tenantID)
	ctx = context.WithValue(ctx, keyTokenID, tokenID)
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

// TokenID returns the authenticated token id, or ErrMissing.
func TokenID(ctx context.Context) (string, error) {
	v, _ := ctx.Value(keyTokenID).(string)
	if v == "" {
		return "", ErrMissing
	}
	return v, nil
}
