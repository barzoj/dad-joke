package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestMiddleware_WrongPrefixDoesNotPoisonCorrectAPIHashBucket(t *testing.T) {
	t.Parallel()
	st := memory.New()
	res, err := bootstrap.Run(context.Background(), st, "mw-poison-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lim := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	authn := NewAuthenticator(AuthConfig{
		Store:        st,
		AuthFailures: lim,
		Clock:        clock,
	})

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	agentMW := authn.Middleware(store.TokenKindAgent)(okHandler)
	controlMW := authn.Middleware(store.TokenKindPAT)(okHandler)

	// Repeated dj_pat_ on AgentAPI from distinct IPs → 401 each time.
	// Distinct IPs keep the IP bucket under MaxHits so only a poisoned *hash*
	// bucket could cause ControlAPI 429 (the bug this regression catches).
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/heartbeat", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:40000", i+1)
		req.Header.Set("Authorization", "Bearer "+res.RawKey)
		rr := httptest.NewRecorder()
		agentMW.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("agent wrong-prefix iter %d: status=%d", i, rr.Code)
		}
	}

	// Same PAT on ControlAPI from a fresh IP must succeed, not 429.
	req := httptest.NewRequest(http.MethodGet, "/v1/api-keys", nil)
	req.RemoteAddr = "10.0.0.99:40000"
	req.Header.Set("Authorization", "Bearer "+res.RawKey)
	rr := httptest.NewRecorder()
	controlMW.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("control after agent wrong-prefix probes: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMiddleware_StoreError503NoRateLimitBurn(t *testing.T) {
	t.Parallel()
	base := memory.New()
	res, err := bootstrap.Run(context.Background(), base, "mw-storeerr-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lim := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 2, Clock: clock})

	failing := &mwTokenFailStore{Store: base, err: errors.New("mongo: connection timed out")}
	authn := NewAuthenticator(AuthConfig{
		Store:        failing,
		AuthFailures: lim,
		Clock:        clock,
	})
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := authn.Middleware(store.TokenKindPAT)(okHandler)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/v1/api-keys", nil)
		req.RemoteAddr = "10.0.0.9:40000"
		req.Header.Set("Authorization", "Bearer "+res.RawKey)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("iter %d: status=%d, want 503", i, rr.Code)
		}
	}

	failing.err = nil
	req := httptest.NewRequest(http.MethodGet, "/v1/api-keys", nil)
	req.RemoteAddr = "10.0.0.9:40000"
	req.Header.Set("Authorization", "Bearer "+res.RawKey)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("after recovery: status=%d (auth-failure budget must be intact)", rr.Code)
	}
}

type mwTokenFailStore struct {
	store.Store
	err error
}

func (s *mwTokenFailStore) Tokens() store.TokenStore {
	return mwTokenFailTokens{TokenStore: s.Store.Tokens(), parent: s}
}

type mwTokenFailTokens struct {
	store.TokenStore
	parent *mwTokenFailStore
}

func (t mwTokenFailTokens) GetByHash(ctx context.Context, kind store.TokenKind, hash string) (*store.Token, error) {
	if t.parent.err != nil {
		return nil, t.parent.err
	}
	return t.TokenStore.GetByHash(ctx, kind, hash)
}
