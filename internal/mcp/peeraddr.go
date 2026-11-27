package mcp

import "context"

type peerAddrKey struct{}

// WithPeerAddr stashes the MCP client's RemoteAddr for in-process ControlAPI
// RoundTrips. Used so AuthFailureLimiter keys by real peer IP, not an empty
// shared bucket. Do not derive this from X-Forwarded-For.
func WithPeerAddr(ctx context.Context, remoteAddr string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if remoteAddr == "" {
		return ctx
	}
	return context.WithValue(ctx, peerAddrKey{}, remoteAddr)
}

// PeerAddrFromContext returns the RemoteAddr previously stored with WithPeerAddr.
func PeerAddrFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	addr, _ := ctx.Value(peerAddrKey{}).(string)
	return addr
}
