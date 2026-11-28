package sessionagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/service"
)

type memClock struct {
	mu     sync.Mutex
	now    time.Time
	afters []time.Duration
	// blockAfter, when set, makes After wait until UnblockAfter.
	blockAfter chan struct{}
}

func newMemClock(now time.Time) *memClock {
	return &memClock{now: now}
}

func (c *memClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *memClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *memClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.afters = append(c.afters, d)
	c.now = c.now.Add(d)
	now := c.now
	block := c.blockAfter
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

func (c *memClock) lastAfter() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.afters) == 0 {
		return 0
	}
	return c.afters[len(c.afters)-1]
}

type fakeChild struct {
	pid      uint32
	exitCh   chan struct{}
	termCh   chan struct{}
	termHang time.Duration
	closed   atomic.Bool
	once     sync.Once
}

func newFakeChild(pid uint32) *fakeChild {
	return &fakeChild{pid: pid, exitCh: make(chan struct{}), termCh: make(chan struct{}, 1)}
}

func (c *fakeChild) PID() uint32 { return c.pid }

func (c *fakeChild) Wait(ctx context.Context) error {
	select {
	case <-c.exitCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeChild) Terminate() error {
	select {
	case c.termCh <- struct{}{}:
	default:
	}
	if c.termHang > 0 {
		// Unresponsive child: Terminate returns but Wait never sees exit
		// until the supervisor's wait budget expires.
		return nil
	}
	c.once.Do(func() { close(c.exitCh) })
	return nil
}

func (c *fakeChild) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *fakeChild) kill() {
	c.once.Do(func() { close(c.exitCh) })
}

type fakeLauncher struct {
	mu        sync.Mutex
	launches  []uint32
	tokens    map[uint32]bool
	err       error
	nextPID   uint32
	children  []*fakeChild
	hangTerm  time.Duration
	launchCh  chan struct{} // optional signal per launch
	launchFn  func(sessionID uint32) (Child, error)
}

func newFakeLauncher(tokens ...uint32) *fakeLauncher {
	m := make(map[uint32]bool)
	for _, id := range tokens {
		m[id] = true
	}
	return &fakeLauncher{tokens: m, nextPID: 1000}
}

func (l *fakeLauncher) HasUserToken(sessionID uint32) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tokens[sessionID]
}

func (l *fakeLauncher) setToken(sessionID uint32, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tokens == nil {
		l.tokens = make(map[uint32]bool)
	}
	l.tokens[sessionID] = ok
}

func (l *fakeLauncher) Launch(_ context.Context, sessionID uint32) (Child, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.launches = append(l.launches, sessionID)
	if l.launchCh != nil {
		select {
		case l.launchCh <- struct{}{}:
		default:
		}
	}
	if l.launchFn != nil {
		return l.launchFn(sessionID)
	}
	if l.err != nil {
		return nil, l.err
	}
	if !l.tokens[sessionID] {
		return nil, ErrNoToken
	}
	l.nextPID++
	ch := newFakeChild(l.nextPID)
	if l.hangTerm > 0 {
		ch.termHang = l.hangTerm
	}
	l.children = append(l.children, ch)
	return ch, nil
}

func (l *fakeLauncher) launchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.launches)
}

func (l *fakeLauncher) lastChild() *fakeChild {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.children) == 0 {
		return nil
	}
	return l.children[len(l.children)-1]
}

type fixedSessions struct {
	mu   sync.Mutex
	list []Session
	err  error
}

func (f *fixedSessions) Sessions(context.Context) ([]Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]Session, len(f.list))
	copy(out, f.list)
	return out, nil
}

func (f *fixedSessions) set(list []Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = list
}

type recordingSink struct {
	mu   sync.Mutex
	ids  []uint32
	err  error
}

func (r *recordingSink) EnqueueRestarted(sessionID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.ids = append(r.ids, sessionID)
	return nil
}

func (r *recordingSink) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

func startSup(t *testing.T, s *Supervisor) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, errCh
}

func waitLaunch(t *testing.T, l *fakeLauncher, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for l.launchCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("want %d launches, got %d", n, l.launchCount())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReconcileTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		sessions []Session
		tokens   []uint32
		want     int
	}{
		{"no_sessions", nil, nil, 0},
		{"one_active", []Session{{ID: 1, UserName: "alice"}}, []uint32{1}, 1},
		{"one_locked", []Session{{ID: 2, UserName: "bob"}}, []uint32{2}, 1},
		{"disconnected_with_user", []Session{{ID: 3, UserName: "carol"}}, []uint32{3}, 1},
		{"two_sessions", []Session{{ID: 1, UserName: "a"}, {ID: 2, UserName: "b"}}, []uint32{1, 2}, 2},
		{"session_0_only", []Session{{ID: 0, UserName: "system"}}, []uint32{0}, 0},
		{"empty_user", []Session{{ID: 4, UserName: ""}}, nil, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			events := make(chan service.Event, 8)
			launch := newFakeLauncher(tc.tokens...)
			src := &fixedSessions{list: tc.sessions}
			s := &Supervisor{
				Events:     events,
				Sessions:   src,
				Launcher:   launch,
				Sink:       &recordingSink{},
				Clock:      newMemClock(time.Now()),
				Log:        NopLogger{},
				StopBudget: time.Second,
			}
			cancel, _ := startSup(t, s)
			waitLaunch(t, launch, tc.want)
			if launch.launchCount() != tc.want {
				t.Fatalf("launches=%d want %d", launch.launchCount(), tc.want)
			}
			cancel()
		})
	}
}

