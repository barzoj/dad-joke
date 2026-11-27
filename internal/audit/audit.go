// Package audit appends sensitive-action records via the DAL AuditStore.
//
// Raw secrets must never appear in Details. Callers pass only non-secret metadata.
package audit

import (
	"context"
	"fmt"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
)

// Stable action names persisted in audit_logs.
const (
	ActionTokenIssue   = "token.issue"
	ActionTokenRevoke  = "token.revoke"
	ActionPolicyCreate = "policy.create"
	ActionScriptLaunch = "script.launch"
	ActionJobCreate    = "job.create"
	ActionJobClaim     = "job.claim"
	ActionJobComplete  = "job.complete"
	ActionJobCancel    = "job.cancel"
)

// Logger writes append-only audit entries.
type Logger struct {
	Store store.AuditStore
	Clock func() time.Time
}

func (l Logger) now() time.Time {
	if l.Clock != nil {
		return l.Clock().UTC()
	}
	return time.Now().UTC()
}

// Append writes a generic audit entry.
func (l Logger) Append(ctx context.Context, tenantID, action, actorID, resource string, details map[string]string) error {
	if l.Store == nil {
		return fmt.Errorf("audit: store is required")
	}
	return l.Store.Append(ctx, &store.AuditEntry{
		TenantID:  tenantID,
		Action:    action,
		ActorID:   actorID,
		Resource:  resource,
		Details:   details,
		CreatedAt: l.now(),
	})
}

// TokenIssue records issuance of an agent token or control API key.
func (l Logger) TokenIssue(ctx context.Context, tenantID, actorID, tokenID string, kind store.TokenKind, prefix, name string) error {
	return l.Append(ctx, tenantID, ActionTokenIssue, actorID, "token:"+tokenID, map[string]string{
		"kind":   string(kind),
		"prefix": prefix,
		"name":   name,
	})
}

// TokenRevoke records revocation of an agent token or control API key.
func (l Logger) TokenRevoke(ctx context.Context, tenantID, actorID, tokenID string, kind store.TokenKind) error {
	return l.Append(ctx, tenantID, ActionTokenRevoke, actorID, "token:"+tokenID, map[string]string{
		"kind": string(kind),
	})
}

// PolicyCreate is the hook for future policy-create audit events.
func (l Logger) PolicyCreate(ctx context.Context, tenantID, actorID, machineID string, version int32) error {
	return l.Append(ctx, tenantID, ActionPolicyCreate, actorID, "policy:"+machineID, map[string]string{
		"machine_id": machineID,
		"version":    fmt.Sprintf("%d", version),
	})
}

// ScriptLaunch is the legacy hook for remote-script launch audit events.
func (l Logger) ScriptLaunch(ctx context.Context, tenantID, actorID, jobID, machineID string) error {
	return l.Append(ctx, tenantID, ActionScriptLaunch, actorID, "job:"+jobID, map[string]string{
		"machine_id": machineID,
		"job_id":     jobID,
	})
}

// JobCreate records enqueue of a one-shot job.
func (l Logger) JobCreate(ctx context.Context, tenantID, actorID, jobID, machineID string) error {
	return l.Append(ctx, tenantID, ActionJobCreate, actorID, "job:"+jobID, map[string]string{
		"machine_id": machineID,
		"job_id":     jobID,
	})
}

// JobClaim records an agent claim (pending → running).
func (l Logger) JobClaim(ctx context.Context, tenantID, actorID, jobID, machineID string) error {
	return l.Append(ctx, tenantID, ActionJobClaim, actorID, "job:"+jobID, map[string]string{
		"machine_id": machineID,
		"job_id":     jobID,
	})
}

// JobComplete records a terminal job result (succeeded/failed).
func (l Logger) JobComplete(ctx context.Context, tenantID, actorID, jobID, machineID, status string) error {
	return l.Append(ctx, tenantID, ActionJobComplete, actorID, "job:"+jobID, map[string]string{
		"machine_id": machineID,
		"job_id":     jobID,
		"status":     status,
	})
}

// JobCancel records cancel of a pending job.
func (l Logger) JobCancel(ctx context.Context, tenantID, actorID, jobID, machineID string) error {
	return l.Append(ctx, tenantID, ActionJobCancel, actorID, "job:"+jobID, map[string]string{
		"machine_id": machineID,
		"job_id":     jobID,
	})
}
