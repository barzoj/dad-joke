package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// SessionServer owns the listen→verify→handshake loop for one interactive session.
// Disconnect does not imply process restart (supervisor owns that). After a
// drop or client disconnect the server re-listens; a successful new handshake
// is the sole active connection (ReplacedConn increments when Drop cancels an
// in-flight serve before re-listen).
type SessionServer struct {
	SessionID       uint32
	UserSID         string
	ExpectedPID     uint32
	IntervalSeconds int
	Handler         Handler
	Listen          ListenFunc
	Log             LogFunc
	Clock           func() time.Time
	Stats           *Stats
	HelloTimeout    time.Duration

	mu         sync.Mutex
	connected  bool
	connGen    uint64
	cancelConn context.CancelFunc
}

// Connected reports whether a post-handshake connection is active.
func (s *SessionServer) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// SetExpectedPID updates the PID accepted after ConnectNamedPipe (after launch).
func (s *SessionServer) SetExpectedPID(pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ExpectedPID = pid
}

// Drop closes the active connection (e2e / diagnostics). Does not stop Run.
func (s *SessionServer) Drop() {
	s.mu.Lock()
	cancel := s.cancelConn
	if cancel != nil {
		s.Stats.ReplacedConn.Add(1)
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run listens and serves until ctx is cancelled.
func (s *SessionServer) Run(ctx context.Context) error {
	if s.Listen == nil {
		s.Listen = DefaultListen
	}
	if s.Log == nil {
		s.Log = func(string, ...any) {}
	}
	if s.Clock == nil {
		s.Clock = time.Now
	}
	if s.Stats == nil {
		s.Stats = &Stats{}
	}
	if s.Handler == nil {
		s.Handler = NopHandler{}
	}

	for {
		if err := ctx.Err(); err != nil {
			s.mu.Lock()
			s.connected = false
			if s.cancelConn != nil {
				s.cancelConn()
			}
			s.mu.Unlock()
			return err
		}
		if err := s.oneConnection(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			if errors.Is(err, errWaitPID) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			s.Log("ipc: session %d serve: %v", s.SessionID, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

var errWaitPID = errors.New("ipc: waiting for expected pid")

func (s *SessionServer) oneConnection(ctx context.Context) error {
	s.mu.Lock()
	pid := s.ExpectedPID
	sid := s.UserSID
	s.mu.Unlock()
	if pid == 0 {
		return errWaitPID
	}

	acc, err := s.Listen(ctx, s.SessionID, sid)
	if err != nil {
		s.Stats.Squatter.Add(1)
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = acc.Close() }()

	stream, client, err := acc.Accept(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	expected := ExpectedClient{PID: s.ExpectedPID, SessionID: s.SessionID}
	s.mu.Unlock()
	if !VerifyClient(client, expected) {
		s.Stats.RefusedClient.Add(1)
		s.Log("ipc: session %d refused client pid=%d sess=%d (want pid=%d sess=%d)",
			s.SessionID, client.PID, client.SessionID, expected.PID, expected.SessionID)
		_ = stream.Close()
		return fmt.Errorf("ipc: client verification failed")
	}

	connCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancelConn = cancel
	s.connGen++
	gen := s.connGen
	s.mu.Unlock()
	defer cancel()

	cfg := ServerConfig{
		SessionID:       s.SessionID,
		Expected:        expected,
		IntervalSeconds: s.IntervalSeconds,
		Handler:         s.Handler,
		Log:             s.Log,
		Clock:           s.Clock,
		Stats:           s.Stats,
		HelloTimeout:    s.HelloTimeout,
		OnHandshake: func() {
			s.mu.Lock()
			if s.connGen == gen {
				s.connected = true
			}
			s.mu.Unlock()
		},
	}

	err = ServeConn(connCtx, stream, cfg)
	s.mu.Lock()
	if s.connGen == gen {
		s.connected = false
		s.cancelConn = nil
	}
	s.mu.Unlock()
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
