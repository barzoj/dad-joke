package sessionagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/ipc"
	"github.com/barzoj/dad-joke/internal/service"
	"github.com/barzoj/dad-joke/internal/winsess"
)

// RealClock uses the wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NopLogger discards logs.
type NopLogger struct{}

// Log implements Logger.
func (NopLogger) Log(string, ...any) {}

// WriterLogger logs to an io.Writer.
type WriterLogger struct{ W io.Writer }

// Log implements Logger.
func (l WriterLogger) Log(format string, args ...any) {
	if l.W == nil {
		return
	}
	_, _ = fmt.Fprintf(l.W, format+"\n", args...)
}

// NopRestartSink discards restart facts.
type NopRestartSink struct{}

// EnqueueRestarted implements RestartSink.
func (NopRestartSink) EnqueueRestarted(uint32) error { return nil }

// winsessSource adapts winsess.Source to SessionSource.
type winsessSource struct{ src winsess.Source }

func (w winsessSource) Sessions(ctx context.Context) ([]Session, error) {
	ss, err := w.src.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(ss))
	for _, s := range ss {
		if s.ID == 0 {
			continue
		}
		if s.UserName == "" && s.UserSID == "" {
			continue
		}
		out = append(out, Session{ID: s.ID, UserName: s.UserName, UserSID: s.UserSID})
	}
	return out, nil
}

// tracked holds one supervised child and its backoff state.
type tracked struct {
	child       Child
	backoff     sessionBackoff
	watchCancel context.CancelFunc
	launching   bool          // true while finishLaunch runs
	launchDone  chan struct{} // closed when finishLaunch returns
	ipcServer   *ipc.SessionServer
	ipcCancel   context.CancelFunc
	userSID     string
}

// Supervisor reconciles one session agent per interactive user session.
type Supervisor struct {
	Events   <-chan service.Event
	Sessions SessionSource
	Launcher Launcher
	Sink     RestartSink
	Clock    Clock
	Log      Logger
	// OnPowerResume is called on PBT_APMRESUMEAUTOMATIC (e.g. sampler.Reset).
	OnPowerResume func()
	// StopBudget bounds terminate waits on service stop (default 40s).
	StopBudget time.Duration
	// IntervalSeconds is sent in hello_ack (sample interval). Default 60.
	IntervalSeconds int
	// IPCListen overrides the named-pipe listener (tests use MemHub).
	IPCListen ipc.ListenFunc
	// IPCHandler receives post-handshake messages (default NopHandler).
	IPCHandler ipc.Handler
	// IPCStats is shared IPC counters (tests / diagnostics).
	IPCStats *ipc.Stats

	mu       sync.Mutex
	children map[uint32]*tracked
	stopped  bool
	runCtx   context.Context // set for the lifetime of Run
}

// New returns a production supervisor wired to winsess and the installed binary.
func New(events <-chan service.Event, sink RestartSink) *Supervisor {
	paths := installer.DefaultPaths()
	interval := config.DefaultSampleIntervalSeconds
	if cfg, err := config.LoadFile(paths.ConfigPath); err == nil {
		interval = cfg.SampleIntervalSeconds
	}
	return &Supervisor{
		Events:          events,
		Sessions:        winsessSource{src: winsess.Default()},
		Launcher:        winsess.DefaultLauncher(paths.BinaryPath),
		Sink:            sink,
		Clock:           RealClock{},
		Log:             WriterLogger{W: os.Stderr},
		StopBudget:      40 * time.Second,
		IntervalSeconds: interval,
		IPCStats:        &ipc.Stats{},
		children:        make(map[uint32]*tracked),
	}
}

// Connected reports whether sessionID has a post-handshake IPC connection.
func (s *Supervisor) Connected(sessionID uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.children[sessionID]
	if t == nil || t.ipcServer == nil {
		return false
	}
	return t.ipcServer.Connected()
}

// DropIPC closes the active IPC connection for sessionID without killing the child.
func (s *Supervisor) DropIPC(sessionID uint32) {
	s.mu.Lock()
	t := s.children[sessionID]
	var srv *ipc.SessionServer
	if t != nil {
		srv = t.ipcServer
	}
	s.mu.Unlock()
	if srv != nil {
		srv.Drop()
	}
}

// Stats returns IPC counters (may be nil before Run).
func (s *Supervisor) Stats() *ipc.Stats {
	return s.IPCStats
}

