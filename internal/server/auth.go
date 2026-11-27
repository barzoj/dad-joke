package server

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
)

// AuthConfig configures bearer auth + rate limits.
type AuthConfig struct {
	Store        store.Store
	AuthFailures *ratelimit.Limiter // per-IP (and per-kind+hash) auth failures
	Mutations    *ratelimit.Limiter // per-token mutating requests
	Clock        func() time.Time
	SkipPaths    map[string]struct{} // paths that skip auth (e.g. /healthz)
}

// Authenticator verifies dj_agt_ / dj_pat_ bearer credentials.
type Authenticator struct {
	cfg AuthConfig
}

// NewAuthenticator builds an Authenticator. Limiters may be nil (no rate limiting).
func NewAuthenticator(cfg AuthConfig) *Authenticator {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.SkipPaths == nil {
		cfg.SkipPaths = map[string]struct{}{"/healthz": {}}
	}
	return &Authenticator{cfg: cfg}
}

// Middleware returns HTTP middleware that accepts only Bearer tokens of wantKind.
func (a *Authenticator) Middleware(wantKind store.TokenKind) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, skip := a.cfg.SkipPaths[r.URL.Path]; skip {
				next.ServeHTTP(w, r)
				return
			}

			reqID := requestID(r)
			ip := clientIP(r)
			failKey := "authfail:ip:" + ip

			if a.cfg.AuthFailures != nil {
				if retry, exceeded := a.cfg.AuthFailures.Exceeded(failKey); exceeded {
					writeTooManyRequests(w, reqID, retry)
					return
				}
			}

			raw, ok := bearerToken(r)
			if !ok {
				a.recordAuthFailure(failKey)
				if a.authFailureLimited(w, reqID, failKey) {
					return
				}
				writeUnauthorized(w, reqID)
				return
			}

			kind := tokenhash.KindOf(raw)
			hash := tokenhash.Hash(raw)
			// Namespace hash buckets by wantKind so a wrong-prefix probe on one API
			// cannot poison the other API's hash rate-limit bucket.
			hashFailKey := "authfail:hash:" + string(wantKind) + ":" + hash

			if kind != wantKind {
				// Wrong prefix: charge IP only. Do not touch the hash bucket —
				// that credential is legitimate on the other API surface.
				a.recordAuthFailure(failKey)
				if a.authFailureLimited(w, reqID, failKey) {
					return
				}
				writeUnauthorized(w, reqID)
				return
			}

			if a.cfg.AuthFailures != nil {
				if retry, exceeded := a.cfg.AuthFailures.Exceeded(hashFailKey); exceeded {
					writeTooManyRequests(w, reqID, retry)
					return
				}
			}

			tok, err := a.cfg.Store.Tokens().GetByHash(r.Context(), wantKind, hash)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					a.recordAuthFailure(failKey, hashFailKey)
					if a.authFailureLimited(w, reqID, failKey) || a.authFailureLimited(w, reqID, hashFailKey) {
						return
					}
					writeUnauthorized(w, reqID)
					return
				}
				// Store/infra blip: fail closed without burning auth-failure budget.
				writeServiceUnavailable(w, reqID)
				return
			}
			if tok == nil || !tokenhash.EqualHash(tok.Hash, raw) || !tokenUsable(tok, a.cfg.Clock().UTC()) {
				a.recordAuthFailure(failKey, hashFailKey)
				if a.authFailureLimited(w, reqID, failKey) || a.authFailureLimited(w, reqID, hashFailKey) {
					return
				}
				writeUnauthorized(w, reqID)
				return
			}

			if isMutating(r.Method) && a.cfg.Mutations != nil {
				mutKey := "mut:token:" + tok.ID
				if retry, ok := a.cfg.Mutations.Allow(mutKey); !ok {
					writeTooManyRequests(w, reqID, retry)
					return
				}
			}

			_ = a.cfg.Store.Tokens().TouchLastUsed(r.Context(), tok.TenantID, tok.ID, a.cfg.Clock().UTC())

			ctx := authctx.With(r.Context(), tok.TenantID, tok.ID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (a *Authenticator) recordAuthFailure(keys ...string) {
	if a.cfg.AuthFailures == nil {
		return
	}
	for _, k := range keys {
		if k != "" {
			a.cfg.AuthFailures.Record(k)
		}
	}
}

func (a *Authenticator) authFailureLimited(w http.ResponseWriter, reqID, key string) bool {
	if a.cfg.AuthFailures == nil || key == "" {
		return false
	}
	if retry, exceeded := a.cfg.AuthFailures.Exceeded(key); exceeded {
		writeTooManyRequests(w, reqID, retry)
		return true
	}
	return false
}

func tokenUsable(tok *store.Token, now time.Time) bool {
	if tok.RevokedAt != nil {
		return false
	}
	if tok.ExpiresAt != nil && !tok.ExpiresAt.After(now) {
		return false
	}
	return true
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return "", false
	}
	return tok, true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
