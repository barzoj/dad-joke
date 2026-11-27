package updater

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Asset names published by scripts/package-release.ps1.
const (
	AssetEXE    = "dad-joke-windows-amd64.exe"
	AssetSHA256 = "dad-joke-windows-amd64.exe.sha256"
)

// Limits for network I/O (security / DoS bounds).
const (
	MaxReleaseJSONBytes int64 = 1 << 20   // 1 MiB
	MaxAssetBytes       int64 = 100 << 20 // 100 MiB
	MaxHTTPRedirects          = 10
	DefaultHTTPTimeout        = 60 * time.Second
	DefaultHealthWait         = 5 * time.Second
	DefaultStopTimeout        = 60 * time.Second
	DefaultStartTimeout       = 60 * time.Second
	// DefaultUpdateInFlightTTL clears a sticky updateInFlight flag if the
	// detached helper dies without stopping the service (stop+start+health budget).
	DefaultUpdateInFlightTTL = 3 * time.Minute
)

// Logger is a minimal structured-enough log sink.
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
	_, _ = io.WriteString(l.W, fmt.Sprintf(format, args...)+"\n")
}

// FileStore isolates filesystem mutations for unit tests.
type FileStore interface {
	MkdirAll(path string) error
	WriteFile(path string, data []byte) error
	ReadFile(path string) ([]byte, error)
	RemoveAll(path string) error
	Rename(oldpath, newpath string) error
	CopyFile(src, dst string) error
	Exists(path string) (bool, error)
	ApplyBinaryACL(path string) error
	ApplyDataACL(path string) error
}

// ServiceControl isolates SCM operations for apply-update / rollback.
type ServiceControl interface {
	Stop(name string) error
	Start(name string) error
	Wait(name string, state ServiceState, timeout time.Duration) error
	Query(name string) (ServiceState, error)
}

// ServiceState mirrors installer states without importing installer (keep packages cohesive).
type ServiceState string

// Service states used by apply-update health / wait.
const (
	StateStopped ServiceState = "Stopped"
	StateRunning ServiceState = "Running"
)

// Spawner launches a detached helper process (apply-update).
type Spawner interface {
	// StartDetached launches name with args in a new process group / detached
	// so it outlives the service process. It must return promptly.
	StartDetached(ctx context.Context, name string, args ...string) error
}

// Clock abstracts sleep for tests.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// Sleep implements Clock.
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// ReleaseMeta is the parsed GitHub Releases "latest" payload we care about.
type ReleaseMeta struct {
	TagName string
	Assets  []AssetMeta
}

// AssetMeta is one release asset.
type AssetMeta struct {
	Name               string
	BrowserDownloadURL string
}

// ReleaseClient fetches release metadata and downloads assets.
type ReleaseClient interface {
	FetchLatest(ctx context.Context, apiURL, agentVersion string) (ReleaseMeta, error)
	Download(ctx context.Context, downloadURL, agentVersion string, maxBytes int64) ([]byte, error)
}
