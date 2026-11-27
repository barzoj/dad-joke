package jobs

import (
	"context"
	"time"
)

// ExecResult is the outcome of a local script run.
type ExecResult struct {
	ExitCode   int
	Stdout     string
	Stderr     string
	TimedOut   bool
	DurationMS int64
}

// Executor runs a claimed job locally. Windows builds use a Job Object;
// tests inject fakes so logic stays portable.
type Executor interface {
	Run(ctx context.Context, jobType, payload string, timeout time.Duration) (ExecResult, error)
}
