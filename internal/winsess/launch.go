package winsess

import "context"

// Child is a process handle owned by the supervisor (not a raw PID).
// Identity for terminate/wait is this handle; PIDs are observational only.
type Child interface {
	// PID returns the OS process id at creation time (observational).
	PID() uint32
	// Wait blocks until the process exits or ctx is cancelled.
	Wait(ctx context.Context) error
	// Terminate requests process termination via the owned handle.
	Terminate() error
	// Close releases the process handle. Safe to call after Wait/Terminate.
	Close() error
}

// Launcher starts and terminates session-agent processes in interactive sessions.
// Contract version: session-launcher/v1.
type Launcher interface {
	// Launch starts the installed binary with args ["session"] in sessionID.
	// Implementations must not launch into session 0. On Windows, orphans of a
	// previous service instance in the same session are terminated first so
	// Restart-Service re-adopts without duplicates.
	Launch(ctx context.Context, sessionID uint32) (Child, error)
	// HasUserToken reports whether WTSQueryUserToken would succeed for sessionID.
	HasUserToken(sessionID uint32) bool
}

// DefaultLauncher returns the platform Launcher (Windows CreateProcessAsUser;
// stub elsewhere).
func DefaultLauncher(exePath string) Launcher {
	return newPlatformLauncher(exePath)
}
