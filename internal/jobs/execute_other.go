//go:build !windows

package jobs

import (
	"context"
	"fmt"
	"time"
)

// unsupportedExecutor rejects execution on non-Windows hosts.
type unsupportedExecutor struct{}

// Run implements Executor.
func (unsupportedExecutor) Run(context.Context, string, string, time.Duration) (ExecResult, error) {
	return ExecResult{}, fmt.Errorf("jobs: script execution requires Windows")
}

// DefaultExecutor returns a stub that refuses to run scripts.
func DefaultExecutor() Executor {
	return unsupportedExecutor{}
}
