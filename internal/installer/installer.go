// Package installer installs and uninstalls the DadJoke Windows Service with
// tamper-resistant ACLs. Windows OS calls are isolated behind Host so core
// orchestration is unit-tested without the OS (see ARCHITECTURE.md).
package installer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
)

// Sentinel errors for the install/uninstall contract.
var (
	ErrNotElevated    = errors.New("administrator privileges required; re-run from an elevated prompt")
	ErrUnsupported    = errors.New("install/uninstall is only supported on Windows")
	ErrNotImplemented = errors.New("install/uninstall not implemented yet (see issue #2)")
)

// Installer is the versioned contract for service install and uninstall.
// Implementations must be idempotent where Issue #2 acceptance criteria require it.
type Installer interface {
	Install(ctx context.Context, opts InstallOptions) (InstallOutcome, error)
	Uninstall(ctx context.Context) error
}

// Options configures a Service installer.
type Options struct {
	Paths           Paths
	Host            Host
	StartTimeout    time.Duration
	StopTimeout     time.Duration
	RequireElevated bool
}

// Service implements Installer by reconciling desired install state via Host.
type Service struct {
	paths           Paths
	host            Host
	startTimeout    time.Duration
	stopTimeout     time.Duration
	requireElevated bool
}

// New returns the platform default Installer (Windows implementation or unsupported stub).
func New() Installer {
	return newPlatformInstaller()
}

// NewService builds an Installer with explicit dependencies (tests inject a mock Host).
func NewService(opts Options) *Service {
	if opts.Paths.InstallDir == "" {
		opts.Paths = DefaultPaths()
	}
	if opts.StartTimeout == 0 {
		opts.StartTimeout = 60 * time.Second
	}
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 60 * time.Second
	}
	return &Service{
		paths:           opts.Paths,
		host:            opts.Host,
		startTimeout:    opts.StartTimeout,
		stopTimeout:     opts.StopTimeout,
		requireElevated: opts.RequireElevated,
	}
}

