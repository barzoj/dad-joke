package factsup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
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

// recordingClock advances instantly and records After durations (for backoff asserts).
type recordingClock struct {
	mu      sync.Mutex
	now     time.Time
	afters  []time.Duration
	blockCh chan struct{} // if non-nil, After waits until closed or ctx-style unblock
}

func newRecordingClock(now time.Time) *recordingClock {
	return &recordingClock{now: now}
}

func (c *recordingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *recordingClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.afters = append(c.afters, d)
	c.now = c.now.Add(d)
	now := c.now
	block := c.blockCh
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if block != nil {
		go func() {
			<-block
			ch <- now
		}()
		return ch
	}
	ch <- now
	return ch
}

func (c *recordingClock) afterDurations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.afters))
	copy(out, c.afters)
	return out
}

type memQueue struct {
	mu      sync.Mutex
	batches []memBatch
	acks    []int64
	rejects []int64
}

type memBatch struct {
	seq  int64
	data []byte
}

func (q *memQueue) Peek() (seq int64, data []byte, ok bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.batches) == 0 {
		return 0, nil, false, nil
	}
	b := q.batches[0]
	cp := append([]byte(nil), b.data...)
	return b.seq, cp, true, nil
}

func (q *memQueue) Ack(seq int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.acks = append(q.acks, seq)
	if len(q.batches) > 0 && q.batches[0].seq == seq {
		q.batches = q.batches[1:]
	}
	return nil
}

func (q *memQueue) Reject(seq int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rejects = append(q.rejects, seq)
	if len(q.batches) > 0 && q.batches[0].seq == seq {
		q.batches = q.batches[1:]
	}
	return nil
}

func (q *memQueue) push(seq int64, data []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.batches = append(q.batches, memBatch{seq: seq, data: append([]byte(nil), data...)})
}

func (q *memQueue) ackCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.acks)
}

func (q *memQueue) rejectCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.rejects)
}

func (q *memQueue) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.batches)
}

type captureDoer struct {
	mu       sync.Mutex
	bodies   [][]byte
	auths    []string
	handler  func(req *http.Request, n int) (*http.Response, error)
	calls    atomic.Int32
	inflight atomic.Int32
	maxIn    atomic.Int32
}

func (d *captureDoer) Do(req *http.Request) (*http.Response, error) {
	n := int(d.calls.Add(1))
	cur := d.inflight.Add(1)
	for {
		old := d.maxIn.Load()
		if cur <= old || d.maxIn.CompareAndSwap(old, cur) {
			break
		}
	}
	defer d.inflight.Add(-1)

	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.bodies = append(d.bodies, append([]byte(nil), body...))
	d.auths = append(d.auths, req.Header.Get("Authorization"))
	h := d.handler
	d.mu.Unlock()
	if h == nil {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"accepted_seq":1,"duplicate":false}`)),
			Header:     make(http.Header),
		}, nil
	}
	// Restore body for handlers that re-read.
	req.Body = io.NopCloser(bytes.NewReader(body))
	return h(req, n)
}

func (d *captureDoer) bodyAt(i int) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i >= len(d.bodies) {
		return nil
	}
	return append([]byte(nil), d.bodies[i]...)
}

func (d *captureDoer) callCount() int {
	return int(d.calls.Load())
}

func testCfg(backend string) config.Config {
	cfg := config.WithTestIdentity(config.Default())
	cfg.BackendURL = backend
	cfg.Token = "super-secret-facts-token-value-do-not-leak"
	return cfg
}

func runUntil(t *testing.T, loop *Loop, pred func() bool, timeout time.Duration) (cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { ch <- loop.Run(ctx) }()
	deadline := time.Now().Add(timeout)
	for !pred() {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cancel, ch
}

func waitDone(t *testing.T, done <-chan error, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit")
	}
}

func TestUploadSuccess200Acks(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	payload := []byte(`{"stream_id":"s","seq":1,"facts":[]}`)
	q.push(1, payload)
	doer := &captureDoer{
		handler: func(req *http.Request, n int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accepted_seq":1,"duplicate":false}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	clock := newRecordingClock(time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC))
	logs := &logBuf{}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    logs,
		Clock:  clock,
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool { return q.ackCount() >= 1 }, 3*time.Second)
	waitDone(t, done, cancel)
	if q.rejectCount() != 0 {
		t.Fatalf("rejects = %d", q.rejectCount())
	}
	if !bytes.Equal(doer.bodyAt(0), payload) {
		t.Fatalf("body = %s", doer.bodyAt(0))
	}
}

func TestUploadDuplicateTrueStillAcks(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(7, []byte(`{"seq":7}`))
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accepted_seq":7,"duplicate":true}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool { return q.ackCount() >= 1 }, 3*time.Second)
	waitDone(t, done, cancel)
	if q.rejectCount() != 0 {
		t.Fatal("unexpected reject")
	}
}

func TestUpload400Rejects(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"bad":true}`))
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"error":"bad"}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	logs := &logBuf{}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    logs,
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool { return q.rejectCount() >= 1 }, 3*time.Second)
	waitDone(t, done, cancel)
	if q.ackCount() != 0 {
		t.Fatal("should not ack")
	}
}

