package jobs

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/installer"
)

// Logger is a minimal log sink.
type Logger interface {
	Log(format string, args ...any)
}

// NopLogger discards logs.
type NopLogger struct{}

// Log implements Logger.
func (NopLogger) Log(string, ...any) {}

// WriterLogger logs to an io-like writer via fmt.
type WriterLogger struct {
	Printf func(format string, args ...any)
}

// Log implements Logger.
func (l WriterLogger) Log(format string, args ...any) {
	if l.Printf != nil {
		l.Printf(format+"\n", args...)
	}
}

// ClientFactory builds an AgentAPI client for a validated backend URL.
type ClientFactory func(backendURL string) (API, error)

// Worker sequentially claims and executes jobs handed off from heartbeat.
// It is independent of the heartbeat and updater loops (service.Group).
type Worker struct {
	Handoff    *Handoff
	Executor   Executor
	Audit      *AuditLog
	ConfigPath string
	Config     *config.Config // optional test override
	Log        Logger
	HTTPClient *http.Client
	NewClient  ClientFactory

	// redactToken is used only for tests that inject a custom redactor.
	redactToken func(err error, token string) string
}

// New returns a production Worker sharing handoff with the heartbeat loop.
func New(handoff *Handoff) *Worker {
	paths := installer.DefaultPaths()
	if handoff == nil {
		handoff = NewHandoff()
	}
	return &Worker{
		Handoff:    handoff,
		Executor:   DefaultExecutor(),
		Audit:      &AuditLog{DataDir: paths.DataDir},
		ConfigPath: paths.ConfigPath,
		Log: WriterLogger{Printf: func(format string, args ...any) {
			_, _ = fmt.Fprintf(os.Stderr, format, args...)
		}},
	}
}

// Run implements service.Agent: process jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if w.Log == nil {
		w.Log = NopLogger{}
	}
	if w.Handoff == nil {
		w.Handoff = NewHandoff()
	}
	if w.Executor == nil {
		w.Executor = DefaultExecutor()
	}
	if w.Audit == nil {
		w.Audit = &AuditLog{DataDir: installer.DefaultPaths().DataDir}
	}
	if w.ConfigPath == "" {
		w.ConfigPath = installer.DefaultPaths().ConfigPath
	}
	if w.NewClient == nil {
		w.NewClient = w.defaultNewClient
	}
	if w.redactToken == nil {
		w.redactToken = redactErr
	}

	w.Log.Log("jobs: worker starting (version=%s)", buildinfo.Version)

	for {
		// Drain any queued work first.
		for {
			id, ok := w.Handoff.Pop()
			if !ok {
				break
			}
			w.processOne(ctx, id)
			if err := ctx.Err(); err != nil {
				w.Log.Log("jobs: worker stopping")
				return err
			}
		}
		select {
		case <-ctx.Done():
			w.Log.Log("jobs: worker stopping")
			return ctx.Err()
		case <-w.Handoff.Wake():
		}
	}
}

