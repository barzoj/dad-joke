package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	djmcp "github.com/barzoj/dad-joke/internal/mcp"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func newServerHandler(t *testing.T, cfg server.Config) http.Handler {
	t.Helper()
	if cfg.Store == nil {
		cfg.Store = memory.New()
	}
	secure := false
	if cfg.CookieSecure == nil {
		cfg.CookieSecure = &secure
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler()
}

func mcpJSONRPCBody() string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
}

func postMCPHandler(t *testing.T, h http.Handler, remoteAddr string, headers map[string]string, body string) (int, http.Header, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, rr.Header(), rr.Body.Bytes()
}

func TestMCP_MissingAuthRateLimitedByRemoteAddr(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	authFail := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	h := newServerHandler(t, server.Config{
		AuthFailureLimiter: authFail,
		Clock:              clock,
	})

	var saw429 bool
	var retryAfter string
	for i := 0; i < 6; i++ {
		code, hdr, body := postMCPHandler(t, h, "10.20.0.1:40000", nil, mcpJSONRPCBody())
		if code == http.StatusTooManyRequests {
			saw429 = true
			retryAfter = hdr.Get("Retry-After")
			assertNoSecret(t, string(body))
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("iter %d: status=%d body=%s", i, code, body)
		}
	}
	if !saw429 {
		t.Fatal("expected missing-auth POST /mcp to return 429 after IP budget exhausted")
	}
	if retryAfter == "" {
		t.Fatal("expected Retry-After on 429")
	}
}

func TestMCP_AuthFailureBucketsIsolatedByRemoteAddr(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	authFail := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	h := newServerHandler(t, server.Config{
		AuthFailureLimiter: authFail,
		Clock:              clock,
	})

	// Exhaust IP A's empty-auth bucket.
	for i := 0; i < 5; i++ {
		code, _, body := postMCPHandler(t, h, "10.20.0.10:40000", nil, mcpJSONRPCBody())
		if code != http.StatusUnauthorized && code != http.StatusTooManyRequests {
			t.Fatalf("IP A iter %d: status=%d body=%s", i, code, body)
		}
	}
	codeA, _, _ := postMCPHandler(t, h, "10.20.0.10:40000", nil, mcpJSONRPCBody())
	if codeA != http.StatusTooManyRequests {
		t.Fatalf("IP A should be limited, got %d", codeA)
	}

	// IP B must not share the empty IP bucket.
	codeB, _, bodyB := postMCPHandler(t, h, "10.20.0.20:40000", nil, mcpJSONRPCBody())
	if codeB != http.StatusUnauthorized {
		t.Fatalf("IP B status=%d body=%s (must not share IP A's bucket)", codeB, bodyB)
	}
}

func TestMCP_Probe429SurfacesAsHTTP429(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	authFail := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	h := newServerHandler(t, server.Config{
		AuthFailureLimiter: authFail,
		Clock:              clock,
	})

	bad := "dj_pat_" + strings.Repeat("1", 64)
	var saw429 bool
	var retryAfter string
	for i := 0; i < 6; i++ {
		code, hdr, body := postMCPHandler(t, h, "10.30.0.1:40000", map[string]string{
			"Authorization": "Bearer " + bad,
		}, mcpJSONRPCBody())
		if code == http.StatusTooManyRequests {
			saw429 = true
			retryAfter = hdr.Get("Retry-After")
			var er struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(body, &er)
			if er.Code != "rate_limited" {
				t.Fatalf("429 body code=%q body=%s", er.Code, body)
			}
			assertNoSecret(t, string(body), bad)
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("iter %d: status=%d body=%s (must not collapse 429 into other codes)", i, code, body)
		}
	}
	if !saw429 {
		t.Fatal("expected probe 429 to surface as HTTP 429")
	}
	if retryAfter == "" {
		t.Fatal("expected Retry-After forwarded from ControlAPI")
	}
}

func TestMCP_RevokedPATReturns401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	boot, err := bootstrap.Run(context.Background(), st, "mcp-revpat-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	h := newServerHandler(t, server.Config{Store: st})

	createReq := httptest.NewRequest(http.MethodPost, "/v1/api-keys", bytes.NewReader([]byte(`{"name":"temp"}`)))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", "Bearer "+boot.RawKey)
	createReq.RemoteAddr = "10.40.0.1:40000"
	createRR := httptest.NewRecorder()
	h.ServeHTTP(createRR, createReq)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create PAT: %d %s", createRR.Code, createRR.Body.String())
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(createRR.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, "dj_pat_") {
		t.Fatalf("token=%q", created.Token)
	}

	code, _, body := postMCPHandler(t, h, "10.40.0.1:40000", map[string]string{
		"Authorization": "Bearer " + created.Token,
	}, mcpJSONRPCBody())
	if code == http.StatusUnauthorized || code == http.StatusTooManyRequests {
		t.Fatalf("fresh PAT rejected: %d %s", code, body)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/v1/api-keys/"+created.ID, nil)
	delReq.Header.Set("Authorization", "Bearer "+boot.RawKey)
	delReq.RemoteAddr = "10.40.0.1:40000"
	delRR := httptest.NewRecorder()
	h.ServeHTTP(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("revoke PAT: %d %s", delRR.Code, delRR.Body.String())
	}

	code, _, body = postMCPHandler(t, h, "10.40.0.1:40000", map[string]string{
		"Authorization": "Bearer " + created.Token,
	}, mcpJSONRPCBody())
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked PAT status=%d body=%s", code, body)
	}
	assertNoSecret(t, string(body), created.Token, boot.RawKey)
}

func TestMCP_HandlerRoundTripperPropagatesPeerAddr(t *testing.T) {
	t.Parallel()
	var seen string
	control := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	})
	rt := djmcp.HandlerRoundTripper{Handler: control}

	req, err := http.NewRequestWithContext(
		djmcp.WithPeerAddr(context.Background(), "203.0.113.9:5555"),
		http.MethodGet,
		"http://control.invalid/v1/machines",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if seen != "203.0.113.9:5555" {
		t.Fatalf("RemoteAddr=%q want peer from context", seen)
	}

	seen = ""
	req2, err := http.NewRequestWithContext(
		djmcp.WithPeerAddr(context.Background(), "198.51.100.7:1"),
		http.MethodGet,
		"http://control.invalid/v1/machines",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("X-Forwarded-For", "1.2.3.4")
	resp2, err := rt.RoundTrip(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if seen != "198.51.100.7:1" {
		t.Fatalf("XFF must not win: RemoteAddr=%q", seen)
	}
}