func TestUpload401RetainsAndBacksOff(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"seq":1}`))
	clock := newRecordingClock(time.Now())
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"error":"auth"}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	logs := &logBuf{}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    logs,
		Clock:  clock,
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool {
		return doer.callCount() >= 2 && len(clock.afterDurations()) >= 1
	}, 3*time.Second)
	waitDone(t, done, cancel)

	if q.ackCount() != 0 || q.rejectCount() != 0 {
		t.Fatalf("acks=%d rejects=%d (batch must be retained)", q.ackCount(), q.rejectCount())
	}
	if q.pending() != 1 {
		t.Fatalf("pending = %d, want 1", q.pending())
	}
	afters := clock.afterDurations()
	if len(afters) == 0 {
		t.Fatal("expected backoff After")
	}
	if !strings.Contains(logs.joined(), "auth failed") {
		t.Fatalf("expected auth log: %s", logs.joined())
	}
}

func TestUpload403RetainsAndBacksOff(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"seq":1}`))
	clock := newRecordingClock(time.Now())
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	logs := &logBuf{}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    logs,
		Clock:  clock,
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool {
		return doer.callCount() >= 2 && len(clock.afterDurations()) >= 1
	}, 3*time.Second)
	waitDone(t, done, cancel)

	if q.ackCount() != 0 || q.rejectCount() != 0 {
		t.Fatalf("acks=%d rejects=%d (batch must be retained)", q.ackCount(), q.rejectCount())
	}
	if q.pending() != 1 {
		t.Fatalf("pending = %d, want 1", q.pending())
	}
	if !strings.Contains(logs.joined(), "auth failed") {
		t.Fatalf("expected auth log: %s", logs.joined())
	}
}

func TestNextBackoffGrowthCapAndJitter(t *testing.T) {
	t.Parallel()
	loop := &Loop{rng: rand.New(rand.NewSource(1))} // #nosec G404 -- deterministic test
	var seen []time.Duration
	for i := 0; i < 20; i++ {
		d := loop.nextBackoff()
		seen = append(seen, d)
		if d < 0 || d > maxBackoff {
			t.Fatalf("jitter %v out of [0, %v]", d, maxBackoff)
		}
		if loop.backoff > maxBackoff {
			t.Fatalf("backoff field %v > max %v", loop.backoff, maxBackoff)
		}
	}
	if loop.backoff != maxBackoff {
		t.Fatalf("after many doublings backoff = %v, want cap %v", loop.backoff, maxBackoff)
	}
	// Growth path: after first call backoff is base; after enough calls it hits 5m.
	growth := &Loop{rng: rand.New(rand.NewSource(2))} // #nosec G404
	_ = growth.nextBackoff()
	if growth.backoff != baseBackoff {
		t.Fatalf("first backoff field = %v, want %v", growth.backoff, baseBackoff)
	}
	_ = growth.nextBackoff()
	if growth.backoff != 2*baseBackoff {
		t.Fatalf("second backoff field = %v, want %v", growth.backoff, 2*baseBackoff)
	}
	// Nil rng → jitter always 0 (still advances internal backoff).
	noJitter := &Loop{}
	if d := noJitter.nextBackoff(); d != 0 {
		t.Fatalf("nil rng jitter = %v, want 0", d)
	}
	_ = seen
}

func TestUpload413Rejects(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(2, []byte(`{"huge":true}`))
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusRequestEntityTooLarge,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool { return q.rejectCount() >= 1 }, 3*time.Second)
	waitDone(t, done, cancel)
}

