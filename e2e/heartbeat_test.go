//go:build e2e && windows

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type heartbeatHit struct {
	At        time.Time
	Auth      string
	Body      string
	UserAgent string
}

type mockAgentAPI struct {
	mu       sync.Mutex
	hits     []heartbeatHit
	listener net.Listener
	srv      *http.Server
	url      string
}

func startMockAgentAPI(t *testing.T) *mockAgentAPI {
	t.Helper()
	m := &mockAgentAPI{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/heartbeat", m.handleHeartbeat)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	m.listener = ln
	m.url = "http://" + ln.Addr().String()
	m.srv = &http.Server{Handler: mux}
	go func() { _ = m.srv.Serve(ln) }()
	t.Cleanup(func() { _ = m.srv.Close() })
	return m
}

func (m *mockAgentAPI) URL() string { return m.url }

func (m *mockAgentAPI) Hits() []heartbeatHit {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]heartbeatHit, len(m.hits))
	copy(out, m.hits)
	return out
}

func (m *mockAgentAPI) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	m.mu.Lock()
	m.hits = append(m.hits, heartbeatHit{
		At:        time.Now(),
		Auth:      r.Header.Get("Authorization"),
		Body:      string(body),
		UserAgent: r.Header.Get("User-Agent"),
	})
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"server_time":"2026-11-27T12:00:00Z","desired_policy_version":0,"pending_job_ids":[]}`))
}

func waitForHeartbeat(t *testing.T, mock *mockAgentAPI, min int, timeout time.Duration) []heartbeatHit {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		hits := mock.Hits()
		if len(hits) >= min {
			return hits
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d heartbeats; got %d", min, len(hits))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func readConfigJSON(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func expectVersion(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DAD_JOKE_EXPECT_VERSION")
	if v == "" {
		return "0.0.1-dev"
	}
	return v
}

// TestTC10_HeartbeatToLocalAgentAPI covers Issue #10 AC #4.
func TestTC10_HeartbeatToLocalAgentAPI(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockAgentAPI(t)
	token := "e2e-heartbeat-token-" + randomSuffix(8)
	wantVersion := expectVersion(t)

	pinNoSelfUpdate(t)
	out, err := runAdmin(t, bin,
		"install",
		"--token", token,
		"--backend-url", mock.URL(),
		"--heartbeat-interval", "2",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if strings.Contains(out, token) && !strings.Contains(out, "token=(set from --token)") {
		// install must not echo the supplied token value
		t.Fatalf("install stdout leaked token: %s", out)
	}
	waitServiceState(t, "Running", 60*time.Second)

	cfg := readConfigJSON(t)
	machineID, _ := cfg["machine_id"].(string)
	if machineID == "" {
		t.Fatal("missing machine_id in config")
	}
	if cfg["backend_url"] != mock.URL() {
		t.Fatalf("backend_url = %v", cfg["backend_url"])
	}
	if int(cfg["heartbeat_interval_seconds"].(float64)) != 2 {
		t.Fatalf("heartbeat_interval_seconds = %v", cfg["heartbeat_interval_seconds"])
	}

	hits := waitForHeartbeat(t, mock, 1, 20*time.Second)
	hit := hits[0]
	if hit.Auth != "Bearer "+token {
		t.Fatalf("Authorization = %q, want Bearer <token>", hit.Auth)
	}
	if !strings.Contains(hit.Body, `"machine_id":"`+machineID+`"`) {
		t.Fatalf("body missing machine_id: %s", hit.Body)
	}
	if !strings.Contains(hit.Body, `"agent_version":"`+wantVersion+`"`) {
		t.Fatalf("body missing version %s: %s", wantVersion, hit.Body)
	}
	if !strings.Contains(hit.Body, `"applied_policy_version":0`) {
		t.Fatalf("body missing applied_policy_version 0: %s", hit.Body)
	}

	// Reinstall without --backend-url must preserve backend settings + identity.
	out2, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("reinstall: %v\n%s", err, out2)
	}
	cfg2 := readConfigJSON(t)
	if cfg2["backend_url"] != mock.URL() {
		t.Fatalf("backend_url not preserved: %v", cfg2["backend_url"])
	}
	if cfg2["machine_id"] != machineID {
		t.Fatalf("machine_id rotated: %v", cfg2["machine_id"])
	}
	if cfg2["token"] != token {
		t.Fatal("token not preserved")
	}
	if int(cfg2["heartbeat_interval_seconds"].(float64)) != 2 {
		t.Fatalf("heartbeat_interval_seconds not preserved: %v", cfg2["heartbeat_interval_seconds"])
	}
}

// TestTC10_RejectNonHTTPSExternalBackend covers Issue #10 AC #2 install rejection.
func TestTC10_RejectNonHTTPSExternalBackend(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	out, err := runAdmin(t, bin, "install", "--backend-url", "http://example.com")
	if err == nil {
		t.Fatalf("expected install failure, got success:\n%s", out)
	}
	low := strings.ToLower(out)
	if !strings.Contains(low, "backend") && !strings.Contains(low, "loopback") && !strings.Contains(low, "http") {
		t.Fatalf("unexpected error output: %s", out)
	}
	// Service must not be left running after rejected install.
	state, _ := powershell(t, fmt.Sprintf(`(Get-Service -Name '%s' -ErrorAction SilentlyContinue).Status`, serviceName))
	if state == "Running" {
		t.Fatal("service unexpectedly Running after rejected install")
	}
}
