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
)

type jobResultHit struct {
	At     time.Time
	JobID  string
	Auth   string
	Body   string
	Status string
}

type mockJobsAPI struct {
	mu sync.Mutex

	token           string
	machineID       string
	pendingIDs      []string
	jobs            map[string]mockJob
	claimCalls      map[string]int
	results         []jobResultHit
	events          []string
	heartbeats      int
	conflictOnClaim map[string]bool

	listener net.Listener
	srv      *http.Server
	url      string
}

type mockJob struct {
	Type           string
	Payload        string
	TimeoutSeconds int32
}

func startMockJobsAPI(t *testing.T, token string) *mockJobsAPI {
	t.Helper()
	m := &mockJobsAPI{
		token:           token,
		jobs:            make(map[string]mockJob),
		claimCalls:      make(map[string]int),
		conflictOnClaim: make(map[string]bool),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/heartbeat", m.handleHeartbeat)
	mux.HandleFunc("/v1/events", m.handleEvents)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// /v1/jobs/{id}/claim and /result
	mux.HandleFunc("/v1/jobs/", m.handleJobs)

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

func (m *mockJobsAPI) URL() string { return m.url }

func (m *mockJobsAPI) SetPending(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingIDs = append([]string(nil), ids...)
}

func (m *mockJobsAPI) AddJob(id string, j mockJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[id] = j
}

func (m *mockJobsAPI) MarkConflict(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conflictOnClaim[id] = true
}

func (m *mockJobsAPI) Results() []jobResultHit {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]jobResultHit, len(m.results))
	copy(out, m.results)
	return out
}

func (m *mockJobsAPI) Heartbeats() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.heartbeats
}

func (m *mockJobsAPI) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	var req struct {
		MachineID string `json:"machine_id"`
	}
	_ = json.Unmarshal(body, &req)

	m.mu.Lock()
	m.heartbeats++
	if m.machineID == "" && req.MachineID != "" {
		m.machineID = req.MachineID
	}
	pending := append([]string(nil), m.pendingIDs...)
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"server_time":            time.Now().UTC().Format(time.RFC3339Nano),
		"desired_policy_version": 0,
		"pending_job_ids":        pending,
	})
}

