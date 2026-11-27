package heartbeat

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
)

func TestCheckRedirectStripsAuthorizationOnHostChange(t *testing.T) {
	t.Parallel()
	orig, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/v1/heartbeat", nil)
	if err != nil {
		t.Fatal(err)
	}
	orig.Header.Set("Authorization", "Bearer secret-token")

	next, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:2/v1/heartbeat", nil)
	if err != nil {
		t.Fatal(err)
	}
	next.Header.Set("Authorization", "Bearer secret-token")

	if err := checkRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization not stripped on host change: %q", next.Header.Get("Authorization"))
	}
}

func TestCheckRedirectKeepsAuthorizationSameHost(t *testing.T) {
	t.Parallel()
	orig, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/a", nil)
	orig.Header.Set("Authorization", "Bearer secret-token")
	next, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/b", nil)
	next.Header.Set("Authorization", "Bearer secret-token")
	if err := checkRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", next.Header.Get("Authorization"))
	}
}

func TestCheckRedirectStripsAuthorizationOnSchemeChange(t *testing.T) {
	t.Parallel()
	orig, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/v1/heartbeat", nil)
	if err != nil {
		t.Fatal(err)
	}
	orig.Header.Set("Authorization", "Bearer secret-token")

	next, err := http.NewRequest(http.MethodPost, "https://127.0.0.1:1/v1/heartbeat", nil)
	if err != nil {
		t.Fatal(err)
	}
	next.Header.Set("Authorization", "Bearer secret-token")

	if err := checkRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization not stripped on scheme change: %q", next.Header.Get("Authorization"))
	}
}

func TestCheckRedirectRejectsNonLoopbackHTTP(t *testing.T) {
	t.Parallel()
	next, _ := http.NewRequest(http.MethodGet, "http://example.com/x", nil)
	if err := checkRedirect(next, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewHTTPClientTLSMinVersion(t *testing.T) {
	t.Parallel()
	c := newHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil {
		t.Fatal("missing TLS config")
	}
	if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d", tr.TLSClientConfig.MinVersion)
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must be false")
	}
}

func TestGeneratedClientHeartbeatRoundTrip(t *testing.T) {
	t.Parallel()
	var gotAuth atomic.Value
	var gotVersion atomic.Value
	var gotMachine atomic.Value

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		_ = r.Body.Close()
		raw := string(body)
		gotVersion.Store(raw)
		gotMachine.Store(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_time":"2026-11-27T12:00:00Z","desired_policy_version":0,"pending_job_ids":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := testCfg(srv.URL, 60)
	logs := &logBuf{}
	httpClient := srv.Client()
	httpClient.CheckRedirect = checkRedirect

	loop := &Loop{
		Version:    "9.9.9-e2e",
		Config:     &cfg,
		Log:        logs,
		Clock:      RealClock{},
		Host:       fakeHost{},
		HTTPClient: httpClient,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for gotAuth.Load() == nil {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no request")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	if auth, _ := gotAuth.Load().(string); auth != "Bearer "+cfg.Token {
		t.Fatalf("Authorization = %q", auth)
	}
	body, _ := gotVersion.Load().(string)
	if !strings.Contains(body, `"agent_version":"9.9.9-e2e"`) {
		t.Fatalf("body missing version: %s", body)
	}
	if !strings.Contains(body, cfg.MachineID) {
		t.Fatalf("body missing machine_id: %s", body)
	}
	if strings.Contains(logs.joined(), cfg.Token) {
		t.Fatalf("token leaked: %s", logs.joined())
	}
}

func TestDefaultNewClientRejectsExternalHTTP(t *testing.T) {
	t.Parallel()
	loop := &Loop{}
	_, err := loop.defaultNewClient("http://example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "backend_url") {
		t.Fatalf("err = %v", err)
	}
	_ = config.ErrInvalid
}

// Ensure agentapi types stay imported when fake paths change.
var _ = agentapi.PostHeartbeatJSONRequestBody{}
