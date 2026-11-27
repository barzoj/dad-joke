package updater

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/barzoj/dad-joke/internal/installer"
)

// ApplyOptions configures the apply-update helper.
type ApplyOptions struct {
	ServiceName   string
	BinaryPath    string // installed dad-joke.exe
	StagedExePath string // verified new binary
	DataDir       string // ProgramData\DadJoke (staging allowlist root)
	HealthWait    time.Duration
	StopTimeout   time.Duration
	StartTimeout  time.Duration
	// SkipPathCheck disables the LocalSystem path allowlist. Production CLI
	// never sets this; unit tests may inject arbitrary paths via the Applier interface.
	SkipPathCheck bool
}

// Applier performs stop → replace → start → health → rollback.
type Applier struct {
	Store   FileStore
	Service ServiceControl
	Clock   Clock
	Log     Logger
}

// Apply replaces the installed binary with the staged exe and restarts the
// service. On start/health failure it restores binary.old and restarts.
// Staging files are always cleaned up.
func (a *Applier) Apply(ctx context.Context, opts ApplyOptions) error {
	if a.Log == nil {
		a.Log = NopLogger{}
	}
	if a.Clock == nil {
		a.Clock = RealClock{}
	}
	if opts.ServiceName == "" {
		opts.ServiceName = "DadJoke"
	}
	if opts.HealthWait <= 0 {
		opts.HealthWait = DefaultHealthWait
	}
	if opts.StopTimeout <= 0 {
		opts.StopTimeout = DefaultStopTimeout
	}
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = DefaultStartTimeout
	}
	if opts.BinaryPath == "" {
		return fmt.Errorf("binary path required")
	}
	if opts.StagedExePath == "" {
		return fmt.Errorf("staged exe path required")
	}
	if !opts.SkipPathCheck {
		paths := defaultInstallPaths()
		dataDir := opts.DataDir
		if dataDir == "" {
			dataDir = paths.dataDir
		}
		if err := ValidateApplyPaths(opts.StagedExePath, opts.BinaryPath, dataDir, paths.binaryPath); err != nil {
			return fmt.Errorf("path allowlist: %w", err)
		}
	}

	oldPath := opts.BinaryPath + ".old"
	stagingDir, err := StagingDirFor(opts.StagedExePath)
	if err != nil {
		stagingDir = filepath.Dir(opts.StagedExePath)
	}

	cleanupStaging := func() {
		_ = a.Store.RemoveAll(stagingDir)
	}
	defer cleanupStaging()

	a.Log.Log("apply-update: stopping service %s", opts.ServiceName)
	if err := a.Service.Stop(opts.ServiceName); err != nil {
		return fmt.Errorf("stop service: %w", err)
	}
	if err := a.waitState(ctx, opts.ServiceName, StateStopped, opts.StopTimeout); err != nil {
		return fmt.Errorf("wait stopped: %w", err)
	}

	// Remove a stale .old from a previous attempt.
	if ok, _ := a.Store.Exists(oldPath); ok {
		_ = a.Store.RemoveAll(oldPath)
	}

	exists, err := a.Store.Exists(opts.BinaryPath)
	if err != nil {
		return fmt.Errorf("stat binary: %w", err)
	}
	if exists {
		a.Log.Log("apply-update: renaming %s -> %s", opts.BinaryPath, oldPath)
		if err := a.Store.Rename(opts.BinaryPath, oldPath); err != nil {
			return fmt.Errorf("rename to .old: %w", err)
		}
	}

	a.Log.Log("apply-update: installing staged binary")
	if err := a.Store.CopyFile(opts.StagedExePath, opts.BinaryPath); err != nil {
		// Best-effort restore before returning.
		if exists {
			_ = a.Store.Rename(oldPath, opts.BinaryPath)
		}
		return fmt.Errorf("install new binary: %w", err)
	}
	if err := a.Store.ApplyBinaryACL(opts.BinaryPath); err != nil {
		a.Log.Log("apply-update: ACL failed (%v); rolling back", err)
		if exists {
			_ = a.Store.RemoveAll(opts.BinaryPath)
			if renErr := a.Store.Rename(oldPath, opts.BinaryPath); renErr != nil {
				return fmt.Errorf("apply binary ACL: %v; restore .old also failed: %w", err, renErr)
			}
			_ = a.Store.ApplyBinaryACL(opts.BinaryPath)
		}
		// Leave service stopped; caller / recovery can restart previous image.
		if startErr := a.Service.Start(opts.ServiceName); startErr != nil {
			return fmt.Errorf("apply binary ACL: %v; restart previous also failed: %w", err, startErr)
		}
		if waitErr := a.waitState(ctx, opts.ServiceName, StateRunning, opts.StartTimeout); waitErr != nil {
			return fmt.Errorf("apply binary ACL: %v; wait previous running: %w", err, waitErr)
		}
		return fmt.Errorf("apply binary ACL: %w", err)
	}

	if err := a.startAndHealth(ctx, opts); err != nil {
		a.Log.Log("apply-update: health failed (%v); rolling back", err)
		if rbErr := a.rollback(ctx, opts, oldPath); rbErr != nil {
			return fmt.Errorf("health failed (%v); rollback also failed: %w", err, rbErr)
		}
		return fmt.Errorf("update rolled back after health failure: %w", err)
	}

	// Retain .old after success for diagnostics / emergency rollback.
	a.Log.Log("apply-update: success")
	return nil
}

