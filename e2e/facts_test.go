//go:build e2e && windows

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/tzmap"
)

type factsHit struct {
	At   time.Time
	Auth string
	Body string
}

type mockFactsAPI struct {
	mu         sync.Mutex
	hits       []factsHit
	acked      map[string]bool
	listener   net.Listener
	srv        *http.Server
	url        string
	offline    bool
	offlineMu  sync.Mutex
}

func startMockFactsAPI(t *testing.T) *mockFactsAPI {
	t.Helper()
	m := &mockFactsAPI{acked: make(map[string]bool)}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		m.offlineMu.Lock()
		off := m.offline
		m.offlineMu.Unlock()
		if off {
			hj, ok := w.(http.Hijacker)
			if ok {
				if c, _, err := hj.Hijack(); err == nil {
					_ = c.Close()
					return
				}
			}
			http.Error(w, "offline", http.StatusBadGateway)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_time":"2026-11-28T12:00:00Z","desired_policy_version":0,"pending_job_ids":[]}`))
	})
	mux.HandleFunc("/v1/facts", m.handleFacts)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m.listener = ln
	m.url = "http://" + ln.Addr().String()
	m.srv = &http.Server{Handler: mux}
	go func() { _ = m.srv.Serve(ln) }()
	t.Cleanup(func() { _ = m.srv.Close() })
	return m
}

func (m *mockFactsAPI) URL() string { return m.url }

func (m *mockFactsAPI) setOffline(v bool) {
	m.offlineMu.Lock()
	m.offline = v
	m.offlineMu.Unlock()
}

func (m *mockFactsAPI) Hits() []factsHit {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]factsHit, len(m.hits))
	copy(out, m.hits)
	return out
}

func (m *mockFactsAPI) handleFacts(w http.ResponseWriter, r *http.Request) {
	m.offlineMu.Lock()
	off := m.offline
	m.offlineMu.Unlock()
	if off {
		hj, ok := w.(http.Hijacker)
		if ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
				return
			}
		}
		http.Error(w, "offline", http.StatusBadGateway)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	m.mu.Lock()
	m.hits = append(m.hits, factsHit{At: time.Now(), Auth: r.Header.Get("Authorization"), Body: string(body)})
	var meta struct {
		StreamID string `json:"stream_id"`
		Seq      int64  `json:"seq"`
	}
	_ = json.Unmarshal(body, &meta)
	key := fmt.Sprintf("%s:%d", meta.StreamID, meta.Seq)
	dup := m.acked[key]
	if !dup {
		m.acked[key] = true
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	payload, _ := json.Marshal(map[string]any{"accepted_seq": meta.Seq, "duplicate": dup})
	_, _ = w.Write(payload)
}