// Install reconciles the desired installed state (idempotent).
func (s *Service) Install(ctx context.Context, opts InstallOptions) (InstallOutcome, error) {
	var empty InstallOutcome
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s.host == nil {
		return empty, fmt.Errorf("installer host is nil")
	}
	if s.requireElevated {
		ok, err := s.host.IsElevated()
		if err != nil {
			return empty, fmt.Errorf("check elevation: %w", err)
		}
		if !ok {
			return empty, ErrNotElevated
		}
	}
	if opts.TokenSet {
		if err := config.ValidateTokenArg(opts.Token); err != nil {
			return empty, err
		}
	}
	if opts.BackendURLSet {
		if err := config.ValidateBackendURL(opts.BackendURL); err != nil {
			return empty, err
		}
	}
	if opts.HeartbeatIntervalSet && opts.HeartbeatIntervalSeconds < 1 {
		return empty, fmt.Errorf("%w: heartbeat_interval_seconds must be >= 1 (got %d)", config.ErrInvalid, opts.HeartbeatIntervalSeconds)
	}

	s.host.Log("install: begin")

	src, err := s.host.ExecutablePath()
	if err != nil {
		return empty, fmt.Errorf("resolve executable: %w", err)
	}

	// Snapshot what already exists so rollback only undoes this run's creations.
	installExisted, err := s.host.FileExists(s.paths.InstallDir)
	if err != nil {
		return empty, fmt.Errorf("stat install dir: %w", err)
	}
	dataExisted, err := s.host.FileExists(s.paths.DataDir)
	if err != nil {
		return empty, fmt.Errorf("stat data dir: %w", err)
	}
	configExisted, err := s.host.FileExists(s.paths.ConfigPath)
	if err != nil {
		return empty, fmt.Errorf("stat config: %w", err)
	}
	serviceExisted, err := s.host.ServiceExists(ServiceName)
	if err != nil {
		return empty, fmt.Errorf("service exists: %w", err)
	}

	var undos []func() error
	rollback := func(cause error) (InstallOutcome, error) {
		s.host.Log("install: rolling back after error: %v", cause)
		for i := len(undos) - 1; i >= 0; i-- {
			if uerr := undos[i](); uerr != nil {
				s.host.Log("install: rollback step failed: %v", uerr)
			}
		}
		return empty, cause
	}

	if err := s.host.MkdirAll(s.paths.InstallDir); err != nil {
		return empty, fmt.Errorf("create install dir: %w", err)
	}
	if !installExisted {
		undos = append(undos, func() error { return s.host.RemoveAll(s.paths.InstallDir) })
	}

	// Stop a running service before replacing the binary (idempotent re-install).
	// Register a restart undo immediately so any later failure brings the agent back up.
	if serviceExisted {
		s.host.Log("install: stopping existing service before binary update")
		if err := s.host.StopService(ServiceName); err != nil {
			return rollback(fmt.Errorf("stop service before replace: %w", err))
		}
		if err := s.host.WaitForState(ServiceName, StateStopped, s.stopTimeout); err != nil {
			return rollback(fmt.Errorf("wait stopped before replace: %w", err))
		}
		// Session agents map the installed image; free it before rename/replace.
		s.host.Log("install: stopping session agents before binary update")
		if err := s.host.StopSessionAgents(s.paths.BinaryPath); err != nil {
			return rollback(fmt.Errorf("stop session agents before replace: %w", err))
		}
		undos = append(undos, func() error {
			s.host.Log("install: rollback restarting pre-existing service")
			if err := s.host.StartService(ServiceName); err != nil {
				return err
			}
			return s.host.WaitForState(ServiceName, StateRunning, s.startTimeout)
		})
	}

	same, err := s.host.SameFile(src, s.paths.BinaryPath)
	if err != nil {
		return rollback(fmt.Errorf("compare executable paths: %w", err))
	}
	bakPath := s.paths.BinaryPath + ".bak"
	wroteBackup := false
	if !same {
		binaryExisted, err := s.host.FileExists(s.paths.BinaryPath)
		if err != nil {
			return rollback(fmt.Errorf("stat binary: %w", err))
		}
		if binaryExisted {
			// Keep a restore copy so rollback can put the previous image back
			// before restarting the service. Removed only after a successful install.
			if err := s.host.CopyFile(s.paths.BinaryPath, bakPath); err != nil {
				return rollback(fmt.Errorf("backup binary: %w", err))
			}
			wroteBackup = true
			undos = append(undos, func() error {
				s.host.Log("install: rollback restoring previous binary")
				if err := s.host.CopyFile(bakPath, s.paths.BinaryPath); err != nil {
					return err
				}
				return s.host.RemoveAll(bakPath)
			})
		}
		if err := s.host.CopyFile(src, s.paths.BinaryPath); err != nil {
			return rollback(fmt.Errorf("copy executable: %w", err))
		}
	}

	if err := s.host.MkdirAll(s.paths.DataDir); err != nil {
		return rollback(fmt.Errorf("create data dir: %w", err))
	}
	if !dataExisted {
		undos = append(undos, func() error { return s.host.RemoveAll(s.paths.DataDir) })
	}

	// Lock the data directory before writing config so the file never inherits
	// permissive ProgramData defaults.
	if err := s.host.ApplyDataACL(s.paths.DataDir); err != nil {
		return rollback(fmt.Errorf("ACL data dir: %w", err))
	}

	outcome, previousCfg, cfgWritten, err := s.ensureConfig(opts)
	if err != nil {
		return rollback(fmt.Errorf("ensure config: %w", err))
	}
	if cfgWritten {
		if !configExisted {
			// Only remove the config file on rollback when the data dir pre-existed
			// (otherwise the data-dir undo already covers it).
			if dataExisted {
				undos = append(undos, func() error { return s.host.RemoveAll(s.paths.ConfigPath) })
			}
		} else if previousCfg != nil {
			// Restore prior config bytes (preserves machine_id/token on rollback).
			prev := append([]byte(nil), previousCfg...)
			undos = append(undos, func() error {
				s.host.Log("install: rollback restoring previous config")
				return s.host.WriteFile(s.paths.ConfigPath, prev)
			})
		}
	}

	if err := s.host.ApplyDataACL(s.paths.ConfigPath); err != nil {
		return rollback(fmt.Errorf("ACL config: %w", err))
	}

	if err := s.host.ApplyBinaryACL(s.paths.InstallDir); err != nil {
		return rollback(fmt.Errorf("ACL install dir: %w", err))
	}
	if err := s.host.ApplyBinaryACL(s.paths.BinaryPath); err != nil {
		return rollback(fmt.Errorf("ACL binary: %w", err))
	}

	spec := DefaultServiceSpec(s.paths.BinaryPath)
	if err := s.host.EnsureService(spec); err != nil {
		return rollback(fmt.Errorf("register service: %w", err))
	}
	if !serviceExisted {
		undos = append(undos, func() error {
			_ = s.host.StopService(ServiceName)
			return s.host.DeleteService(ServiceName)
		})
	}

	fw := DefaultFirewallRule(s.paths.BinaryPath)
	// Register firewall undo BEFORE mutating. EnsureFirewallRule may delete then
	// add; on reinstall a failed add must restore the outbound allow rule.
	if serviceExisted {
		undos = append(undos, func() error {
			s.host.Log("install: rollback restoring firewall rule")
			return s.host.EnsureFirewallRule(fw)
		})
	} else {
		undos = append(undos, func() error { return s.host.RemoveFirewallRule(FirewallRuleName) })
	}
	if err := s.host.EnsureFirewallRule(fw); err != nil {
		return rollback(fmt.Errorf("firewall rule: %w", err))
	}

	if err := s.host.StartService(ServiceName); err != nil {
		return rollback(fmt.Errorf("start service: %w", err))
	}
	if err := s.host.WaitForState(ServiceName, StateRunning, s.startTimeout); err != nil {
		return rollback(fmt.Errorf("wait running: %w", err))
	}

	if wroteBackup {
		_ = s.host.RemoveAll(bakPath)
	}
	s.host.Log("install: complete; service %s is Running", ServiceName)
	return outcome, nil
}

