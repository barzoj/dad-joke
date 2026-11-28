// Package sessionagent implements the session-agent process and its service
// supervisor (T-21) plus versioned IPC to the service (T-22).
//
// # Contract session-agent/v1
//
// Two roles share one binary (`dad-joke.exe`):
//
//  1. Session agent (`dad-joke.exe session`): runs in an interactive user
//     session. Takes no secrets, no agent token, no backend URL, no network.
//     Does not read config.json or ProgramData. Connects to the service over
//     the versioned named-pipe IPC (`internal/ipc`, ProtocolVersion=1) and
//     blocks until context cancel.
//
//  2. Supervisor (`Supervisor`, a service.Agent): runs in the Windows Service
//     (Session 0, LocalSystem). Desired state = exactly one session agent per
//     interactive session that has a user token. Reconcile is level-triggered
//     on SCM events (service.Event) and child exits. Child identity is the
//     process handle the service created — never a remembered PID.
//     Relaunch-cap reset events: PowerResume, SessionLogon, ConsoleConnect,
//     RemoteConnect. Lock/Unlock/Disconnect do not clear the cap.
//     Per session the supervisor listens on `\\.\pipe\DadJoke.session.<id>`,
//     verifies the client PID/session, and exposes Connected(sessionID).
//     IPC disconnect does not restart the child; process exit does.
//
// OS calls stay behind winsess.Launcher and SessionSource. Fact markers
// (`session.restarted`) go through RestartSink. IPC framing/handshake live in
// `internal/ipc`.
package sessionagent

import (
	"context"
	"time"

	"github.com/barzoj/dad-joke/internal/winsess"
)

// Session is one interactive logon the supervisor may host an agent in.
type Session struct {
	ID       uint32
	UserName string
	UserSID  string
}

// SessionSource enumerates interactive sessions (excludes session 0).
type SessionSource interface {
	Sessions(ctx context.Context) ([]Session, error)
}

// Launcher starts and owns session-agent child handles.
type Launcher = winsess.Launcher

// Child is a supervised process handle.
type Child = winsess.Child

// Clock abstracts time for backoff tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RestartSink records a session.restarted fact for sessionID (dur_s=0).
type RestartSink interface {
	EnqueueRestarted(sessionID uint32) error
}

// Logger is a minimal log sink.
type Logger interface {
	Log(format string, args ...any)
}