func waitForFacts(t *testing.T, mock *mockFactsAPI, min int, timeout time.Duration) []factsHit {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		hits := mock.Hits()
		if len(hits) >= min {
			return hits
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d facts batches; got %d", min, len(hits))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestTC20_FactsSampleToLocalAgentAPI(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	token := "e2e-facts-token-" + randomSuffix(8)
	pinNoSelfUpdate(t)
	out, err := runAdmin(t, bin,
		"install",
		"--token", token,
		"--backend-url", mock.URL(),
		"--heartbeat-interval", "30",
		"--sample-interval", "15",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)

	cfg := readConfigJSON(t)
	if int(cfg["sample_interval_seconds"].(float64)) != 15 {
		t.Fatalf("sample_interval_seconds = %v", cfg["sample_interval_seconds"])
	}

	hits := waitForFacts(t, mock, 1, 45*time.Second)
	hit := hits[0]
	if hit.Auth != "Bearer "+token {
		t.Fatalf("Authorization = %q", hit.Auth)
	}
	iana, off := tzmap.Resolve(time.Now())
	if !strings.Contains(hit.Body, `"time_zone":"`+iana+`"`) {
		t.Fatalf("time_zone want %q in %s", iana, hit.Body)
	}
	offJSON, _ := json.Marshal(off)
	if !strings.Contains(hit.Body, `"utc_offset_minutes":`+string(offJSON)) {
		t.Fatalf("utc_offset_minutes want %d in %s", off, hit.Body)
	}
	// Service runs in Session 0; proc.sample may be empty of interactive apps,
	// but when session.state/proc.sample are present they must be well-formed.
	if strings.Contains(hit.Body, `"kind":"proc.sample"`) && !strings.Contains(hit.Body, `"exes":`) {
		t.Fatalf("proc.sample missing exes: %s", hit.Body)
	}
	assertStrictACL(t, filepath.Join(dataDir, "facts"))
}

func TestTC20_FactsQueueACLDeniedForStandardUser(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)

	user := "djfacts" + randomSuffix(4)
	pass := "E2e!Facts-" + randomSuffix(6)
	createOut, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
net user %s %s /add
net localgroup Administrators %s /delete 2>&1 | Out-Null
Write-Output 'ok'
`, user, pass, user))
	if err != nil {
		t.Fatalf("create user: %v\n%s", err, createOut)
	}
	t.Cleanup(func() {
		_, _ = powershell(t, fmt.Sprintf(`$ErrorActionPreference='SilentlyContinue'; net user %s /delete`, user))
		cleanupInstall(t, bin)
	})

	mock := startMockFactsAPI(t)
	pinNoSelfUpdate(t)
	if _, err := runAdmin(t, bin, "install", "--token", "e2e-facts-acl-"+randomSuffix(6),
		"--backend-url", mock.URL(), "--sample-interval", "15"); err != nil {
		t.Fatal(err)
	}
	waitServiceState(t, "Running", 60*time.Second)
	_ = waitForFacts(t, mock, 1, 45*time.Second)

	factsDir := filepath.Join(dataDir, "facts")
	assertStrictACL(t, factsDir)
	probe := filepath.Join(factsDir, "acl-probe.txt")
	if err := attemptWriteFileAs(user, pass, probe); !isAccessDeniedErr(err) {
		t.Fatalf("standard user write: %v", err)
	}
	entries, _ := os.ReadDir(factsDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			p := filepath.Join(factsDir, e.Name())
			if err := attemptReadFileAs(user, pass, p); !isAccessDeniedErr(err) {
				t.Fatalf("standard user read %s: %v", e.Name(), err)
			}
			if err := attemptDeleteFileAs(user, pass, p); !isAccessDeniedErr(err) {
				t.Fatalf("standard user delete %s: %v", e.Name(), err)
			}
			break
		}
	}
}

func TestTC20_FactsRestartPreservesStream(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	pinNoSelfUpdate(t)
	token := "e2e-facts-restart-" + randomSuffix(6)
	if _, err := runAdmin(t, bin, "install", "--token", token, "--backend-url", mock.URL(), "--sample-interval", "15"); err != nil {
		t.Fatal(err)
	}
	waitServiceState(t, "Running", 60*time.Second)
	hits1 := waitForFacts(t, mock, 1, 45*time.Second)
	var meta1 struct {
		StreamID string `json:"stream_id"`
		Seq      int64  `json:"seq"`
	}
	if err := json.Unmarshal([]byte(hits1[0].Body), &meta1); err != nil || meta1.StreamID == "" {
		t.Fatalf("bad first batch: %v %s", err, hits1[0].Body)
	}

	mustPowershell(t, fmt.Sprintf(`Restart-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Running", 60*time.Second)
	hits2 := waitForFacts(t, mock, len(hits1)+1, 45*time.Second)
	var meta2 struct {
		StreamID string `json:"stream_id"`
		Seq      int64  `json:"seq"`
	}
	_ = json.Unmarshal([]byte(hits2[len(hits2)-1].Body), &meta2)
	if meta2.StreamID != meta1.StreamID {
		t.Fatalf("stream_id changed: %q → %q", meta1.StreamID, meta2.StreamID)
	}
	if meta2.Seq <= meta1.Seq {
		t.Fatalf("seq did not advance: %d → %d", meta1.Seq, meta2.Seq)
	}
}

func TestTC20_FactsOfflineThenDeliver(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	mock.setOffline(true)
	pinNoSelfUpdate(t)
	token := "e2e-facts-off-" + randomSuffix(6)
	if _, err := runAdmin(t, bin, "install", "--token", token, "--backend-url", mock.URL(), "--sample-interval", "15"); err != nil {
		t.Fatal(err)
	}
	waitServiceState(t, "Running", 60*time.Second)
	time.Sleep(18 * time.Second) // allow at least one sample while offline
	if n := len(mock.Hits()); n != 0 {
		t.Fatalf("expected no hits while offline, got %d", n)
	}
	mock.setOffline(false)
	hits := waitForFacts(t, mock, 1, 60*time.Second)
	var prev int64
	for i, h := range hits {
		var meta struct {
			Seq int64 `json:"seq"`
		}
		_ = json.Unmarshal([]byte(h.Body), &meta)
		if i > 0 && meta.Seq <= prev {
			t.Fatalf("seq order broken: %d then %d", prev, meta.Seq)
		}
		prev = meta.Seq
	}
}

func TestTC20_QueueCapConstant(t *testing.T) {
	if factsq.DefaultCapBytes != 20<<20 {
		t.Fatalf("DefaultCapBytes=%d", factsq.DefaultCapBytes)
	}
}