func (w *Worker) processOne(ctx context.Context, jobID string) {
	cfg, err := w.loadConfig()
	if err != nil {
		w.Log.Log("jobs: load config failed (skip %s): %v", jobID, err)
		return
	}
	backend := strings.TrimSpace(cfg.BackendURL)
	if backend == "" {
		w.Log.Log("jobs: backend_url unset; skip %s", jobID)
		return
	}
	client, err := w.NewClient(backend)
	if err != nil {
		w.Log.Log("jobs: client init failed (skip %s): %s", jobID, w.redactToken(err, cfg.Token))
		return
	}
	editor := bearerEditor(cfg.Token)

	claim := claimJob(ctx, client, jobID, cfg.MachineID, editor)
	switch claim.Outcome {
	case ClaimSkip:
		_ = w.Audit.Append(AuditEntry{
			JobID:        jobID,
			Status:       "skipped_claim",
			SkippedClaim: true,
		})
		w.Log.Log("jobs: claim skipped (409/404) id=%s", jobID)
		return
	case ClaimErr:
		w.Log.Log("jobs: claim failed id=%s: %s", jobID, w.redactToken(claim.Err, cfg.Token))
		return
	}

	job := claim.Job
	jobType := string(job.Type)
	payload := job.Payload

	if err := ValidateClaimed(jobType, payload); err != nil {
		w.finishInvalid(ctx, client, editor, cfg, jobID, jobType, err)
		return
	}

	timeout := ClampTimeout(job.TimeoutSeconds)
	w.Log.Log("jobs: executing id=%s type=%s timeout=%s", jobID, jobType, timeout)

	execRes, execErr := w.Executor.Run(ctx, jobType, payload, timeout)
	if execErr != nil && !execRes.TimedOut {
		// Treat spawn/run infrastructure errors as failed results when possible.
		w.Log.Log("jobs: execute error id=%s: %v", jobID, execErr)
		if execRes.DurationMS == 0 {
			execRes.DurationMS = 0
		}
		if execRes.Stderr == "" {
			execRes.Stderr = TruncateUTF8String(execErr.Error(), MaxOutputBytes)
		}
		if execRes.ExitCode == 0 {
			execRes.ExitCode = 1
		}
	}

	status := agentapi.DadJokeCommonJobResultStatusFailed
	statusStr := "failed"
	switch {
	case execRes.TimedOut:
		status = agentapi.DadJokeCommonJobResultStatusTimedOut
		statusStr = "timed_out"
	case execRes.ExitCode == 0 && execErr == nil:
		status = agentapi.DadJokeCommonJobResultStatusSucceeded
		statusStr = "succeeded"
	}

	stdout := TruncateUTF8String(execRes.Stdout, MaxOutputBytes)
	stderr := TruncateUTF8String(execRes.Stderr, MaxOutputBytes)
	ec := clampExitCode(execRes.ExitCode)
	exitPtr := &ec

	if err := postResult(ctx, client, editor, jobID, cfg.MachineID, status, exitPtr, stdout, stderr, execRes.DurationMS); err != nil {
		w.Log.Log("jobs: result submit failed id=%s: %s", jobID, w.redactToken(err, cfg.Token))
	}
	// Events are best-effort.
	if err := postJobEvent(ctx, client, editor, cfg.MachineID, jobID, statusStr, execRes.DurationMS); err != nil {
		w.Log.Log("jobs: event emit failed id=%s: %s", jobID, w.redactToken(err, cfg.Token))
	}
	_ = w.Audit.Append(AuditEntry{
		JobID:       jobID,
		Type:        jobType,
		Status:      statusStr,
		ExitCode:    &execRes.ExitCode,
		DurationMS:  execRes.DurationMS,
		StdoutBytes: len(stdout),
		StderrBytes: len(stderr),
	})
	w.Log.Log("jobs: finished id=%s status=%s exit=%d duration_ms=%d", jobID, statusStr, execRes.ExitCode, execRes.DurationMS)
}

func (w *Worker) finishInvalid(ctx context.Context, client API, editor agentapi.RequestEditorFn, cfg config.Config, jobID, jobType string, validErr error) {
	w.Log.Log("jobs: validation rejected id=%s: %v", jobID, validErr)
	stderr := TruncateUTF8String(validErr.Error(), MaxOutputBytes)
	code := int32(1)
	status := agentapi.DadJokeCommonJobResultStatusFailed
	_ = postResult(ctx, client, editor, jobID, cfg.MachineID, status, &code, "", stderr, 0)
	_ = postJobEvent(ctx, client, editor, cfg.MachineID, jobID, "failed", 0)
	errStr := validErr.Error()
	exit := 1
	_ = w.Audit.Append(AuditEntry{
		JobID:      jobID,
		Type:       jobType,
		Status:     "failed",
		ExitCode:   &exit,
		DurationMS: 0,
		Error:      errStr,
	})
}

func (w *Worker) loadConfig() (config.Config, error) {
	if w.Config != nil {
		if err := w.Config.Validate(); err != nil {
			return config.Config{}, err
		}
		return *w.Config, nil
	}
	return config.LoadFile(w.ConfigPath)
}

func (w *Worker) defaultNewClient(backendURL string) (API, error) {
	if err := config.ValidateBackendURL(backendURL); err != nil {
		return nil, err
	}
	httpClient := w.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient()
	}
	return agentapi.NewClientWithResponses(backendURL, agentapi.WithHTTPClient(httpClient))
}

func redactErr(err error, token string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if token != "" && strings.Contains(s, token) {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}

func clampExitCode(code int) int32 {
	if code > math.MaxInt32 {
		return math.MaxInt32
	}
	if code < math.MinInt32 {
		return math.MinInt32
	}
	return int32(code) // #nosec G115 -- clamped to int32 range above
}