// Run implements service.Agent.
func (s *Supervisor) Run(ctx context.Context) error {
	if s.Log == nil {
		s.Log = NopLogger{}
	}
	if s.Clock == nil {
		s.Clock = RealClock{}
	}
	if s.Sink == nil {
		s.Sink = NopRestartSink{}
	}
	if s.Sessions == nil {
		s.Sessions = winsessSource{src: winsess.Default()}
	}
	if s.Launcher == nil {
		s.Launcher = winsess.DefaultLauncher(installer.DefaultPaths().BinaryPath)
	}
	if s.children == nil {
		s.children = make(map[uint32]*tracked)
	}
	if s.StopBudget <= 0 {
		s.StopBudget = 40 * time.Second
	}
	if s.IntervalSeconds <= 0 {
		s.IntervalSeconds = config.DefaultSampleIntervalSeconds
	}
	if s.IPCStats == nil {
		s.IPCStats = &ipc.Stats{}
	}
	if s.IPCHandler == nil {
		s.IPCHandler = ipc.NopHandler{}
	}
	s.runCtx = ctx

	s.Log.Log("sessionagent: supervisor starting")
	s.reconcile(ctx)
	go s.runDiagLoop(ctx)

	events := s.Events
	for {
		select {
		case <-ctx.Done():
			s.shutdown(ctx)
			s.Log.Log("sessionagent: supervisor stopping")
			return ctx.Err()
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			s.handleEvent(ctx, ev)
		}
	}
}

// Cap-reset policy (AC #5 / #9):
//   - Clears relaunch cap: PowerResume (all sessions), SessionLogon,
//     ConsoleConnect, RemoteConnect.
//   - Does NOT clear cap: SessionLock, SessionUnlock, ConsoleDisconnect,
//     RemoteDisconnect (crash-storm guard).
func (s *Supervisor) handleEvent(ctx context.Context, ev service.Event) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	switch ev.Kind {
	case service.EventPowerResume:
		if s.OnPowerResume != nil {
			s.OnPowerResume()
		}
		s.clearAllRelaunchCaps()
		s.reconcile(ctx)
	case service.EventSessionLogoff:
		s.terminateSession(ev.SessionID)
		s.reconcile(ctx)
	case service.EventSessionLogon, service.EventConsoleConnect, service.EventRemoteConnect:
		if ev.SessionID != 0 {
			s.clearRelaunchCap(ev.SessionID)
		}
		s.reconcile(ctx)
	default:
		// Lock/Unlock/Disconnect: level-triggered reconcile only — keep cap.
		s.reconcile(ctx)
	}
}

func (s *Supervisor) clearRelaunchCap(sessionID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.children[sessionID]; t != nil {
		t.backoff.clearCapOnEvent()
	}
}

func (s *Supervisor) clearAllRelaunchCaps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.children {
		if t != nil {
			t.backoff.clearCapOnEvent()
		}
	}
}

func (s *Supervisor) reconcile(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		return
	}
	sessions, err := s.Sessions.Sessions(ctx)
	if err != nil {
		s.Log.Log("sessionagent: enumerate sessions: %v", err)
		return
	}

	desired := make(map[uint32]Session, len(sessions))
	for _, sess := range sessions {
		if sess.ID == 0 {
			continue
		}
		if sess.UserName == "" && !s.Launcher.HasUserToken(sess.ID) {
			continue
		}
		if !s.Launcher.HasUserToken(sess.ID) {
			continue
		}
		desired[sess.ID] = sess
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}

	// Terminate children for sessions that are gone / have no user.
	for id, t := range s.children {
		if _, ok := desired[id]; ok {
			continue
		}
		s.stopTrackedLocked(id, t)
	}

	now := s.Clock.Now()
	for id, sess := range desired {
		t := s.children[id]
		if t != nil && t.child != nil {
			t.backoff.onStableTick(now)
			s.ensureIPCLocked(ctx, id, t, sess)
			continue
		}
		if t != nil && t.launching {
			continue
		}
		if t == nil {
			t = &tracked{}
			s.children[id] = t
		}
		if t.backoff.capped {
			if !t.backoff.capLogged {
				s.Log.Log("sessionagent: session %d relaunch cap reached; waiting for next session event", id)
				t.backoff.capLogged = true
			}
			continue
		}
		if !t.backoff.pendingUntil.IsZero() && now.Before(t.backoff.pendingUntil) {
			continue
		}
		s.ensureIPCLocked(ctx, id, t, sess)
		s.launchLocked(ctx, id, t)
	}
}