type installPaths struct {
	dataDir    string
	binaryPath string
}

func defaultInstallPaths() installPaths {
	p := installer.DefaultPaths()
	return installPaths{dataDir: p.DataDir, binaryPath: p.BinaryPath}
}

func (a *Applier) startAndHealth(ctx context.Context, opts ApplyOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.Log.Log("apply-update: starting service %s", opts.ServiceName)
	if err := a.Service.Start(opts.ServiceName); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	if err := a.waitState(ctx, opts.ServiceName, StateRunning, opts.StartTimeout); err != nil {
		return fmt.Errorf("wait running: %w", err)
	}

	// Documented health window: service must remain Running.
	timer := a.Clock.After(opts.HealthWait)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer:
	}
	st, err := a.Service.Query(opts.ServiceName)
	if err != nil {
		return fmt.Errorf("query after health wait: %w", err)
	}
	if st != StateRunning {
		return fmt.Errorf("service state after health wait: %s (want Running)", st)
	}
	return nil
}

func (a *Applier) rollback(ctx context.Context, opts ApplyOptions, oldPath string) error {
	_ = a.Service.Stop(opts.ServiceName)
	_ = a.waitState(ctx, opts.ServiceName, StateStopped, opts.StopTimeout)

	if ok, _ := a.Store.Exists(opts.BinaryPath); ok {
		_ = a.Store.RemoveAll(opts.BinaryPath)
	}
	ok, err := a.Store.Exists(oldPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("rollback: %s missing", oldPath)
	}
	if err := a.Store.Rename(oldPath, opts.BinaryPath); err != nil {
		return fmt.Errorf("rollback rename: %w", err)
	}
	_ = a.Store.ApplyBinaryACL(opts.BinaryPath)

	if err := a.Service.Start(opts.ServiceName); err != nil {
		return fmt.Errorf("rollback start: %w", err)
	}
	if err := a.waitState(ctx, opts.ServiceName, StateRunning, opts.StartTimeout); err != nil {
		return fmt.Errorf("rollback wait running: %w", err)
	}
	a.Log.Log("apply-update: rollback complete; previous binary restored")
	return nil
}

func (a *Applier) waitState(ctx context.Context, name string, state ServiceState, timeout time.Duration) error {
	deadline := a.Clock.Now().Add(timeout)
	var last ServiceState
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		st, err := a.Service.Query(name)
		if err == nil && st == state {
			return nil
		}
		if err == nil {
			last = st
		}
		if !a.Clock.Now().Before(deadline) {
			if err != nil {
				return fmt.Errorf("timeout waiting for %s: %w", state, err)
			}
			return fmt.Errorf("timeout waiting for %s (last=%s)", state, last)
		}
		// Allow implementations to block briefly; ignore timeout errors and re-query.
		_ = a.Service.Wait(name, state, 200*time.Millisecond)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.Clock.After(50 * time.Millisecond):
		}
	}
}

// TriggerOptions configures spawning the detached apply-update helper.
type TriggerOptions struct {
	HelperExe     string // usually os.Executable()
	StagedExePath string
	BinaryPath    string
	ServiceName   string
	HealthWait    time.Duration
}

// TriggerApply starts a detached `apply-update` helper and returns immediately.
func TriggerApply(ctx context.Context, spawner Spawner, opts TriggerOptions) error {
	if opts.HelperExe == "" {
		return fmt.Errorf("helper exe required")
	}
	if opts.StagedExePath == "" {
		return fmt.Errorf("staged path required")
	}
	args := []string{
		"apply-update",
		"--staged", opts.StagedExePath,
	}
	if opts.BinaryPath != "" {
		args = append(args, "--binary", opts.BinaryPath)
	}
	if opts.ServiceName != "" {
		args = append(args, "--service", opts.ServiceName)
	}
	if opts.HealthWait > 0 {
		args = append(args, "--health-seconds", fmt.Sprintf("%d", int(opts.HealthWait.Seconds())))
	}
	return spawner.StartDetached(ctx, opts.HelperExe, args...)
}
