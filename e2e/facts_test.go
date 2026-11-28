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
	mu                sync.Mutex
	hits              []factsHit
	acked             map[string]bool
	acceptedUnique    map[string]int // first-accept counts only (no double count)
	listener          net.Listener
	srv               *http.Server
	url               string
	offline           bool
	offlineMu         sync.Mutex
	dropResponsesLeft int // when >0, consume body then close without writing ack
}

func startMockFactsAPI(t *testing.T) *mockFactsAPI {
	t.Helper()
	m := &mockFactsAPI{acked: make(map[string]bool), acceptedUnique: make(map[string]int)}
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

func (m *mockFactsAPI) setDropResponses(n int) {
	m.mu.Lock()
	m.dropResponsesLeft = n
	m.mu.Unlock()
}

func (m *mockFactsAPI) Hits() []factsHit {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]factsHit, len(m.hits))
	copy(out, m.hits)
	return out
}

func (m *mockFactsAPI) uniqueAccepted(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acceptedUnique[key]
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
		m.acceptedUnique[key]++
	}
	drop := m.dropResponsesLeft > 0
	if drop {
		m.dropResponsesLeft--
	}
	m.mu.Unlock()
	if drop {
		hj, ok := w.(http.Hijacker)
		if ok {
			if c, _, err := hj.Hijack(); err == nil {
				_ = c.Close()
				return
			}
		}
		// Fallback: close without a complete response body.
		return
	}
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

func factsQueueDir() string { return filepath.Join(dataDir, "facts") }

