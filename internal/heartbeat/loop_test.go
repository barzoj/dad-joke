package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/jobs"
)

type logBuf struct {
	mu       sync.Mutex
	rendered []string
}

func (l *logBuf) Log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rendered = append(l.rendered, fmt.Sprintf(format, args...))
}

func (l *logBuf) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.rendered, "\n")
}

type fakeHost struct{}

func (fakeHost) Hostname() (string, error) { return "test-host", nil }
func (fakeHost) OSVersion() string         { return "test-os" }

type fakeClient struct {
	mu       sync.Mutex
	beats    atomic.Int32
	err      error
	status   int
	lastAuth string
	lastBody *agentapi.PostHeartbeatJSONRequestBody
	editors  []agentapi.RequestEditorFn
	pending  []string
}

func (c *fakeClient) PostHeartbeatWithResponse(ctx context.Context, body agentapi.PostHeartbeatJSONRequestBody, reqEditors ...agentapi.RequestEditorFn) (*agentapi.PostHeartbeatResponse, error) {
	c.beats.Add(1)
	c.mu.Lock()
	c.lastBody = &body
	c.editors = append([]agentapi.RequestEditorFn(nil), reqEditors...)
	err := c.err
	status := c.status
	if status == 0 {
		status = http.StatusOK
	}
	c.mu.Unlock()

	// Apply editors like the generated client would, capturing Authorization.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/v1/heartbeat", nil)
	for _, ed := range reqEditors {
		if ed == nil {
			continue
		}
		if e := ed(ctx, req); e != nil {
			return nil, e
		}
	}
	c.mu.Lock()
	c.lastAuth = req.Header.Get("Authorization")
	c.mu.Unlock()

	if err != nil {
		return nil, err
	}
	out := &agentapi.PostHeartbeatResponse{HTTPResponse: &http.Response{StatusCode: status}}
	if status == http.StatusOK {
		out.JSON200 = &agentapi.HeartbeatResponse{
			ServerTime:           time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC),
			DesiredPolicyVersion: 0,
			PendingJobIds:        nil,
		}
		c.mu.Lock()
		if c.pending != nil {
			out.JSON200.PendingJobIds = append([]string(nil), c.pending...)
		}
		c.mu.Unlock()
	}
	return out, nil
}

func testCfg(backend string, interval int) config.Config {
	cfg := config.WithTestIdentity(config.Default())
	cfg.BackendURL = backend
	cfg.HeartbeatIntervalSeconds = interval
	cfg.Token = "super-secret-heartbeat-token-value-do-not-leak"
	return cfg
}