// ensureIPCLocked starts the per-session IPC server if needed. Caller holds s.mu.
func (s *Supervisor) ensureIPCLocked(ctx context.Context, sessionID uint32, t *tracked, sess Session) {
	if t.ipcServer != nil {
		return
	}
	sid := sess.UserSID
	if sid == "" {
		sid = lookupSessionUserSID(sessionID)
	}
	if sid == "" {
		s.Log.Log("sessionagent: session %d: no user SID for IPC pipe ACL", sessionID)
		return
	}
	t.userSID = sid
	srv := &ipc.SessionServer{
		SessionID:       sessionID,
		UserSID:         sid,
		IntervalSeconds: s.IntervalSeconds,
		Handler:         s.IPCHandler,
		Listen:          s.IPCListen,
		Log:             s.Log.Log,
		Stats:           s.IPCStats,
	}
	if t.child != nil {
		srv.ExpectedPID = t.child.PID()
	}
	ipcCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.ipcServer = srv
	t.ipcCancel = cancel
	go func() {
		_ = srv.Run(ipcCtx)
	}()
}

func lookupSessionUserSID(sessionID uint32) string {
	tok, err := winsess.QueryUserToken(sessionID)
	if err != nil {
		return ""
	}
	defer func() {
		if tok != 0 {
			_ = closeToken(tok)
		}
	}()
	sid, err := winsess.TokenUserSIDString(tok)
	if err != nil {
		return ""
	}
	return sid
}

// launchLocked starts a session agent asynchronously. Caller must hold s.mu.
// Launch runs on a goroutine; shutdown joins launchDone (≤ StopBudget) so
// CreateProcessAsUser cannot outlive SCM stop without a join attempt.
func (s *Supervisor) launchLocked(ctx context.Context, sessionID uint32, t *tracked) {
	if s.stopped || t.launching || ctx.Err() != nil {
		return
	}
	t.launching = true
	done := make(chan struct{})
	t.launchDone = done
	go s.finishLaunch(ctx, sessionID, t, done)
}

func (s *Supervisor) finishLaunch(ctx context.Context, sessionID uint32, t *tracked, done chan struct{}) {
	defer func() {
		s.mu.Lock()
		t.launching = false
		if t.launchDone == done {
			t.launchDone = nil
		}
		s.mu.Unlock()
		close(done)
	}()

	child, err := s.Launcher.Launch(ctx, sessionID)
	detach := context.WithoutCancel(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		s.reapChildLocked(detach, child)
		return
	}
	if err != nil {
		s.Log.Log("sessionagent: launch session %d: %v", sessionID, err)
		delay, ok := t.backoff.allowRelaunch(s.Clock.Now())
		if ok {
			s.scheduleRetryLocked(ctx, sessionID, delay)
		} else if t.backoff.capped && !t.backoff.capLogged {
			s.Log.Log("sessionagent: session %d relaunch cap reached; waiting for next session event", sessionID)
			t.backoff.capLogged = true
		}
		return
	}
	if s.children[sessionID] != t {
		s.reapChildLocked(detach, child)
		return
	}
	t.child = child
	t.backoff.onLaunched(s.Clock.Now())
	if t.ipcServer != nil {
		t.ipcServer.SetExpectedPID(child.PID())
	}
	watchCtx, cancel := context.WithCancel(detach)
	t.watchCancel = cancel
	go s.watchChild(watchCtx, sessionID, child)
}

// reapChildLocked terminates, waits briefly, and closes child. Caller holds s.mu.
func (s *Supervisor) reapChildLocked(detach context.Context, child Child) {
	if child == nil {
		return
	}
	_ = child.Terminate()
	waitCtx, cancel := context.WithTimeout(detach, 2*time.Second)
	_ = child.Wait(waitCtx)
	cancel()
	_ = child.Close()
}

func (s *Supervisor) scheduleRetryLocked(ctx context.Context, sessionID uint32, delay time.Duration) {
	go func() {
		timer := s.Clock.After(delay)
		select {
		case <-ctx.Done():
			return
		case <-timer:
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.stopped {
				return
			}
			t := s.children[sessionID]
			if t == nil || t.child != nil || t.launching {
				return
			}
			s.launchLocked(ctx, sessionID, t)
		}
	}()
}

func (s *Supervisor) watchChild(ctx context.Context, sessionID uint32, child Child) {
	_ = child.Wait(ctx)
	s.onChildExit(sessionID, child)
}