func waitFactsState(t *testing.T, timeout time.Duration) {
	t.Helper()
	path := filepath.Join(factsQueueDir(), "state.json")
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for facts state.json (uploader Open)")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func stopDadJokeService(t *testing.T) {
	t.Helper()
	mustPowershell(t, fmt.Sprintf(`Stop-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Stopped", 60*time.Second)
}

func startDadJokeService(t *testing.T) {
	t.Helper()
	mustPowershell(t, fmt.Sprintf(`Start-Service -Name '%s'`, serviceName))
	waitServiceState(t, "Running", 60*time.Second)
}

type onDiskFactsState struct {
	StreamID string `json:"stream_id"`
	NextSeq  int64  `json:"next_seq"`
}

// seedFactsBatches writes n PostFactsRequest batch files into the durable queue
// while the service is stopped (E2E-only; no production test hooks). Headless
// Session 0 has no interactive sessions, so production sampler correctly
// enqueues nothing — seeding drives uploader/ACL/stream tests deterministically.
func seedFactsBatches(t *testing.T, n int, restart bool) (streamID string, firstSeq, lastSeq int64) {
	t.Helper()
	if n < 1 {
		t.Fatal("seedFactsBatches: n >= 1")
	}
	waitFactsState(t, 30*time.Second)
	stopDadJokeService(t)
	if restart {
		defer startDadJokeService(t)
	}

	cfg := readConfigJSON(t)
	machineID, _ := cfg["machine_id"].(string)
	if machineID == "" {
		t.Fatal("missing machine_id")
	}
	iana, off := tzmap.Resolve(time.Now())

	statePath := filepath.Join(factsQueueDir(), "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st onDiskFactsState
	if err := json.Unmarshal(raw, &st); err != nil || st.StreamID == "" || st.NextSeq < 1 {
		t.Fatalf("bad state.json: %v %s", err, raw)
	}
	streamID = st.StreamID
	firstSeq = st.NextSeq
	seq := st.NextSeq
	for i := 0; i < n; i++ {
		body := map[string]any{
			"machine_id":         machineID,
			"stream_id":          streamID,
			"seq":                seq,
			"sent_at":            time.Now().UTC().Format(time.RFC3339Nano),
			"time_zone":          iana,
			"utc_offset_minutes": off,
			"facts": []any{
				map[string]any{
					"ts":     time.Now().UTC().Format(time.RFC3339Nano),
					"kind":   "session.state",
					"plugin": "e2e-seed",
					"dur_s":  15,
					"data": map[string]any{
						"session_id": "1",
						"username":   "e2e",
						"state":      "active",
					},
				},
			},
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(factsQueueDir(), fmt.Sprintf("%d.json", seq))
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		lastSeq = seq
		seq++
	}
	st.NextSeq = seq
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return streamID, firstSeq, lastSeq
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

	_, _, _ = seedFactsBatches(t, 1, true)
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
	if !strings.Contains(hit.Body, `"kind":"session.state"`) {
		t.Fatalf("seeded session.state missing: %s", hit.Body)
	}
	if strings.Contains(hit.Body, `"kind":"proc.sample"`) && !strings.Contains(hit.Body, `"exes":`) {
		t.Fatalf("proc.sample missing exes: %s", hit.Body)
	}
	assertStrictACL(t, factsQueueDir())
}

func TestTC20_FactsQueueACLDeniedForStandardUser(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)

	user, pass := createStandardUser(t, "djfacts")
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	pinNoSelfUpdate(t)
	if _, err := runAdmin(t, bin, "install", "--token", "e2e-facts-acl-"+randomSuffix(6),
		"--backend-url", mock.URL(), "--sample-interval", "15"); err != nil {
		t.Fatal(err)
	}
	waitServiceState(t, "Running", 60*time.Second)
	// Seed while stopped so a .json remains for read/delete probes (uploader would Ack).
	_, _, _ = seedFactsBatches(t, 1, false)

	factsDir := factsQueueDir()
	assertStrictACL(t, factsDir)
	probe := filepath.Join(factsDir, "acl-probe.txt")
	if err := attemptWriteFileAs(user, pass, probe); !isAccessDeniedErr(err) {
		t.Fatalf("standard user write: %v", err)
	}
	entries, _ := os.ReadDir(factsDir)
	foundJSON := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && e.Name() != "state.json" {
			foundJSON = true
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
	if !foundJSON {
		t.Fatal("expected seeded batch .json for ACL probe")
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
	stream1, seq1, _ := seedFactsBatches(t, 1, true)
	hits1 := waitForFacts(t, mock, 1, 45*time.Second)
	var meta1 struct {
		StreamID string `json:"stream_id"`
		Seq      int64  `json:"seq"`
	}
	if err := json.Unmarshal([]byte(hits1[0].Body), &meta1); err != nil || meta1.StreamID == "" {
		t.Fatalf("bad first batch: %v %s", err, hits1[0].Body)
	}
	if meta1.StreamID != stream1 || meta1.Seq != seq1 {
		t.Fatalf("uploaded meta=%+v seed stream=%s seq=%d", meta1, stream1, seq1)
	}

	mustPowershell(t, fmt.Sprintf(`Restart-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Running", 60*time.Second)
	stream2, seq2, _ := seedFactsBatches(t, 1, true)
	if stream2 != stream1 {
		t.Fatalf("stream_id changed across restart seed: %q → %q", stream1, stream2)
	}
	hits2 := waitForFacts(t, mock, len(hits1)+1, 45*time.Second)
	var meta2 struct {
		StreamID string `json:"stream_id"`
		Seq      int64  `json:"seq"`
	}
	_ = json.Unmarshal([]byte(hits2[len(hits2)-1].Body), &meta2)
	if meta2.StreamID != meta1.StreamID {
		t.Fatalf("stream_id changed: %q → %q", meta1.StreamID, meta2.StreamID)
	}
	if meta2.Seq != seq2 || meta2.Seq <= meta1.Seq {
		t.Fatalf("seq did not advance: %d → %d (seeded %d)", meta1.Seq, meta2.Seq, seq2)
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
	_, first, last := seedFactsBatches(t, 2, true)
	if n := len(mock.Hits()); n != 0 {
		t.Fatalf("expected no hits while offline, got %d", n)
	}
	mock.setOffline(false)
	hits := waitForFacts(t, mock, 2, 60*time.Second)
	var prev int64
	for i, h := range hits {
		var meta struct {
			Seq int64 `json:"seq"`
		}
		_ = json.Unmarshal([]byte(h.Body), &meta)
		if i == 0 && meta.Seq != first {
			t.Fatalf("first delivered seq=%d want %d", meta.Seq, first)
		}
		if i > 0 && meta.Seq <= prev {
			t.Fatalf("seq order broken: %d then %d", prev, meta.Seq)
		}
		prev = meta.Seq
	}
	if prev != last {
		t.Fatalf("last delivered seq=%d want %d", prev, last)
	}
}

func TestTC20_QueueCapConstant(t *testing.T) {
	if factsq.DefaultCapBytes != 20<<20 {
		t.Fatalf("DefaultCapBytes=%d", factsq.DefaultCapBytes)
	}
}

// TestTC20_FactsStopProcessSCMRecovery kills the agent process with Stop-Process
// -Force and asserts SCM recovery continues the same stream/seq without loss.
func TestTC20_FactsStopProcessSCMRecovery(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	pinNoSelfUpdate(t)
	token := "e2e-facts-kill-" + randomSuffix(6)
	if _, err := runAdmin(t, bin, "install", "--token", token, "--backend-url", mock.URL(), "--sample-interval", "15"); err != nil {
		t.Fatal(err)
	}
	waitServiceState(t, "Running", 60*time.Second)
	stream, first, last := seedFactsBatches(t, 3, true)
	_ = waitForFacts(t, mock, 1, 45*time.Second)

	mustPowershell(t, `Get-Process -Name 'dad-joke' -ErrorAction Stop | Stop-Process -Force`)
	waitServiceState(t, "Running", 90*time.Second) // SCM failure recovery restart
	hits := waitForFacts(t, mock, 3, 60*time.Second)
	seen := map[int64]bool{}
	for _, h := range hits {
		var m struct {
			StreamID string `json:"stream_id"`
			Seq      int64  `json:"seq"`
		}
		_ = json.Unmarshal([]byte(h.Body), &m)
		if m.StreamID != stream {
			continue
		}
		seen[m.Seq] = true
	}
	for s := first; s <= last; s++ {
		if !seen[s] {
			t.Fatalf("missing seq %d after SCM recovery (stream %s)", s, stream)
		}
	}
}

// TestTC20_FactsLostAckResendDuplicate drops the first HTTP response so the
// agent resends; the second response carries duplicate:true and unique-accept
// count stays 1 (no double count).
func TestTC20_FactsLostAckResendDuplicate(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockFactsAPI(t)
	pinNoSelfUpdate(t)
	token := "e2e-facts-lostack-" + randomSuffix(6)
	if _, err := runAdmin(t, bin, "install", "--token", token, "--backend-url", mock.URL(), "--sample-interval", "15"); err != nil {
		t.Fatal(err)
	}
	waitServiceState(t, "Running", 60*time.Second)
	mock.setDropResponses(1)
	stream, seq, _ := seedFactsBatches(t, 1, true)

	deadline := time.Now().Add(90 * time.Second)
	var firstKey string
	for {
		hits := mock.Hits()
		if len(hits) >= 2 {
			var m0, m1 struct {
				StreamID string `json:"stream_id"`
				Seq      int64  `json:"seq"`
			}
			_ = json.Unmarshal([]byte(hits[0].Body), &m0)
			_ = json.Unmarshal([]byte(hits[1].Body), &m1)
			if m0.StreamID == stream && m0.Seq == seq && m1.StreamID == stream && m1.Seq == seq {
				firstKey = fmt.Sprintf("%s:%d", m0.StreamID, m0.Seq)
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for lost-ack resend; hits=%d", len(mock.Hits()))
		}
		time.Sleep(200 * time.Millisecond)
	}
	if mock.uniqueAccepted(firstKey) != 1 {
		t.Fatalf("unique accept count for %s = %d, want 1 (no double count)", firstKey, mock.uniqueAccepted(firstKey))
	}
}
