package jobs

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type fakeAPI struct {
	mu sync.Mutex

	claimStatus  int
	claimJob     *agentapi.Job
	claimCalls   atomic.Int32
	resultCalls  atomic.Int32
	eventCalls   atomic.Int32
	lastResult   *agentapi.PostJobResultJSONRequestBody
	lastEvent    *agentapi.PostEventsJSONRequestBody
	resultStatus int
	executed     chan struct{}

	// claimDelay blocks claim to prove heartbeat isolation in other tests.
	claimDelay time.Duration
}

func (f *fakeAPI) ClaimJobWithResponse(ctx context.Context, id openapi_types.UUID, body agentapi.ClaimJobJSONRequestBody, _ ...agentapi.RequestEditorFn) (*agentapi.ClaimJobResponse, error) {
	f.claimCalls.Add(1)
	if f.claimDelay > 0 {
		select {
		case <-time.After(f.claimDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	status := f.claimStatus
	if status == 0 {
		status = http.StatusOK
	}
	out := &agentapi.ClaimJobResponse{HTTPResponse: &http.Response{StatusCode: status}}
	if status == http.StatusOK {
		job := f.claimJob
		if job == nil {
			now := time.Now().UTC()
			job = &agentapi.Job{
				Id:        id,
				MachineId: body.MachineId,
				Status:    agentapi.DadJokeCommonJobStatusRunning,
				Type:      agentapi.Cmd,
				Payload:   "echo hi",
				CreatedAt: now,
				ExpiresAt: now.Add(time.Hour),
				ClaimedAt: &now,
			}
		}
		cp := *job
		cp.Id = id
		out.JSON200 = &cp
	}
	return out, nil
}

func (f *fakeAPI) PostJobResultWithResponse(_ context.Context, _ openapi_types.UUID, body agentapi.PostJobResultJSONRequestBody, _ ...agentapi.RequestEditorFn) (*agentapi.PostJobResultResponse, error) {
	f.resultCalls.Add(1)
	f.mu.Lock()
	cp := body
	f.lastResult = &cp
	f.mu.Unlock()
	status := f.resultStatus
	if status == 0 {
		status = http.StatusNoContent
	}
	if f.executed != nil {
		select {
		case f.executed <- struct{}{}:
		default:
		}
	}
	return &agentapi.PostJobResultResponse{HTTPResponse: &http.Response{StatusCode: status}}, nil
}

func (f *fakeAPI) PostEventsWithResponse(_ context.Context, body agentapi.PostEventsJSONRequestBody, _ ...agentapi.RequestEditorFn) (*agentapi.PostEventsResponse, error) {
	f.eventCalls.Add(1)
	f.mu.Lock()
	cp := body
	f.lastEvent = &cp
	f.mu.Unlock()
	return &agentapi.PostEventsResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNoContent}}, nil
}

type fakeExec struct {
	mu       sync.Mutex
	calls    []string
	delay    time.Duration
	result   ExecResult
	err      error
	running  atomic.Int32
	started  chan struct{}
	block    chan struct{} // if set, Run waits until closed or ctx done
}

func (e *fakeExec) Run(ctx context.Context, jobType, payload string, _ time.Duration) (ExecResult, error) {
	e.running.Add(1)
	defer e.running.Add(-1)
	e.mu.Lock()
	e.calls = append(e.calls, jobType+":"+payload)
	e.mu.Unlock()
	if e.started != nil {
		select {
		case e.started <- struct{}{}:
		default:
		}
	}
	if e.block != nil {
		select {
		case <-e.block:
		case <-ctx.Done():
			return ExecResult{TimedOut: true, ExitCode: -1, DurationMS: 1}, nil
		}
	}
	if e.delay > 0 {
		select {
		case <-time.After(e.delay):
		case <-ctx.Done():
			return ExecResult{TimedOut: true, ExitCode: -1, DurationMS: e.delay.Milliseconds()}, nil
		}
	}
	return e.result, e.err
}

func testCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.WithTestIdentity(config.Default())
	cfg.BackendURL = "http://127.0.0.1:9"
	cfg.Token = "dj_agt_" + strings.Repeat("a", 64)
	cfg.HeartbeatIntervalSeconds = 1
	return cfg
}