func (s *Supervisor) onChildExit(sessionID uint32, child Child) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		_ = child.Close()
		return
	}
	t := s.children[sessionID]
	if t == nil || t.child != child {
		// Stale watcher (PID reuse / replaced child) — ignore.
		_ = child.Close()
		return
	}
	emitRestart := t.backoff.expectRestart
	t.child = nil
	if t.ipcServer != nil {
		t.ipcServer.SetExpectedPID(0)
		t.ipcServer.Drop() // close any connection from the dead child
	}
	if t.watchCancel != nil {
		t.watchCancel()
		t.watchCancel = nil
	}
	_ = child.Close()

	if !emitRestart {
		return
	}

	now := s.Clock.Now()
	delay, ok := t.backoff.allowRelaunch(now)
	if !ok {
		if t.backoff.capped && !t.backoff.capLogged {
			s.Log.Log("sessionagent: session %d relaunch cap reached; waiting for next session event", sessionID)
			t.backoff.capLogged = true
		}
		return
	}
	if err := s.Sink.EnqueueRestarted(sessionID); err != nil {
		s.Log.Log("sessionagent: enqueue session.restarted: %v", err)
	}
	runCtx := s.runCtx
	if runCtx == nil || runCtx.Err() != nil {
		return
	}
	go func() {
		timer := s.Clock.After(delay)
		select {
		case <-runCtx.Done():
			return
		case <-timer:
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stopped {
			return
		}
		t := s.children[sessionID]
		if t == nil || t.child != nil || t.launching {
			return
		}
		s.launchLocked(runCtx, sessionID, t)
	}()
}

func (s *Supervisor) terminateSession(sessionID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.children[sessionID]
	if t == nil {
		return
	}
	s.stopTrackedLocked(sessionID, t)
}

func (s *Supervisor) stopTrackedLocked(sessionID uint32, t *tracked) {
	t.backoff.expectRestart = false
	if t.watchCancel != nil {
		t.watchCancel()
		t.watchCancel = nil
	}
	if t.ipcCancel != nil {
		t.ipcCancel()
		t.ipcCancel = nil
		t.ipcServer = nil
	}
	if t.child != nil {
		detach := context.Background()
		if s.runCtx != nil {
			detach = context.WithoutCancel(s.runCtx)
		}
		s.reapChildLocked(detach, t.child)
		t.child = nil
	}
	delete(s.children, sessionID)
}

func (s *Supervisor) shutdown(parent context.Context) {
	s.mu.Lock()
	s.stopped = true
	budget := s.StopBudget
	type snap struct {
		t    *tracked
		done <-chan struct{}
	}
	snaps := make([]snap, 0, len(s.children))
	for id, t := range s.children {
		t.backoff.expectRestart = false
		if t.ipcCancel != nil {
			t.ipcCancel()
			t.ipcCancel = nil
			t.ipcServer = nil
		}
		snaps = append(snaps, snap{t: t, done: t.launchDone})
		delete(s.children, id)
	}
	s.mu.Unlock()

	deadline := s.Clock.Now().Add(budget)
	if dl, ok := parent.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	detach := context.WithoutCancel(parent)

	// Join in-flight Launch/CreateProcessAsUser so late children are reaped
	// before Run returns (bounded by StopBudget).
	for _, sn := range snaps {
		if sn.done == nil {
			continue
		}
		remain := deadline.Sub(s.Clock.Now())
		if remain < 0 {
			remain = 0
		}
		timer := time.NewTimer(remain)
		select {
		case <-sn.done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			// Launch wedged past budget; finishLaunch still reaps when it returns.
		}
	}

	for _, sn := range snaps {
		t := sn.t
		if t.watchCancel != nil {
			t.watchCancel()
		}
		if t.child == nil {
			continue
		}
		_ = t.child.Terminate()
	}
	for _, sn := range snaps {
		t := sn.t
		if t.child == nil {
			continue
		}
		remain := deadline.Sub(s.Clock.Now())
		if remain < 0 {
			remain = 0
		}
		waitCtx, cancel := context.WithTimeout(detach, remain)
		_ = t.child.Wait(waitCtx)
		cancel()
		_ = t.child.Close()
	}
}

// ErrNoToken is a sentinel launchers may wrap when WTSQueryUserToken fails.
var ErrNoToken = errors.New("sessionagent: no user token")