func TestUpload429HonoursRetryAfter(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"seq":1}`))
	clock := newRecordingClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			hdr := make(http.Header)
			hdr.Set("Retry-After", "45")
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     hdr,
			}, nil
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  clock,
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool {
		for _, d := range clock.afterDurations() {
			if d >= 45*time.Second {
				return true
			}
		}
		return false
	}, 3*time.Second)
	waitDone(t, done, cancel)
	if q.ackCount() != 0 || q.rejectCount() != 0 {
		t.Fatal("429 must retain batch")
	}
}

func TestUpload500RetriesSameBytes(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	payload := []byte(`{"stream_id":"abc","seq":9,"facts":[{"kind":"x"}]}`)
	q.push(9, payload)
	doer := &captureDoer{
		handler: func(req *http.Request, n int) (*http.Response, error) {
			if n < 3 {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`err`)),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accepted_seq":9,"duplicate":false}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool { return q.ackCount() >= 1 }, 3*time.Second)
	waitDone(t, done, cancel)
	if doer.callCount() < 3 {
		t.Fatalf("calls = %d, want ≥3", doer.callCount())
	}
	for i := 0; i < 3; i++ {
		if !bytes.Equal(doer.bodyAt(i), payload) {
			t.Fatalf("body[%d] = %s, want identical retry bytes", i, doer.bodyAt(i))
		}
	}
}

func TestUploadTimeoutRetriesSameBytes(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	payload := []byte(`{"seq":3,"retry":true}`)
	q.push(3, payload)
	doer := &captureDoer{
		handler: func(req *http.Request, n int) (*http.Response, error) {
			if n == 1 {
				return nil, errors.New("net/http: timeout awaiting response headers")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accepted_seq":3,"duplicate":false}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool { return q.ackCount() >= 1 }, 3*time.Second)
	waitDone(t, done, cancel)
	if doer.callCount() < 2 {
		t.Fatalf("calls = %d", doer.callCount())
	}
	if !bytes.Equal(doer.bodyAt(0), payload) || !bytes.Equal(doer.bodyAt(1), payload) {
		t.Fatal("retried body mismatch")
	}
}

func TestUploadOnlyOneInFlight(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"a":1}`))
	q.push(2, []byte(`{"a":2}`))

	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var loopRef atomic.Pointer[Loop]
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			started <- struct{}{}
			if l := loopRef.Load(); l != nil {
				if l.InFlight.Load() != 1 {
					t.Errorf("InFlight = %d during request, want 1", l.InFlight.Load())
				}
			}
			<-release
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"accepted_seq":1,"duplicate":false}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	loopRef.Store(loop)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("first request not started")
	}
	// While first is blocked, no second request should start.
	select {
	case <-started:
		close(release)
		cancel()
		t.Fatal("second request started while first still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	if doer.maxIn.Load() != 1 {
		t.Fatalf("max concurrent Do = %d, want 1", doer.maxIn.Load())
	}
	if loop.InFlight.Load() != 1 {
		t.Fatalf("InFlight = %d, want 1", loop.InFlight.Load())
	}
	close(release)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestUploadCancelDuringRequest(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"seq":1}`))
	entered := make(chan struct{})
	doer := &captureDoer{
		handler: func(req *http.Request, _ int) (*http.Response, error) {
			close(entered)
			<-req.Context().Done()
			return nil, req.Context().Err()
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("request not entered")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit on cancel")
	}
	if q.ackCount() != 0 {
		t.Fatal("must not ack on cancel")
	}
}

func TestUploadTokenRedactionInLogs(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9")
	q := &memQueue{}
	q.push(1, []byte(`{"seq":1}`))
	logs := &logBuf{}
	doer := &captureDoer{
		handler: func(*http.Request, int) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed involving %s", cfg.Token)
		},
	}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    logs,
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			return doer, nil
		},
	}
	cancel, done := runUntil(t, loop, func() bool {
		return strings.Contains(logs.joined(), "upload") && doer.callCount() >= 1
	}, 3*time.Second)
	waitDone(t, done, cancel)

	out := logs.joined()
	if strings.Contains(out, cfg.Token) {
		t.Fatalf("token leaked: %s", out)
	}
	if !strings.Contains(out, config.RedactedToken) {
		t.Fatalf("expected redaction marker: %s", out)
	}
}

func TestCheckRedirectStripsAuthorization(t *testing.T) {
	t.Parallel()
	orig, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/v1/facts", nil)
	if err != nil {
		t.Fatal(err)
	}
	orig.Header.Set("Authorization", "Bearer secret-token")

	next, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:2/v1/facts", nil)
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

	same, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:1/v1/facts", nil)
	same.Header.Set("Authorization", "Bearer secret-token")
	if err := checkRedirect(same, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if same.Header.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("same-origin Authorization = %q", same.Header.Get("Authorization"))
	}
}

func TestCheckRedirectRejectsNonLoopbackHTTP(t *testing.T) {
	t.Parallel()
	next, _ := http.NewRequest(http.MethodGet, "http://example.com/x", nil)
	if err := checkRedirect(next, nil); err == nil {
		t.Fatal("expected error for non-loopback http redirect")
	}
}

func TestNewHTTPRejectsNonLoopbackHTTP(t *testing.T) {
	t.Parallel()
	loop := &Loop{}
	_, err := loop.defaultHTTP("http://example.com")
	if !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	_, err = loop.defaultHTTP("http://127.0.0.1:8080")
	if err != nil {
		t.Fatalf("loopback http should be allowed: %v", err)
	}
}

func TestEmptyBackendURLIdleNoHTTP(t *testing.T) {
	t.Parallel()
	cfg := testCfg("")
	logs := &logBuf{}
	var builds atomic.Int32
	q := &memQueue{}
	q.push(1, []byte(`{"seq":1}`))
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    logs,
		Clock:  newRecordingClock(time.Now()),
		NewHTTP: func(string) (HTTPDoer, error) {
			builds.Add(1)
			t.Error("NewHTTP must not be called when backend_url is empty")
			return nil, errors.New("unreachable")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logs.joined(), "backend_url unset") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("expected idle log")
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
	if builds.Load() != 0 {
		t.Fatalf("NewHTTP calls = %d", builds.Load())
	}
	if q.ackCount() != 0 {
		t.Fatal("idle must not ack")
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if d := parseRetryAfter("30", now); d != 30*time.Second {
		t.Fatalf("seconds = %v", d)
	}
	if d := parseRetryAfter("-1", now); d != 0 {
		t.Fatalf("negative = %v", d)
	}
	httpDate := now.Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(httpDate, now); d != 90*time.Second {
		t.Fatalf("http-date = %v", d)
	}
	if d := parseRetryAfter("", now); d != 0 {
		t.Fatalf("empty = %v", d)
	}
}

func TestSameOrigin(t *testing.T) {
	t.Parallel()
	a, _ := url.Parse("http://127.0.0.1:1/a")
	b, _ := url.Parse("http://127.0.0.1:1/b")
	c, _ := url.Parse("http://127.0.0.1:2/a")
	if !sameOrigin(a, b) {
		t.Fatal("same host should match")
	}
	if sameOrigin(a, c) {
		t.Fatal("different port/host must not match")
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	tok := "secret-xyz"
	got := redact("failed with secret-xyz here", tok)
	if strings.Contains(got, tok) {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, config.RedactedToken) {
		t.Fatalf("got %q", got)
	}
}

func TestDecodeAck(t *testing.T) {
	t.Parallel()
	ack, err := DecodeAck([]byte(`{"accepted_seq":3,"duplicate":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if ack.AcceptedSeq != 3 || !ack.Duplicate {
		t.Fatalf("%+v", ack)
	}
}

func TestPostOnceSetsBearerAndPath(t *testing.T) {
	t.Parallel()
	cfg := testCfg("http://127.0.0.1:9/")
	var gotURL string
	var gotAuth string
	var gotCT string
	doer := &captureDoer{
		handler: func(req *http.Request, _ int) (*http.Response, error) {
			gotURL = req.URL.String()
			gotAuth = req.Header.Get("Authorization")
			gotCT = req.Header.Get("Content-Type")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
				Header:     make(http.Header),
			}, nil
		},
	}
	loop := &Loop{Clock: newRecordingClock(time.Now()), Log: NopLogger{}}
	status, _, err := loop.postOnce(context.Background(), doer, cfg, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !strings.HasSuffix(gotURL, "/v1/facts") {
		t.Fatalf("url = %q", gotURL)
	}
	if gotAuth != "Bearer "+cfg.Token {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
}

// Ensure httptest import is used for redirect integration via real client CheckRedirect.
func TestCheckRedirectViaHTTPClient(t *testing.T) {
	t.Parallel()
	var sawAuthOnSecond atomic.Value
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthOnSecond.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted_seq":1,"duplicate":false}`))
	}))
	t.Cleanup(final.Close)

	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Different host (different port) → Authorization must be stripped.
		http.Redirect(w, r, final.URL+"/v1/facts", http.StatusFound)
	}))
	t.Cleanup(redir.Close)

	client := &http.Client{CheckRedirect: checkRedirect}
	req, err := http.NewRequest(http.MethodPost, redir.URL+"/v1/facts", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	auth, _ := sawAuthOnSecond.Load().(string)
	if auth != "" {
		t.Fatalf("Authorization leaked across redirect host change: %q", auth)
	}
}