func TestEventHandling(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 4)
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	sink := &recordingSink{}
	clock := newMemClock(time.Now())
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: sink,
		Clock: clock, Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)

	// Duplicate SessionLogon — still exactly one child.
	events <- service.Event{Kind: service.EventSessionLogon, SessionID: 1}
	events <- service.Event{Kind: service.EventSessionLogon, SessionID: 1}
	time.Sleep(50 * time.Millisecond)
	if launch.launchCount() != 1 {
		t.Fatalf("duplicate logon caused relaunch: %d", launch.launchCount())
	}

	// Logoff for unknown session — no panic.
	events <- service.Event{Kind: service.EventSessionLogoff, SessionID: 99}
	time.Sleep(20 * time.Millisecond)

	// Logon then immediate Logoff.
	src.set([]Session{{ID: 2, UserName: "b"}})
	launch.setToken(2, true)
	events <- service.Event{Kind: service.EventSessionLogon, SessionID: 2}
	waitLaunch(t, launch, 2)
	src.set(nil)
	events <- service.Event{Kind: service.EventSessionLogoff, SessionID: 2}
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		_, alive := s.children[2]
		s.mu.Unlock()
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child 2 still tracked after logoff")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Lock/Unlock storms — still converges, no extra launches for session 1
	// (session 1 was terminated when src became nil; restore).
	src.set([]Session{{ID: 1, UserName: "a"}})
	before := launch.launchCount()
	for i := 0; i < 20; i++ {
		events <- service.Event{Kind: service.EventSessionLock, SessionID: 1}
		events <- service.Event{Kind: service.EventSessionUnlock, SessionID: 1}
	}
	waitLaunch(t, launch, before+1)
	time.Sleep(30 * time.Millisecond)
	if launch.launchCount() != before+1 {
		t.Fatalf("lock storm launches=%d want %d", launch.launchCount(), before+1)
	}

	cancel()
}

func TestEventChannelFullStillConverges(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event) // unbuffered → Emit-style full drops; we send via select default
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: time.Second,
	}
	// Pre-start reconcile happens on Run; give it a buffered path by not blocking.
	// Use buffered events for start, then flood.
	eventsBuf := make(chan service.Event, 1)
	s.Events = eventsBuf
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)

	// Fill channel and drop extras (mirrors EmitEvent).
	service.EmitEvent(eventsBuf, service.Event{Kind: service.EventSessionLock, SessionID: 1})
	service.EmitEvent(eventsBuf, service.Event{Kind: service.EventSessionUnlock, SessionID: 1})
	src.set([]Session{{ID: 1, UserName: "a"}, {ID: 2, UserName: "b"}})
	launch.setToken(2, true)
	service.EmitEvent(eventsBuf, service.Event{Kind: service.EventSessionLogon, SessionID: 2})
	// Drain if needed then force reconcile via another event after reading.
	select {
	case <-eventsBuf:
	default:
	}
	eventsBuf <- service.Event{Kind: service.EventSessionLogon, SessionID: 2}
	waitLaunch(t, launch, 2)
	cancel()
}

func TestEventsAfterStopIgnored(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 8)
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: 200 * time.Millisecond,
	}
	cancel, errCh := startSup(t, s)
	waitLaunch(t, launch, 1)
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stop timeout")
	}
	n := launch.launchCount()
	events <- service.Event{Kind: service.EventSessionLogon, SessionID: 1}
	time.Sleep(50 * time.Millisecond)
	if launch.launchCount() != n {
		t.Fatalf("launch after stop: %d -> %d", n, launch.launchCount())
	}
}