func TestLoopEmptyBackendURLNoErrors(t *testing.T) {
	t.Parallel()
	cfg := testCfg("", 1)
	logs := &logBuf{}
	loop := &Loop{
		Version: "0.0.1-dev",
		Config:  &cfg,
		Log:     logs,
		Clock:   RealClock{},
		Host:    fakeHost{},
		NewClient: func(string) (HeartbeatClient, error) {
			t.Fatal("NewClient must not be called when backend_url is empty")
			return nil, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.joined(), "heartbeat disabled") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("expected disabled log")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	out := logs.joined()
	if strings.Contains(out, "beat failed") || strings.Contains(out, "error") {
		t.Fatalf("unexpected error logs: %s", out)
	}
	if strings.Contains(out, cfg.Token) {
		t.Fatalf("token leaked: %s", out)
	}
}

func TestLoopSendsBearerMachineIDVersion(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9", 60)
	logs := &logBuf{}
	fc := &fakeClient{}
	loop := &Loop{
		Version: "1.2.3-test",
		Config:  &cfg,
		Log:     logs,
		Clock:   RealClock{},
		Host:    fakeHost{},
		NewClient: func(url string) (HeartbeatClient, error) {
			if url != cfg.BackendURL {
				t.Fatalf("url = %q", url)
			}
			return fc, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for fc.beats.Load() < 1 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no heartbeat")
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

	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.lastAuth != "Bearer "+cfg.Token {
		t.Fatalf("Authorization = %q", fc.lastAuth)
	}
	if fc.lastBody == nil {
		t.Fatal("missing body")
	}
	if fc.lastBody.AgentVersion != "1.2.3-test" {
		t.Fatalf("version = %q", fc.lastBody.AgentVersion)
	}
	gotID, _ := fc.lastBody.MachineId.MarshalText()
	if string(gotID) != cfg.MachineID {
		t.Fatalf("machine_id = %s", gotID)
	}
	if fc.lastBody.AppliedPolicyVersion != 0 {
		t.Fatalf("applied_policy_version = %d", fc.lastBody.AppliedPolicyVersion)
	}
	if strings.Contains(logs.joined(), cfg.Token) {
		t.Fatalf("token leaked in logs: %s", logs.joined())
	}
}

func TestLoopBackendErrorsDoNotStopOrLeakToken(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9", 1)
	logs := &logBuf{}
	fc := &fakeClient{err: errors.New("dial tcp: connection refused involving " + cfg.Token)}
	loop := &Loop{
		Version: "0.0.1-dev",
		Config:  &cfg,
		Log:     logs,
		Clock:   RealClock{},
		Host:    fakeHost{},
		NewClient: func(string) (HeartbeatClient, error) {
			return fc, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for fc.beats.Load() < 2 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("beats = %d", fc.beats.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	out := logs.joined()
	if !strings.Contains(out, "beat failed") {
		t.Fatalf("expected failure log: %s", out)
	}
	if strings.Contains(out, cfg.Token) {
		t.Fatalf("token leaked: %s", out)
	}
	if !strings.Contains(out, config.RedactedToken) {
		t.Fatalf("expected redaction marker: %s", out)
	}
}

func TestLoopHTTP5xxContinuesAndShutsDown(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9", 1)
	logs := &logBuf{}
	fc := &fakeClient{status: http.StatusInternalServerError}
	loop := &Loop{
		Version: "0.0.1-dev",
		Config:  &cfg,
		Log:     logs,
		Clock:   RealClock{},
		Host:    fakeHost{},
		NewClient: func(string) (HeartbeatClient, error) {
			return fc, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for fc.beats.Load() < 2 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("beats = %d", fc.beats.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if strings.Contains(logs.joined(), cfg.Token) {
		t.Fatal("token leaked")
	}
}

func TestRedactSecret(t *testing.T) {
	t.Parallel()
	secret := "abc-secret-xyz"
	got := redactSecret("failed: abc-secret-xyz in message", secret)
	if strings.Contains(got, secret) {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, config.RedactedToken) {
		t.Fatalf("got %q", got)
	}
}

func TestLoopReusesClientWhenBackendURLUnchanged(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9", 1)
	logs := &logBuf{}
	fc := &fakeClient{}
	var builds atomic.Int32
	loop := &Loop{
		Version: "0.0.1-dev",
		Config:  &cfg,
		Log:     logs,
		Clock:   RealClock{},
		Host:    fakeHost{},
		NewClient: func(url string) (HeartbeatClient, error) {
			builds.Add(1)
			if url != cfg.BackendURL {
				t.Fatalf("url = %q", url)
			}
			return fc, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for fc.beats.Load() < 3 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("beats = %d", fc.beats.Load())
		}
		time.Sleep(20 * time.Millisecond)
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
	if builds.Load() != 1 {
		t.Fatalf("NewClient calls = %d, want 1 (reuse when backend_url unchanged)", builds.Load())
	}
}

func TestLoopHandsOffPendingJobsWithoutBlocking(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9", 60)
	fc := &fakeClient{pending: []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
	}}
	h := jobs.NewHandoff()
	// Slow consumer: Enqueue must still return immediately.
	loop := &Loop{
		Version: "0.0.1-dev",
		Config:  &cfg,
		Log:     NopLogger{},
		Clock:   RealClock{},
		Host:    fakeHost{},
		Jobs:    h,
		NewClient: func(string) (HeartbeatClient, error) {
			return fc, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for h.Len() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("handoff len = %d, want 2", h.Len())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Second beat with same ids must not block even if queue still full of them
	// (dedupe keeps len at 2).
	done := make(chan struct{})
	go func() {
		h.Enqueue([]string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Enqueue blocked")
	}
}

func TestLoopContextCanceledBeatIsSilent(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9", 60)
	logs := &logBuf{}
	fc := &fakeClient{err: context.Canceled}
	loop := &Loop{
		Version: "0.0.1-dev",
		Config:  &cfg,
		Log:     logs,
		Clock:   RealClock{},
		Host:    fakeHost{},
		NewClient: func(string) (HeartbeatClient, error) {
			return fc, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for fc.beats.Load() < 1 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no beat")
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
	if strings.Contains(logs.joined(), "beat failed") {
		t.Fatalf("context.Canceled must not log beat failed: %s", logs.joined())
	}
}