func (m *mockJobsAPI) handleEvents(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	m.mu.Lock()
	m.events = append(m.events, string(body))
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (m *mockJobsAPI) handleJobs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	jobID, action := parts[0], parts[1]
	switch {
	case action == "claim" && r.Method == http.MethodPost:
		m.handleClaim(w, r, jobID)
	case action == "result" && r.Method == http.MethodPost:
		m.handleResult(w, r, jobID)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockJobsAPI) handleClaim(w http.ResponseWriter, r *http.Request, jobID string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	var req struct {
		MachineID string `json:"machine_id"`
	}
	_ = json.Unmarshal(body, &req)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimCalls[jobID]++
	if m.conflictOnClaim[jobID] {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict","message":"job not claimable","request_id":"e2e"}`))
		return
	}
	job, ok := m.jobs[jobID]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"job not found","request_id":"e2e"}`))
		return
	}
	// Remove from pending once claimed.
	filtered := m.pendingIDs[:0]
	for _, id := range m.pendingIDs {
		if id != jobID {
			filtered = append(filtered, id)
		}
	}
	m.pendingIDs = filtered

	now := time.Now().UTC()
	timeout := job.TimeoutSeconds
	var timeoutPtr any
	if timeout > 0 {
		timeoutPtr = timeout
	} else {
		timeoutPtr = nil
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":              jobID,
		"machine_id":      req.MachineID,
		"status":          "running",
		"type":            job.Type,
		"payload":         job.Payload,
		"timeout_seconds": timeoutPtr,
		"exit_code":       nil,
		"stdout":          "",
		"stderr":          "",
		"duration_ms":     nil,
		"created_at":      now.Add(-time.Minute).Format(time.RFC3339Nano),
		"expires_at":      now.Add(time.Hour).Format(time.RFC3339Nano),
		"claimed_at":      now.Format(time.RFC3339Nano),
		"finished_at":     nil,
	})
}

func (m *mockJobsAPI) handleResult(w http.ResponseWriter, r *http.Request, jobID string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = r.Body.Close()
	var req struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &req)
	m.mu.Lock()
	m.results = append(m.results, jobResultHit{
		At:     time.Now(),
		JobID:  jobID,
		Auth:   r.Header.Get("Authorization"),
		Body:   string(body),
		Status: req.Status,
	})
	m.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func waitForJobResult(t *testing.T, mock *mockJobsAPI, jobID string, timeout time.Duration) jobResultHit {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, r := range mock.Results() {
			if r.JobID == jobID {
				return r
			}
		}
		if time.Now().After(deadline) {
			mock.mu.Lock()
			claims := mock.claimCalls[jobID]
			pending := append([]string(nil), mock.pendingIDs...)
			results := append([]jobResultHit(nil), mock.results...)
			mock.mu.Unlock()
			t.Fatalf("timed out waiting for result of %s; claims=%d pending=%v got %#v", jobID, claims, pending, results)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func installWithJobsBackend(t *testing.T, bin, token, backend string) {
	t.Helper()
	pinNoSelfUpdate(t)
	out, err := runAdmin(t, bin,
		"install",
		"--token", token,
		"--backend-url", backend,
		"--heartbeat-interval", "2",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)
}

// TestTC14_ClaimExecuteResult covers AC #1: pending job claimed, executed, stdout+exit_code arrive.
func TestTC14_ClaimExecuteResult(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-token-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	jobID := "11111111-1111-4111-8111-111111111111"
	mock.AddJob(jobID, mockJob{Type: "cmd", Payload: "echo hello-from-job"})
	mock.SetPending(jobID)

	installWithJobsBackend(t, bin, token, mock.URL())

	res := waitForJobResult(t, mock, jobID, 45*time.Second)
	if res.Status != "succeeded" {
		t.Fatalf("status = %s body=%s", res.Status, res.Body)
	}
	if !strings.Contains(res.Body, "hello-from-job") {
		t.Fatalf("stdout missing: %s", res.Body)
	}
	if !strings.Contains(res.Body, `"exit_code":0`) && !strings.Contains(res.Body, `"exit_code": 0`) {
		t.Fatalf("exit_code missing/nonzero: %s", res.Body)
	}
	if res.Auth != "Bearer "+token {
		t.Fatalf("auth = %q", res.Auth)
	}
}

// TestTC14_TimeoutKillsChildProcess covers AC #2.
func TestTC14_TimeoutKillsChildProcess(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-timeout-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	jobID := "22222222-2222-4222-8222-222222222222"
	pidFile := filepath.Join(dataDir, "e2e-child.pid")
	_ = os.Remove(pidFile)

	// Spawn a long-lived child, write its PID, then hang in the parent script.
	payload := fmt.Sprintf(
		`$p = Start-Process -FilePath 'powershell.exe' -ArgumentList '-NoProfile','-NonInteractive','-Command','while($true){Start-Sleep -Seconds 1}' -PassThru -WindowStyle Hidden; Set-Content -LiteralPath '%s' -Value $p.Id; while($true){Start-Sleep -Seconds 1}`,
		pidFile,
	)
	mock.AddJob(jobID, mockJob{Type: "powershell", Payload: payload, TimeoutSeconds: 5})
	mock.SetPending(jobID)

	installWithJobsBackend(t, bin, token, mock.URL())
	res := waitForJobResult(t, mock, jobID, 60*time.Second)
	if res.Status != "timed_out" {
		t.Fatalf("status = %s body=%s", res.Status, res.Body)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		raw, err := os.ReadFile(pidFile)
		if err == nil {
			pid := strings.TrimSpace(string(raw))
			alive, _ := powershell(t, fmt.Sprintf(`(Get-Process -Id %s -ErrorAction SilentlyContinue) -ne $null`, pid))
			if strings.EqualFold(alive, "False") || alive == "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("child pid %s still alive after job object timeout", pid)
			}
		} else if time.Now().After(deadline) {
			t.Fatalf("pid file never written / child check failed: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestTC14_HugeOutputTruncated covers AC #3.
func TestTC14_HugeOutputTruncated(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-huge-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	jobID := "33333333-3333-4333-8333-333333333333"
	// Write >64KiB of 'A' to stdout.
	payload := `$s = 'A' * 70000; Write-Output $s`
	mock.AddJob(jobID, mockJob{Type: "powershell", Payload: payload, TimeoutSeconds: 30})
	mock.SetPending(jobID)

	installWithJobsBackend(t, bin, token, mock.URL())
	res := waitForJobResult(t, mock, jobID, 60*time.Second)
	if res.Status != "succeeded" && res.Status != "failed" {
		// Truncation must not prevent result POST; accept succeeded (exit 0).
		t.Fatalf("status = %s body len=%d", res.Status, len(res.Body))
	}
	var parsed struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(res.Body), &parsed); err != nil {
		t.Fatalf("parse result: %v body=%s", err, res.Body)
	}
	if len(parsed.Stdout) > 65536 {
		t.Fatalf("stdout len %d > 64KiB", len(parsed.Stdout))
	}
	if len(parsed.Stdout) < 60000 {
		t.Fatalf("stdout unexpectedly short: %d", len(parsed.Stdout))
	}
}

// TestTC14_HeartbeatAndUpdaterDuringJob covers AC #4 and #11.
func TestTC14_HeartbeatAndUpdaterDuringJob(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-iso-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	release := startMockReleaseServer(t)
	exeBytes, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	// Older tag than the agent so polls happen without an apply/restart mid-job.
	release.SetRelease("v0.0.0", exeBytes, nil)

	jobID := "44444444-4444-4444-8444-444444444444"
	mock.AddJob(jobID, mockJob{
		Type:           "powershell",
		Payload:        `Start-Sleep -Seconds 12; Write-Output 'done'`,
		TimeoutSeconds: 60,
	})

	// Install first, then configure the updater and restart *before* enqueueing.
	// Restarting after claim would kill the suspended/running job and clear
	// pending_job_ids on the mock, so no result would ever be posted.
	installWithJobsBackend(t, bin, token, mock.URL())
	writeUpdateConfig(t, release.LatestURL(), 2)
	mustPowershell(t, fmt.Sprintf(`Restart-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Running", 60*time.Second)

	mock.SetPending(jobID)

	hbBefore := mock.Heartbeats()
	pollsBefore := len(release.Polls())
	_ = waitForJobResult(t, mock, jobID, 90*time.Second)

	if mock.Heartbeats() <= hbBefore {
		t.Fatalf("heartbeats did not continue during job: before=%d after=%d", hbBefore, mock.Heartbeats())
	}
	if len(release.Polls()) <= pollsBefore {
		t.Fatalf("updater did not poll during job: before=%d after=%d", pollsBefore, len(release.Polls()))
	}
}