func TestBackoffSequenceAndCap(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 4)
	clock := newMemClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	launch := newFakeLauncher(1)
	sink := &recordingSink{}
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: sink,
		Clock: clock, Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)

	// Crash loop: kill child repeatedly; backoff 1,2,4...
	var delays []time.Duration
	for i := 0; i < 10; i++ {
		ch := launch.lastChild()
		if ch == nil {
			t.Fatal("no child")
		}
		before := launch.launchCount()
		ch.kill()
		waitLaunch(t, launch, before+1)
		delays = append(delays, clock.lastAfter())
	}
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second,
		60 * time.Second, 60 * time.Second,
	}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delay[%d]=%v want %v (all=%v)", i, delays[i], want[i], delays)
		}
	}
	if sink.count() != 10 {
		t.Fatalf("restart facts=%d want 10", sink.count())
	}

	// 11th exit hits cap — no further launch.
	before := launch.launchCount()
	launch.lastChild().kill()
	time.Sleep(80 * time.Millisecond)
	if launch.launchCount() != before {
		t.Fatalf("expected cap; launches %d -> %d", before, launch.launchCount())
	}
	if sink.count() != 10 {
		t.Fatalf("facts must stay bounded at 10, got %d", sink.count())
	}

	// Logon clears cap and allows relaunch (Lock/Unlock must not — see dedicated test).
	events <- service.Event{Kind: service.EventSessionLogon, SessionID: 1}
	waitLaunch(t, launch, before+1)

	cancel()
}

func TestPowerResumeClearsRelaunchCap(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 4)
	clock := newMemClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: clock, Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)

	// Hit the relaunch cap.
	for i := 0; i < relaunchMaxInWindow; i++ {
		before := launch.launchCount()
		launch.lastChild().kill()
		waitLaunch(t, launch, before+1)
	}
	cappedAt := launch.launchCount()
	launch.lastChild().kill()
	time.Sleep(80 * time.Millisecond)
	if launch.launchCount() != cappedAt {
		t.Fatalf("expected capped; got %d want %d", launch.launchCount(), cappedAt)
	}

	// AC #5: after resume the agent must come back even when capped.
	events <- service.Event{Kind: service.EventPowerResume}
	waitLaunch(t, launch, cappedAt+1)
	cancel()
}

func TestLockUnlockDoesNotClearRelaunchCap(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 64)
	clock := newMemClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	launch := newFakeLauncher(1)
	sink := &recordingSink{}
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: sink,
		Clock: clock, Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)

	for i := 0; i < relaunchMaxInWindow; i++ {
		before := launch.launchCount()
		launch.lastChild().kill()
		waitLaunch(t, launch, before+1)
	}
	cappedAt := launch.launchCount()
	factsAtCap := sink.count()
	launch.lastChild().kill()
	time.Sleep(50 * time.Millisecond)

	// AC #9: lock/unlock storms must not re-arm another relaunch window.
	for i := 0; i < 30; i++ {
		events <- service.Event{Kind: service.EventSessionLock, SessionID: 1}
		events <- service.Event{Kind: service.EventSessionUnlock, SessionID: 1}
	}
	time.Sleep(100 * time.Millisecond)
	if launch.launchCount() != cappedAt {
		t.Fatalf("lock/unlock cleared cap: launches %d -> %d", cappedAt, launch.launchCount())
	}
	if sink.count() != factsAtCap {
		t.Fatalf("lock/unlock flooded restarted facts: %d -> %d", factsAtCap, sink.count())
	}
	cancel()
}

func TestBackoffResetAfterStable(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	clock := newMemClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: clock, Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)

	launch.lastChild().kill()
	waitLaunch(t, launch, 2)
	if clock.lastAfter() != time.Second {
		t.Fatalf("first backoff=%v", clock.lastAfter())
	}

	// Advance past stable window via reconcile tick.
	clock.Advance(backoffStableAfter)
	events <- service.Event{Kind: service.EventSessionLock, SessionID: 1}
	time.Sleep(30 * time.Millisecond)

	launch.lastChild().kill()
	waitLaunch(t, launch, 3)
	if clock.lastAfter() != time.Second {
		t.Fatalf("after stable reset backoff=%v want 1s", clock.lastAfter())
	}
	cancel()
}

func TestLauncherErrorRetriesWithoutDying(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	clock := newMemClock(time.Now())
	launch := newFakeLauncher(1)
	launch.err = fmt.Errorf("%w: ERROR_NO_TOKEN", ErrNoToken)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: clock, Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, errCh := startSup(t, s)
	waitLaunch(t, launch, 3) // several retries
	select {
	case err := <-errCh:
		t.Fatalf("supervisor exited: %v", err)
	default:
	}
	cancel()
}

func TestProcessIdentityIgnoresStalePID(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	sink := &recordingSink{}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: sink,
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)
	first := launch.lastChild()

	// Replace child (simulate re-launch) then signal exit on the OLD handle —
	// must not terminate/relaunch based on PID alone.
	second := newFakeChild(first.pid) // same PID, different handle
	s.mu.Lock()
	s.children[1].child = second
	s.mu.Unlock()
	first.kill()
	time.Sleep(50 * time.Millisecond)
	if sink.count() != 0 {
		t.Fatalf("stale handle exit must not emit restart, got %d", sink.count())
	}
	if launch.launchCount() != 1 {
		t.Fatalf("stale exit caused launch: %d", launch.launchCount())
	}
	cancel()
}