func mustParseUUID(t *testing.T, s string) openapi_types.UUID {
	t.Helper()
	var id openapi_types.UUID
	if err := id.UnmarshalText([]byte(s)); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWorker_Claim409DoesNotExecute(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t)
	api := &fakeAPI{claimStatus: http.StatusConflict}
	exec := &fakeExec{result: ExecResult{ExitCode: 0}}
	dir := t.TempDir()
	h := NewHandoff()
	w := &Worker{
		Handoff:  h,
		Executor: exec,
		Audit:    &AuditLog{DataDir: dir},
		Config:   &cfg,
		Log:      NopLogger{},
		NewClient: func(string) (API, error) {
			return api, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	jobID := "11111111-1111-4111-8111-111111111111"
	h.Enqueue([]string{jobID})
	deadline := time.Now().Add(2 * time.Second)
	for api.claimCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if api.claimCalls.Load() == 0 {
		t.Fatal("claim not called")
	}
	if len(exec.calls) != 0 {
		t.Fatalf("executed despite 409: %#v", exec.calls)
	}
	if api.resultCalls.Load() != 0 {
		t.Fatal("result should not be posted on skip")
	}
	cancel()
}

func TestWorker_RejectsUnknownTypeBeforeSpawn(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t)
	jobID := "22222222-2222-4222-8222-222222222222"
	mid := mustParseUUID(t, cfg.MachineID)
	now := time.Now().UTC()
	api := &fakeAPI{
		claimJob: &agentapi.Job{
			Id:        mustParseUUID(t, jobID),
			MachineId: mid,
			Status:    agentapi.DadJokeCommonJobStatusRunning,
			Type:      agentapi.DadJokeCommonJobType("bash"),
			Payload:   "echo nope",
			CreatedAt: now,
			ExpiresAt: now.Add(time.Hour),
			ClaimedAt: &now,
		},
	}
	exec := &fakeExec{}
	dir := t.TempDir()
	h := NewHandoff()
	w := &Worker{
		Handoff: h, Executor: exec, Audit: &AuditLog{DataDir: dir}, Config: &cfg, Log: NopLogger{},
		NewClient: func(string) (API, error) { return api, nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	h.Enqueue([]string{jobID})
	deadline := time.Now().Add(2 * time.Second)
	for api.resultCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(exec.calls) != 0 {
		t.Fatal("must not spawn on unknown type")
	}
	if api.resultCalls.Load() == 0 {
		t.Fatal("expected failed result submit")
	}
	api.mu.Lock()
	st := api.lastResult.Status
	api.mu.Unlock()
	if st != agentapi.DadJokeCommonJobResultStatusFailed {
		t.Fatalf("status = %s", st)
	}
	cancel()
}

func TestWorker_SequentialAndHeartbeatIsolation(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	exec := &fakeExec{started: started, block: release, result: ExecResult{ExitCode: 0, Stdout: "ok"}}
	api := &fakeAPI{executed: make(chan struct{}, 2)}
	dir := t.TempDir()
	h := NewHandoff()
	w := &Worker{
		Handoff: h, Executor: exec, Audit: &AuditLog{DataDir: dir}, Config: &cfg, Log: NopLogger{},
		NewClient: func(string) (API, error) { return api, nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	id1 := "33333333-3333-4333-8333-333333333333"
	id2 := "44444444-4444-4444-8444-444444444444"
	h.Enqueue([]string{id1, id2})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job1 did not start")
	}
	// While job1 is blocked, job2 must not be running (sequential).
	time.Sleep(50 * time.Millisecond)
	if exec.running.Load() != 1 {
		t.Fatalf("running = %d, want 1 (sequential)", exec.running.Load())
	}
	// Heartbeat handoff remains non-blocking while job runs.
	done := make(chan struct{})
	go func() {
		h.Enqueue([]string{"55555555-5555-4555-8555-555555555555"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Enqueue blocked while job running")
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for api.resultCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if api.resultCalls.Load() < 2 {
		t.Fatalf("results = %d, want >=2", api.resultCalls.Load())
	}
	cancel()
}

func TestWorker_HugeOutputTruncatedAndResultOK(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t)
	huge := strings.Repeat("x", MaxOutputBytes+4096)
	api := &fakeAPI{}
	exec := &fakeExec{result: ExecResult{ExitCode: 0, Stdout: huge, Stderr: "", DurationMS: 5}}
	dir := t.TempDir()
	h := NewHandoff()
	w := &Worker{
		Handoff: h, Executor: exec, Audit: &AuditLog{DataDir: dir}, Config: &cfg, Log: NopLogger{},
		NewClient: func(string) (API, error) { return api, nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	h.Enqueue([]string{"66666666-6666-4666-8666-666666666666"})
	deadline := time.Now().Add(2 * time.Second)
	for api.resultCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.lastResult == nil || api.lastResult.Stdout == nil {
		t.Fatal("missing result")
	}
	if len(*api.lastResult.Stdout) > MaxOutputBytes {
		t.Fatalf("stdout len %d", len(*api.lastResult.Stdout))
	}
	if api.lastResult.Status != agentapi.DadJokeCommonJobResultStatusSucceeded {
		t.Fatalf("status = %s", api.lastResult.Status)
	}
	cancel()
}

func TestWorker_TimedOutStatus(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t)
	api := &fakeAPI{}
	exec := &fakeExec{result: ExecResult{TimedOut: true, ExitCode: -1, DurationMS: 60, Stderr: "killed"}}
	dir := t.TempDir()
	h := NewHandoff()
	w := &Worker{
		Handoff: h, Executor: exec, Audit: &AuditLog{DataDir: dir}, Config: &cfg, Log: NopLogger{},
		NewClient: func(string) (API, error) { return api, nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	h.Enqueue([]string{"77777777-7777-4777-8777-777777777777"})
	deadline := time.Now().Add(2 * time.Second)
	for api.resultCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	api.mu.Lock()
	st := api.lastResult.Status
	api.mu.Unlock()
	if st != agentapi.DadJokeCommonJobResultStatusTimedOut {
		t.Fatalf("status = %s", st)
	}
	if api.eventCalls.Load() == 0 {
		t.Fatal("expected event emit")
	}
	cancel()
}

func TestAuditLog_NeverWritesToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	token := "dj_agt_" + strings.Repeat("b", 64)
	a := &AuditLog{DataDir: dir, Redact: []string{token}}
	err := a.Append(AuditEntry{
		JobID:  "jid",
		Type:   "cmd",
		Status: "failed",
		Error:  "spawn failed token=" + token,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"job_id":"jid"`) {
		t.Fatalf("audit = %s", raw)
	}
	if strings.Contains(text, token) {
		t.Fatalf("raw agent token leaked into audit log: %s", text)
	}
	if !strings.Contains(text, auditRedacted) {
		t.Fatalf("expected %s in audit line: %s", auditRedacted, text)
	}
	if _, err := os.Stat(filepath.Join(dir, AuditFileName)); err != nil {
		t.Fatal(err)
	}
}

func TestHandoff_DedupeAndNonBlocking(t *testing.T) {
	t.Parallel()
	h := NewHandoff()
	h.Enqueue([]string{"a", "a", "b"})
	if h.Len() != 2 {
		t.Fatalf("len = %d", h.Len())
	}
	id, ok := h.Pop()
	if !ok || id != "a" {
		t.Fatalf("pop1 = %q %v", id, ok)
	}
	id, ok = h.Pop()
	if !ok || id != "b" {
		t.Fatalf("pop2 = %q %v", id, ok)
	}
}
