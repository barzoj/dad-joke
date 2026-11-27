package heartbeat

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"time"
)

// Logger is a minimal log sink (mirrors updater.Logger; kept local to avoid coupling).
type Logger interface {
	Log(format string, args ...any)
}

// NopLogger discards logs.
type NopLogger struct{}

// Log implements Logger.
func (NopLogger) Log(string, ...any) {}

// WriterLogger logs to an io.Writer (typically stderr).
type WriterLogger struct {
	W io.Writer
}

// Log implements Logger.
func (l WriterLogger) Log(format string, args ...any) {
	if l.W == nil {
		return
	}
	_, _ = fmt.Fprintf(l.W, format+"\n", args...)
}

// Clock abstracts timers for tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// HostInfo reports hostname and OS version for heartbeat payloads.
type HostInfo interface {
	Hostname() (string, error)
	OSVersion() string
}

// DefaultHost uses os.Hostname and a stable GOOS/GOARCH label.
type DefaultHost struct{}

// Hostname implements HostInfo.
func (DefaultHost) Hostname() (string, error) {
	return os.Hostname()
}

// OSVersion implements HostInfo.
func (DefaultHost) OSVersion() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
