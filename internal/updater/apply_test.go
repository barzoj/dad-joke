package updater

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeService struct {
	mu                sync.Mutex
	state             ServiceState
	startN            int
	stopN             int
	queryN            int
	failStartAfter    int // if >0, Start fails while startN <= failStartAfter
	crashAfterQueries int // if >0, flip to Stopped once queryN exceeds this after Start
}

func (f *fakeService) Stop(string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopN++
	f.state = StateStopped
	return nil
}

func (f *fakeService) Start(string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startN++
	if f.failStartAfter > 0 && f.startN <= f.failStartAfter {
		return fmt.Errorf("start failed")
	}
	f.state = StateRunning
	f.queryN = 0
	return nil
}

func (f *fakeService) Wait(_ string, state ServiceState, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == state {
		return nil
	}
	return fmt.Errorf("not yet")
}

func (f *fakeService) Query(string) (ServiceState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queryN++
	if f.crashAfterQueries > 0 && f.state == StateRunning && f.queryN > f.crashAfterQueries {
		f.state = StateStopped
	}
	return f.state, nil
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock {
	return &fakeClock{now: t}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	ch <- now
	return ch
}

func TestApplyHappyPath(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	bin := `C:\Program Files\DadJoke\dad-joke.exe`
	stagedDir := `C:\ProgramData\DadJoke\updates\ok`
	staged := filepath.Join(stagedDir, AssetEXE)
	_ = store.WriteFile(bin, []byte("old-bin"))
	_ = store.WriteFile(staged, []byte("new-bin"))

	svc := &fakeService{state: StateRunning}
	clock := newFakeClock(time.Now())
	a := &Applier{Store: store, Service: svc, Clock: clock, Log: NopLogger{}}
	err := a.Apply(context.Background(), ApplyOptions{
		ServiceName:   "DadJoke",
		BinaryPath:    bin,
		StagedExePath: staged,
		HealthWait:    time.Second,
		StopTimeout:   time.Second,
		StartTimeout:  time.Second,
		SkipPathCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := store.ReadFile(bin)
	if string(got) != "new-bin" {
		t.Fatalf("binary = %q", got)
	}
	old, err := store.ReadFile(bin + ".old")
	if err != nil || string(old) != "old-bin" {
		t.Fatalf(".old = %q err=%v", old, err)
	}
	if ok, _ := store.Exists(staged); ok {
		t.Fatal("staging should be cleaned")
	}
}

func TestApplyRollbackOnStartFailure(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	bin := `C:\Program Files\DadJoke\dad-joke.exe`
	stagedDir := `C:\ProgramData\DadJoke\updates\fail`
	staged := filepath.Join(stagedDir, AssetEXE)
	_ = store.WriteFile(bin, []byte("old-bin"))
	_ = store.WriteFile(staged, []byte("bad-bin"))

	svc := &fakeService{state: StateRunning, failStartAfter: 1}
	a := &Applier{Store: store, Service: svc, Clock: newFakeClock(time.Now()), Log: NopLogger{}}
	err := a.Apply(context.Background(), ApplyOptions{
		BinaryPath:    bin,
		StagedExePath: staged,
		HealthWait:    time.Millisecond,
		StopTimeout:   time.Second,
		StartTimeout:  time.Second,
		SkipPathCheck: true,
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v", err)
	}
	got, _ := store.ReadFile(bin)
	if string(got) != "old-bin" {
		t.Fatalf("binary after rollback = %q", got)
	}
	if svc.state != StateRunning {
		t.Fatalf("service state = %s", svc.state)
	}
}

func TestApplyRollbackOnHealthFailure(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	bin := `C:\Program Files\DadJoke\dad-joke.exe`
	stagedDir := `C:\ProgramData\DadJoke\updates\health`
	staged := filepath.Join(stagedDir, AssetEXE)
	_ = store.WriteFile(bin, []byte("old-bin"))
	_ = store.WriteFile(staged, []byte("flaky-bin"))

	// Stay Running for the post-Start waitState Query, then crash on the health Query.
	svc := &fakeService{state: StateRunning, crashAfterQueries: 1}
	a := &Applier{Store: store, Service: svc, Clock: newFakeClock(time.Now()), Log: NopLogger{}}
	err := a.Apply(context.Background(), ApplyOptions{
		BinaryPath:    bin,
		StagedExePath: staged,
		HealthWait:    time.Millisecond,
		StopTimeout:   time.Second,
		StartTimeout:  time.Second,
		SkipPathCheck: true,
	})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v", err)
	}
	got, _ := store.ReadFile(bin)
	if string(got) != "old-bin" {
		t.Fatalf("binary after rollback = %q", got)
	}
}

func TestApplyFailsClosedOnACL(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	store.aclErr = fmt.Errorf("acl denied")
	bin := `C:\Program Files\DadJoke\dad-joke.exe`
	staged := `C:\ProgramData\DadJoke\updates\acl\` + AssetEXE
	_ = store.WriteFile(bin, []byte("old-bin"))
	_ = store.WriteFile(staged, []byte("new-bin"))

	svc := &fakeService{state: StateRunning}
	a := &Applier{Store: store, Service: svc, Clock: newFakeClock(time.Now()), Log: NopLogger{}}
	err := a.Apply(context.Background(), ApplyOptions{
		BinaryPath:    bin,
		StagedExePath: staged,
		SkipPathCheck: true,
		StopTimeout:   time.Second,
		StartTimeout:  time.Second,
		HealthWait:    time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "ACL") {
		t.Fatalf("err = %v", err)
	}
	got, _ := store.ReadFile(bin)
	if string(got) != "old-bin" {
		t.Fatalf("binary after ACL failure = %q", got)
	}
	if svc.state != StateRunning {
		t.Fatalf("service state = %s", svc.state)
	}
}

func TestApplyRejectsPathAllowlist(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	a := &Applier{Store: store, Service: &fakeService{state: StateRunning}, Clock: newFakeClock(time.Now()), Log: NopLogger{}}
	err := a.Apply(context.Background(), ApplyOptions{
		BinaryPath:    `C:\Windows\System32\cmd.exe`,
		StagedExePath: `C:\Temp\evil.exe`,
		StopTimeout:   time.Second,
		StartTimeout:  time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "path allowlist") {
		t.Fatalf("err = %v", err)
	}
}

func TestTriggerApplyArgs(t *testing.T) {
	t.Parallel()
	var gotName string
	var gotArgs []string
	sp := spawnerFunc(func(_ context.Context, name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	})
	err := TriggerApply(context.Background(), sp, TriggerOptions{
		HelperExe:     `C:\Program Files\DadJoke\dad-joke.exe`,
		StagedExePath: `C:\ProgramData\DadJoke\updates\1\x.exe`,
		BinaryPath:    `C:\Program Files\DadJoke\dad-joke.exe`,
		ServiceName:   "DadJoke",
		HealthWait:    5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotName == "" || gotArgs[0] != "apply-update" {
		t.Fatalf("spawn = %s %v", gotName, gotArgs)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "--staged") || !strings.Contains(joined, "--health-seconds 5") {
		t.Fatalf("args = %v", gotArgs)
	}
}

type spawnerFunc func(ctx context.Context, name string, args ...string) error

func (f spawnerFunc) StartDetached(ctx context.Context, name string, args ...string) error {
	return f(ctx, name, args...)
}
