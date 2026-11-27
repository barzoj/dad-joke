// Package service implements the DadJoke Windows Service entrypoint
// (`dad-joke run`). Windows SCM / OS calls stay behind platform files so
// unit tests run on Linux/macOS (see ARCHITECTURE.md).
package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// ErrNotImplemented is retained for CLI tests that inject Stub.
var ErrNotImplemented = errors.New("run not implemented yet (see issues #2 and #3)")

// ErrUnsupported is returned when run is invoked on a non-Windows build that
// cannot register with SCM (foreground diagnostic mode still works via Runner).
var ErrUnsupported = errors.New("windows service mode is only supported on Windows")

// Runner is the versioned contract for the service process entrypoint.
type Runner interface {
	Run(ctx context.Context) error
}

// Agent is the long-running work performed while the service is Running.
// Production wires the Issue #3 update poller via NewRunner Options.Agent.
type Agent interface {
	// Run blocks until ctx is cancelled. It must return promptly on cancel.
	Run(ctx context.Context) error
}

// IdleAgent blocks until ctx is done — used by tests and as a safe default.
type IdleAgent struct{}

// Run waits for cancellation.
func (IdleAgent) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Options configures a ServiceRunner.
type Options struct {
	Name    string
	Agent   Agent
	Signals []os.Signal
	// ForceForeground skips SCM detection (diagnostics / tests).
	ForceForeground bool
}

// ServiceRunner runs as a Windows Service when launched by SCM, otherwise
// foreground until signal / ctx cancel.
type ServiceRunner struct {
	name            string
	agent           Agent
	signals         []os.Signal
	forceForeground bool
}

// New returns the platform default Runner.
func New() Runner {
	return NewRunner(Options{})
}

// NewRunner builds a Runner with explicit options.
func NewRunner(opts Options) *ServiceRunner {
	if opts.Name == "" {
		opts.Name = "DadJoke"
	}
	if opts.Agent == nil {
		opts.Agent = IdleAgent{}
	}
	if len(opts.Signals) == 0 {
		opts.Signals = []os.Signal{os.Interrupt, syscall.SIGTERM}
	}
	return &ServiceRunner{
		name:            opts.Name,
		agent:           opts.Agent,
		signals:         opts.Signals,
		forceForeground: opts.ForceForeground,
	}
}

// Run starts the agent under SCM or in the foreground.
func (r *ServiceRunner) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.forceForeground || !isWindowsServiceSession() {
		return r.runForeground(ctx)
	}
	return runWindowsService(r.name, r.agent)
}

func (r *ServiceRunner) runForeground(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, r.signals...)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.agent.Run(ctx)
	}()

	select {
	case <-ctx.Done():
		cancel()
		select {
		case err := <-errCh:
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-time.After(5 * time.Second):
			return fmt.Errorf("agent did not stop after cancel")
		}
	case sig := <-sigCh:
		cancel()
		select {
		case err := <-errCh:
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-time.After(5 * time.Second):
			return fmt.Errorf("agent did not stop after signal %v", sig)
		}
	case err := <-errCh:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

// Stub returns ErrNotImplemented for CLI unit tests.
type Stub struct{}

// Run returns ErrNotImplemented.
func (Stub) Run(context.Context) error { return ErrNotImplemented }