func TestStopAbortsInFlightLaunch(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	entered := make(chan struct{})
	release := make(chan struct{})
	var termCount atomic.Int32
	launch := newFakeLauncher(1)
	launch.launchFn = func(sessionID uint32) (Child, error) {
		close(entered)
		<-release
		ch := newFakeChild(4242)
		// Count Terminate so we know the post-stop child was reaped.
		return &countingTermChild{fakeChild: ch, n: &termCount}, nil
	}
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: 500 * time.Millisecond,
	}
	cancel, errCh := startSup(t, s)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Launch never started")
	}
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stop blocked on in-flight Launch")
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for termCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if termCount.Load() == 0 {
		t.Fatal("late Launch child was not terminated after stop")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.children) != 0 {
		t.Fatalf("children left after stop: %d", len(s.children))
	}
}

type countingTermChild struct {
	*fakeChild
	n *atomic.Int32
}

func (c *countingTermChild) Terminate() error {
	c.n.Add(1)
	return c.fakeChild.Terminate()
}

func TestStopDuringBackoff(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	clock := newMemClock(time.Now())
	clock.blockAfter = make(chan struct{})
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: clock, Log: NopLogger{}, StopBudget: 200 * time.Millisecond,
	}
	cancel, errCh := startSup(t, s)
	waitLaunch(t, launch, 1)
	launch.lastChild().kill()
	// Supervisor is waiting on blocked After for backoff.
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("did not stop during backoff")
	}
	close(clock.blockAfter) // unblock stray goroutine
}

func TestStopWithUnresponsiveChildHonoursBudget(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	launch := newFakeLauncher(1)
	launch.hangTerm = 5 * time.Second
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: 100 * time.Millisecond,
	}
	cancel, errCh := startSup(t, s)
	waitLaunch(t, launch, 1)
	start := time.Now()
	cancel()
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not return within budget")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stop took %v, want near StopBudget", elapsed)
	}
}

func TestTerminateFailurePath(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: time.Second,
	}
	// Child whose Terminate is no-op but Wait never completes until Close path timeout.
	var termFails atomic.Bool
	launch.launchFn = func(sessionID uint32) (Child, error) {
		ch := newFakeChild(42)
		if termFails.Load() {
			return &termFailChild{fakeChild: ch}, nil
		}
		return ch, nil
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)
	src.set(nil)
	events <- service.Event{Kind: service.EventSessionLogoff, SessionID: 1}
	time.Sleep(100 * time.Millisecond)
	cancel()
}

type termFailChild struct {
	*fakeChild
}

func (c *termFailChild) Terminate() error {
	return errors.New("access denied")
}

func TestPowerResumeCallsHookAndReconciles(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 2)
	launch := newFakeLauncher(1)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}}}
	var resumes atomic.Int32
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: time.Second,
		OnPowerResume: func() { resumes.Add(1) },
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 1)
	// Kill without waiting for relaunch; resume should reconcile.
	s.mu.Lock()
	if t := s.children[1]; t != nil && t.child != nil {
		t.backoff.expectRestart = false
		_ = t.child.Close()
		t.child.(*fakeChild).kill()
		t.child = nil
	}
	s.mu.Unlock()
	events <- service.Event{Kind: service.EventPowerResume}
	waitLaunch(t, launch, 2)
	if resumes.Load() != 1 {
		t.Fatalf("OnPowerResume calls=%d", resumes.Load())
	}
	cancel()
}

func TestFastUserSwitchingTwoSessions(t *testing.T) {
	t.Parallel()
	events := make(chan service.Event, 4)
	launch := newFakeLauncher(1, 2)
	src := &fixedSessions{list: []Session{{ID: 1, UserName: "a"}, {ID: 2, UserName: "b"}}}
	s := &Supervisor{
		Events: events, Sessions: src, Launcher: launch, Sink: &recordingSink{},
		Clock: newMemClock(time.Now()), Log: NopLogger{}, StopBudget: time.Second,
	}
	cancel, _ := startSup(t, s)
	waitLaunch(t, launch, 2)

	// Log off session 1 only.
	src.set([]Session{{ID: 2, UserName: "b"}})
	events <- service.Event{Kind: service.EventSessionLogoff, SessionID: 1}
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		_, a := s.children[1]
		_, b := s.children[2]
		s.mu.Unlock()
		if !a && b {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want only session 2; a=%v b=%v", a, b)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if launch.launchCount() != 2 {
		t.Fatalf("unexpected relaunch: %d", launch.launchCount())
	}
	cancel()
}