// TestTC14_ClaimConflictSkipsExecution covers AC #6.
func TestTC14_ClaimConflictSkipsExecution(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-409-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	jobID := "55555555-5555-4555-8555-555555555555"
	mock.AddJob(jobID, mockJob{Type: "cmd", Payload: `echo should-not-run`})
	mock.MarkConflict(jobID)
	mock.SetPending(jobID)

	installWithJobsBackend(t, bin, token, mock.URL())

	// Wait for at least one claim attempt, ensure no result posted.
	deadline := time.Now().Add(30 * time.Second)
	for {
		mock.mu.Lock()
		calls := mock.claimCalls[jobID]
		results := len(mock.results)
		mock.mu.Unlock()
		if calls >= 1 {
			if results != 0 {
				t.Fatalf("result posted despite 409: %#v", mock.Results())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("claim never attempted")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestTC14_AuditFileStrictACL covers AC #8.
func TestTC14_AuditFileStrictACL(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-audit-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	jobID := "66666666-6666-4666-8666-666666666666"
	mock.AddJob(jobID, mockJob{Type: "cmd", Payload: "echo audit-me"})
	mock.SetPending(jobID)

	installWithJobsBackend(t, bin, token, mock.URL())
	_ = waitForJobResult(t, mock, jobID, 45*time.Second)

	auditPath := filepath.Join(dataDir, "jobs-audit.jsonl")
	deadline := time.Now().Add(15 * time.Second)
	var raw []byte
	var err error
	for {
		raw, err = os.ReadFile(auditPath)
		if err == nil && len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit file missing: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if strings.Contains(string(raw), token) {
		t.Fatal("raw agent token leaked into audit log")
	}
	assertStrictACL(t, auditPath)
	assertStrictACL(t, dataDir)
}

// TestTC14_SequentialJobs covers AC #9 (job2 waits for job1).
func TestTC14_SequentialJobs(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	token := "e2e-jobs-seq-" + randomSuffix(8)
	mock := startMockJobsAPI(t, token)
	id1 := "77777777-7777-4777-8777-777777777777"
	id2 := "88888888-8888-4888-8888-888888888888"
	mock.AddJob(id1, mockJob{
		Type:           "powershell",
		Payload:        `Start-Sleep -Seconds 4; Write-Output 'one'`,
		TimeoutSeconds: 30,
	})
	mock.AddJob(id2, mockJob{Type: "cmd", Payload: "echo two", TimeoutSeconds: 30})
	mock.SetPending(id1, id2)

	installWithJobsBackend(t, bin, token, mock.URL())
	r1 := waitForJobResult(t, mock, id1, 60*time.Second)
	r2 := waitForJobResult(t, mock, id2, 60*time.Second)
	if r2.At.Before(r1.At) {
		t.Fatalf("job2 finished before job1: r1=%v r2=%v", r1.At, r2.At)
	}
}
