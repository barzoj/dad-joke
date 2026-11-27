package updater

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
)

type logBuf struct {
	mu          sync.Mutex
	lines       []string
	rendered    []string
	captureArgs bool
}

func (l *logBuf) Log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, format)
	if l.captureArgs {
		l.rendered = append(l.rendered, fmt.Sprintf(format, args...))
	}
}

type countingClient struct {
	mu       sync.Mutex
	fetches  atomic.Int32
	meta     ReleaseMeta
	err      error
	versions []string
}

func (c *countingClient) FetchLatest(_ context.Context, _ string, agentVersion string) (ReleaseMeta, error) {
	c.fetches.Add(1)
	c.mu.Lock()
	c.versions = append(c.versions, agentVersion)
	err := c.err
	meta := c.meta
	c.mu.Unlock()
	if err != nil {
		return ReleaseMeta{}, err
	}
	return meta, nil
}

func (c *countingClient) Download(context.Context, string, string, int64) ([]byte, error) {
	return nil, nil
}

func TestPollerCancelAndErrorResilience(t *testing.T) {
	t.Parallel()

	client := &countingClient{
		meta: ReleaseMeta{TagName: "v0.0.1"}, // equal to current → no update
		err:  context.DeadlineExceeded,       // first checks fail; must not crash
	}
	cfg := config.WithTestIdentity(config.Config{
		SchemaVersion:       1,
		ReleaseAPIURL:       "http://127.0.0.1:9/releases/latest",
		PollIntervalSeconds: 1,
	})
	var spawned atomic.Int32
	sp := spawnerFunc(func(context.Context, string, ...string) error {
		spawned.Add(1)
		return nil
	})
	logs := &logBuf{}
	p := &Poller{
		Version:    "0.0.1",
		Config:     &cfg,
		Client:     client,
		Store:      newMemStore(),
		Spawner:    sp,
		Clock:      RealClock{},
		Log:        logs,
		DataDir:    t.TempDir(),
		BinaryPath: "bin",
		HelperExe:  "helper",
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for client.fetches.Load() < 2 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("fetches = %d, want >= 2", client.fetches.Load())
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Recover from errors: clear err so a successful equal-version poll happens.
	client.mu.Lock()
	client.err = nil
	client.mu.Unlock()

	deadline = time.Now().Add(3 * time.Second)
	start := client.fetches.Load()
	for client.fetches.Load() < start+1 {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poller did not stop on cancel")
	}

	if spawned.Load() != 0 {
		t.Fatalf("spawned = %d, want 0", spawned.Load())
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, v := range client.versions {
		if v != "0.0.1" {
			t.Fatalf("agent version header input = %q", v)
		}
	}
}

func TestPollerTriggersUpdate(t *testing.T) {
	t.Parallel()
	exe := []byte("v2")
	sum := HashBytes(exe)
	fc := &fakeClient{
		meta: ReleaseMeta{
			TagName: "v0.0.2",
			Assets: []AssetMeta{
				{Name: AssetEXE, BrowserDownloadURL: "http://127.0.0.1/exe"},
				{Name: AssetSHA256, BrowserDownloadURL: "http://127.0.0.1/sha"},
			},
		},
		blobs: map[string][]byte{
			"http://127.0.0.1/exe": exe,
			"http://127.0.0.1/sha": []byte(sum + "  " + AssetEXE + "\n"),
		},
		dlErr: map[string]error{},
	}
	var gotArgs []string
	sp := spawnerFunc(func(_ context.Context, _ string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	})
	cfg := config.WithTestIdentity(config.Config{SchemaVersion: 1, ReleaseAPIURL: "http://127.0.0.1/r", PollIntervalSeconds: 60})
	p := &Poller{
		Version: "0.0.1-dev", Config: &cfg, Client: fc, Store: newMemStore(), Spawner: sp,
		Clock: RealClock{}, Log: NopLogger{}, DataDir: t.TempDir(), BinaryPath: "bin", HelperExe: "helper",
		stagingID: func() string { return "s" },
	}
	if err := p.checkOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) == 0 || gotArgs[0] != "apply-update" {
		t.Fatalf("args = %v", gotArgs)
	}
	if !strings.Contains(strings.Join(gotArgs, " "), "--staged") {
		t.Fatalf("missing staged: %v", gotArgs)
	}
}

func TestPollerClearsStickyInFlightAfterTTL(t *testing.T) {
	t.Parallel()
	exe := []byte("v2")
	sum := HashBytes(exe)
	fc := &fakeClient{
		meta: ReleaseMeta{
			TagName: "v0.0.2",
			Assets: []AssetMeta{
				{Name: AssetEXE, BrowserDownloadURL: "http://127.0.0.1/exe"},
				{Name: AssetSHA256, BrowserDownloadURL: "http://127.0.0.1/sha"},
			},
		},
		blobs: map[string][]byte{
			"http://127.0.0.1/exe": exe,
			"http://127.0.0.1/sha": []byte(sum + "  " + AssetEXE + "\n"),
		},
		dlErr: map[string]error{},
	}
	var spawned atomic.Int32
	sp := spawnerFunc(func(context.Context, string, ...string) error {
		spawned.Add(1)
		return nil // helper "dies" without stopping service
	})
	cfg := config.WithTestIdentity(config.Config{SchemaVersion: 1, ReleaseAPIURL: "http://127.0.0.1/r", PollIntervalSeconds: 60})
	clock := newFakeClock(time.Unix(1_000, 0))
	p := &Poller{
		Version:           "0.0.1",
		Config:            &cfg,
		Client:            fc,
		Store:             newMemStore(),
		Spawner:           sp,
		Clock:             clock,
		Log:               NopLogger{},
		DataDir:           t.TempDir(),
		BinaryPath:        "bin",
		HelperExe:         "helper",
		UpdateInFlightTTL: time.Minute,
		stagingID:         func() string { return fmt.Sprintf("%d", spawned.Load()) },
	}
	if err := p.checkOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if spawned.Load() != 1 {
		t.Fatalf("spawned = %d", spawned.Load())
	}
	// Still within TTL: second attempt must skip.
	if err := p.checkOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if spawned.Load() != 1 {
		t.Fatalf("spawned during TTL = %d", spawned.Load())
	}
	// Helper died; advance past TTL — poller must retry.
	clock.mu.Lock()
	clock.now = clock.now.Add(2 * time.Minute)
	clock.mu.Unlock()
	if err := p.checkOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if spawned.Load() != 2 {
		t.Fatalf("spawned after TTL = %d, want 2", spawned.Load())
	}
}

func TestPollerDevUpdatesToNumbered(t *testing.T) {
	t.Parallel()
	exe := []byte("v1")
	sum := HashBytes(exe)
	fc := &fakeClient{
		meta: ReleaseMeta{
			TagName: "v0.0.1",
			Assets: []AssetMeta{
				{Name: AssetEXE, BrowserDownloadURL: "http://127.0.0.1/exe"},
				{Name: AssetSHA256, BrowserDownloadURL: "http://127.0.0.1/sha"},
			},
		},
		blobs: map[string][]byte{
			"http://127.0.0.1/exe": exe,
			"http://127.0.0.1/sha": []byte(sum + "  " + AssetEXE),
		},
		dlErr: map[string]error{},
	}
	var spawned atomic.Int32
	sp := spawnerFunc(func(context.Context, string, ...string) error {
		spawned.Add(1)
		return nil
	})
	cfg := config.WithTestIdentity(config.Default())
	cfg.ReleaseAPIURL = "http://127.0.0.1/r"
	p := &Poller{
		Version: "0.0.1-dev", Config: &cfg, Client: fc, Store: newMemStore(), Spawner: sp,
		Log: NopLogger{}, DataDir: t.TempDir(), BinaryPath: "bin", HelperExe: "helper",
		stagingID: func() string { return "s" },
	}
	if err := p.checkOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if spawned.Load() != 1 {
		t.Fatalf("spawned = %d", spawned.Load())
	}
}

func TestPollerFailsWithoutIdentity(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.ReleaseAPIURL = "http://127.0.0.1/r"
	logs := &logBuf{}
	p := &Poller{
		Version: "0.0.1", Config: &cfg, Client: &countingClient{}, Store: newMemStore(),
		Spawner: spawnerFunc(func(context.Context, string, ...string) error { return nil }),
		Clock:   RealClock{}, Log: logs, DataDir: t.TempDir(), BinaryPath: "bin", HelperExe: "helper",
	}
	err := p.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for missing machine_id/token")
	}
	if !strings.Contains(err.Error(), "machine_id") && !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v", err)
	}
}

func TestPollerStartupLogDoesNotLeakToken(t *testing.T) {
	t.Parallel()
	secret := "super-secret-token-value-do-not-leak"
	cfg := config.WithTestIdentity(config.Default())
	cfg.Token = secret
	cfg.ReleaseAPIURL = "http://127.0.0.1:9/r"
	cfg.PollIntervalSeconds = 60
	client := &countingClient{meta: ReleaseMeta{TagName: "v0.0.1"}}
	logs := &logBuf{}
	logs.captureArgs = true
	p := &Poller{
		Version: "0.0.1", Config: &cfg, Client: client, Store: newMemStore(),
		Spawner: spawnerFunc(func(context.Context, string, ...string) error { return nil }),
		Clock:   RealClock{}, Log: logs, DataDir: t.TempDir(), BinaryPath: "bin", HelperExe: "helper",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for client.fetches.Load() < 1 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("no fetch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, line := range logs.rendered {
		if strings.Contains(line, secret) {
			t.Fatalf("token leaked in log: %s", line)
		}
	}
}
