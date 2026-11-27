package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/barzoj/dad-joke/internal/acl"
)

// AuditFileName is the JSONL audit log under ProgramData\DadJoke.
const AuditFileName = "jobs-audit.jsonl"

const auditRedacted = "[REDACTED]"

// AuditEntry is one local job-run record. Never includes agent tokens.
type AuditEntry struct {
	TS           string `json:"ts"`
	JobID        string `json:"job_id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
	StdoutBytes  int    `json:"stdout_bytes"`
	StderrBytes  int    `json:"stderr_bytes"`
	Error        string `json:"error,omitempty"`
	SkippedClaim bool   `json:"skipped_claim,omitempty"`
}

// AuditLog appends JSONL entries under DataDir with strict ACLs.
type AuditLog struct {
	DataDir string
	// Redact is a list of secrets (e.g. agent token) scrubbed from every line.
	Redact []string
	mu     sync.Mutex
	ready  bool
}

// Append writes one audit entry. Failures are returned (caller may log).
// Secrets in Redact are replaced with [REDACTED] before the line is written.
func (a *AuditLog) Append(e AuditEntry) error {
	if a == nil {
		return fmt.Errorf("jobs: audit log is nil")
	}
	if a.DataDir == "" {
		return fmt.Errorf("jobs: audit data dir empty")
	}
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339Nano)
	}
	e.Error = a.scrub(e.Error)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = []byte(a.scrub(string(line)))
	line = append(line, '\n')

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := os.MkdirAll(a.DataDir, 0o700); err != nil {
		return fmt.Errorf("jobs: mkdir audit dir: %w", err)
	}
	// Harden the data directory itself (idempotent).
	_ = acl.Apply(a.DataDir)

	path := filepath.Join(a.DataDir, AuditFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path under ProgramData
	if err != nil {
		return fmt.Errorf("jobs: open audit: %w", err)
	}
	defer f.Close()

	if !a.ready {
		if err := acl.Apply(path); err != nil {
			return fmt.Errorf("jobs: audit acl: %w", err)
		}
		a.ready = true
	}
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("jobs: write audit: %w", err)
	}
	return nil
}

// Path returns the audit file path.
func (a *AuditLog) Path() string {
	return filepath.Join(a.DataDir, AuditFileName)
}

func (a *AuditLog) scrub(s string) string {
	if a == nil || s == "" {
		return s
	}
	for _, secret := range a.Redact {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, auditRedacted)
	}
	return s
}
