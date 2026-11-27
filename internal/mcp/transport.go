package mcp

import (
	"net/http"
	"net/http/httptest"
)

// HandlerRoundTripper routes generated ControlAPI client requests to an
// in-process http.Handler (no network hop). Used when MCP is mounted on the
// same process as ControlAPI.
type HandlerRoundTripper struct {
	Handler http.Handler
}

// RoundTrip implements http.RoundTripper.
func (t HandlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Handler == nil {
		return nil, &ToolError{Code: "internal_error", Message: "control handler missing"}
	}
	// Clone so we never mutate the caller's request; ensure a non-nil URL host
	// for handlers that inspect req.Host / req.URL.
	r := req.Clone(req.Context())
	if r.URL != nil && r.URL.Scheme == "" {
		r.URL.Scheme = "http"
	}
	if r.URL != nil && r.URL.Host == "" {
		r.URL.Host = "control.invalid"
	}
	if r.Host == "" {
		r.Host = "control.invalid"
	}
	// Propagate the original MCP peer address so ControlAPI auth rate limits
	// key by client IP. Never trust X-Forwarded-For on this in-process hop.
	if addr := PeerAddrFromContext(r.Context()); addr != "" {
		r.RemoteAddr = addr
	}
	rec := httptest.NewRecorder()
	t.Handler.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}