// Uninstall removes the service, firewall rule, and installed files (idempotent).
func (s *Service) Uninstall(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.host == nil {
		return fmt.Errorf("installer host is nil")
	}
	if s.requireElevated {
		ok, err := s.host.IsElevated()
		if err != nil {
			return fmt.Errorf("check elevation: %w", err)
		}
		if !ok {
			return ErrNotElevated
		}
	}

	s.host.Log("uninstall: begin")
	var errs []error

	exists, err := s.host.ServiceExists(ServiceName)
	if err != nil {
		errs = append(errs, fmt.Errorf("service exists: %w", err))
	} else if exists {
		if err := s.host.StopService(ServiceName); err != nil {
			s.host.Log("uninstall: stop service: %v (continuing)", err)
		}
		if err := s.host.WaitForState(ServiceName, StateStopped, s.stopTimeout); err != nil {
			s.host.Log("uninstall: wait stopped: %v (continuing)", err)
		}
		if err := s.host.StopSessionAgents(s.paths.BinaryPath); err != nil {
			s.host.Log("uninstall: stop session agents: %v (continuing)", err)
		}
		if err := s.host.DeleteService(ServiceName); err != nil {
			errs = append(errs, fmt.Errorf("delete service: %w", err))
		}
	}

	if err := s.host.RemoveFirewallRule(FirewallRuleName); err != nil {
		errs = append(errs, fmt.Errorf("remove firewall rule: %w", err))
	}

	if err := s.host.RemoveAll(s.paths.InstallDir); err != nil {
		errs = append(errs, fmt.Errorf("remove install dir: %w", err))
	}
	if err := s.host.RemoveAll(s.paths.DataDir); err != nil {
		errs = append(errs, fmt.Errorf("remove data dir: %w", err))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	s.host.Log("uninstall: complete")
	return nil
}

// Stub is retained for CLI unit tests that inject an unimplemented Installer.
type Stub struct{}

// Install returns ErrNotImplemented.
func (Stub) Install(context.Context, InstallOptions) (InstallOutcome, error) {
	return InstallOutcome{}, ErrNotImplemented
}

// Uninstall returns ErrNotImplemented.
func (Stub) Uninstall(context.Context) error { return ErrNotImplemented }
