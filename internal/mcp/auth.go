package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

const sessionCookieName = "dj_session"

// extractCredential returns the raw bearer/cookie secret from an MCP HTTP request.
// fromCookie is true only when the value came from the dj_session cookie.
func extractCredential(r *http.Request) (raw string, fromCookie bool) {
	if r == nil {
		return "", false
	}
	h := r.Header.Get("Authorization")
	if len(h) >= 7 && strings.EqualFold(h[:7], "bearer ") {
		tok := strings.TrimSpace(h[7:])
		if tok != "" {
			return tok, false
		}
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c == nil || strings.TrimSpace(c.Value) == "" {
		return "", false
	}
	return c.Value, true
}

// forwardAuth returns a RequestEditorFn that attaches the caller's credentials
// to outbound ControlAPI requests without logging the secret.
func forwardAuth(raw string, fromCookie bool) controlapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if req == nil || raw == "" {
			return nil
		}
		if fromCookie {
			req.Header.Set("Cookie", sessionCookieName+"="+raw)
			req.Header.Del("Authorization")
			return nil
		}
		req.Header.Set("Authorization", "Bearer "+raw)
		return nil
	}
}

// headersAuth extracts credentials from an MCP tool request's Extra.Header.
func headersAuth(h http.Header) (raw string, fromCookie bool) {
	if h == nil {
		return "", false
	}
	auth := h.Get("Authorization")
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		tok := strings.TrimSpace(auth[7:])
		if tok != "" {
			return tok, false
		}
	}
	// Cookie header may contain dj_session=...
	cookieHdr := h.Get("Cookie")
	if cookieHdr == "" {
		return "", false
	}
	parts := strings.Split(cookieHdr, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, sessionCookieName+"=") {
			val := strings.TrimPrefix(p, sessionCookieName+"=")
			if val != "" {
				return val, true
			}
		}
	}
	return "", false
}
