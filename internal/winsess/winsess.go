// Package winsess enumerates interactive Windows Terminal Services sessions
// and the processes running in them. OS calls stay behind Source so unit tests
// run on Linux with fakes.
package winsess

import "context"

// State is an interactive session state reported in session.state facts.
type State string

const (
	StateActive       State = "active"
	StateLocked       State = "locked"
	StateDisconnected State = "disconnected"
)

// Session is one interactive logon session (services session 0 is omitted).
type Session struct {
	ID       uint32
	UserName string
	UserSID  string
	State    State
}

// Process is a process snapshot entry with its session and owner SID.
type Process struct {
	SessionID uint32
	PID       uint32
	Exe       string // basename (path stripped)
	OwnerSID  string
}

// Source enumerates sessions and processes. Implementations must be safe for
// concurrent use from the sampler loop.
type Source interface {
	Sessions(ctx context.Context) ([]Session, error)
	Processes(ctx context.Context) ([]Process, error)
}

// Default returns the platform Source (Windows WTS; stub elsewhere).
func Default() Source {
	return platformSource{}
}
