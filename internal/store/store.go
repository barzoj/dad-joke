package store

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by store implementations.
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
	ErrInvalid  = errors.New("store: invalid argument")
)

// Pinger reports datastore reachability for readiness checks.
type Pinger interface {
	Ping(ctx context.Context) error
}

// TenantStore persists tenants.
type TenantStore interface {
	Create(ctx context.Context, t *Tenant) error
	GetByID(ctx context.Context, id string) (*Tenant, error)
	GetByName(ctx context.Context, name string) (*Tenant, error)
	// Delete removes a tenant by id (compensating rollback for failed registration).
	Delete(ctx context.Context, id string) error
}

// UserStore persists users and external identities.
type UserStore interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, tenantID, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	CreateIdentity(ctx context.Context, id *UserIdentity) error
	GetIdentity(ctx context.Context, provider, subject string) (*UserIdentity, error)
}

// SessionStore persists hashed user session credentials.
type SessionStore interface {
	Create(ctx context.Context, s *Session) error
	GetByHash(ctx context.Context, hash string) (*Session, error)
	GetByID(ctx context.Context, id string) (*Session, error)
	Revoke(ctx context.Context, id string, at time.Time) error
}

// TokenStore persists hashed agent tokens and control-plane PATs.
type TokenStore interface {
	Create(ctx context.Context, t *Token) error
	GetByID(ctx context.Context, tenantID, id string) (*Token, error)
	GetByHash(ctx context.Context, kind TokenKind, hash string) (*Token, error)
	ListByTenant(ctx context.Context, tenantID string, kind TokenKind) ([]Token, error)
	Revoke(ctx context.Context, tenantID, id string, at time.Time) error
	TouchLastUsed(ctx context.Context, tenantID, id string, at time.Time) error
}

// MachineStore persists enrolled machines.
type MachineStore interface {
	Upsert(ctx context.Context, m *Machine) error
	Get(ctx context.Context, tenantID, machineID string) (*Machine, error)
	ListByTenant(ctx context.Context, tenantID string) ([]Machine, error)
}

// PolicyStore persists versioned desired policy documents.
type PolicyStore interface {
	Get(ctx context.Context, tenantID, machineID string) (*Policy, error)
	// Put writes a policy. expectedVersion 0 creates version 1; otherwise the
	// stored version must equal expectedVersion or ErrConflict is returned.
	Put(ctx context.Context, p *Policy, expectedVersion int32) error
}

// JobIOMaxBytes is the hard cap for job payload and result stdout/stderr.
const JobIOMaxBytes = 64 * 1024

// JobResult is the terminal outcome of a claimed job.
type JobResult struct {
	Status     JobStatus
	ExitCode   *int32
	Stdout     string
	Stderr     string
	DurationMS *int64
	FinishedAt time.Time
}

// JobStore persists the one-shot job queue.
type JobStore interface {
	Create(ctx context.Context, j *Job) error
	Get(ctx context.Context, tenantID, id string) (*Job, error)
	ListByMachine(ctx context.Context, tenantID, machineID string) ([]Job, error)
	// Claim atomically moves pending → running for (tenantID, machineID, id).
	// Expired pending jobs transition to timed_out and return ErrConflict.
	// Wrong machine or missing/cross-tenant jobs return ErrNotFound.
	Claim(ctx context.Context, tenantID, machineID, id string, at time.Time) (*Job, error)
	// Complete writes a terminal result for a running job owned by machineID.
	Complete(ctx context.Context, tenantID, machineID, id string, result JobResult) error
	Cancel(ctx context.Context, tenantID, id string, at time.Time) error
	// ListPendingIDs returns non-expired pending job ids and transitions any
	// expired pending jobs for the machine to timed_out.
	ListPendingIDs(ctx context.Context, tenantID, machineID string, now time.Time) ([]string, error)
}

// Event ingest / listing hard limits (mirrored in api/common/events.tsp).
const (
	EventBatchMaxCount    = 100
	EventBatchMaxBytes    = 256 * 1024
	EventMaxFutureSkew    = 5 * time.Minute
	EventListDefaultLimit = 50
	EventListMinLimit     = 1
	EventListMaxLimit     = 100
	EventTTLSeconds       = 30 * 24 * 60 * 60 // 30 days
)

// Fact ingest hard limits (mirrored in api/common/facts.tsp).
const (
	FactBatchMaxCount         = 500
	FactBatchMaxBytes         = 256 * 1024
	FactDurMaxSeconds         = 300
	FactTitleMaxLen           = 256 // characters/runes, not bytes
	FactRetentionDaysDefault  = 30
	FactRetentionDaysMin      = 1
	FactRetentionDaysMax      = 365
	FactTTLSeconds            = FactRetentionDaysDefault * 24 * 60 * 60 // default 30 days
)

// FactTTLSecondsFor returns expireAfterSeconds for the given retention days.
func FactTTLSecondsFor(days int) int32 {
	return int32(days * 24 * 60 * 60)
}

// EventQuery filters control-plane event reads.
// Results are ordered by ts ascending, then event_id ascending (tie-breaker).
// Cursor resumes strictly after the encoded (ts, event_id) pair.
type EventQuery struct {
	TenantID  string
	MachineID string
	Type      string
	From      *time.Time
	To        *time.Time
	Limit     int
	Cursor    string // opaque; empty = start
}

// EventPage is a deterministic page of events plus an optional next cursor.
type EventPage struct {
	Events     []Event
	NextCursor string // empty when no further results
}

// EventStore persists agent telemetry with idempotent inserts.
//
// InsertBatch semantics:
//   - Callers must validate the batch (size, timestamps) before calling.
//   - Duplicate (tenant_id, machine_id, event_id) — within the batch or
//     against existing rows — are skipped idempotently (no error, no dup).
type EventStore interface {
	InsertBatch(ctx context.Context, events []Event) error
	List(ctx context.Context, q EventQuery) (EventPage, error)
}

// FactStore persists append-only agent facts. Insert only — no update/delete.
//
// InsertBatch semantics:
//   - Callers must validate the batch before calling.
//   - Unique (tenant_id, machine_id, stream_id, seq, idx); duplicate-key
//     errors are swallowed per document (like EventStore).
//   - Returns the number of newly inserted rows.
type FactStore interface {
	InsertBatch(ctx context.Context, facts []Fact) (inserted int, err error)
}

// UsageStore maintains per-machine local-day × bucket aggregates.
//
// Apply uses a per-stream high-water mark so replays and out-of-order seq
// values never double-count. Returns the number of documents whose seconds
// were incremented (0 when every delta was already applied or empty).
type UsageStore interface {
	Apply(ctx context.Context, tenantID, machineID, streamID string, seq int64, deltas []UsageDelta) (updated int, err error)
	ListByMachine(ctx context.Context, tenantID, machineID, date string) ([]UsageDaily, error)
}

// AuditStore appends sensitive-action audit records.
type AuditStore interface {
	Append(ctx context.Context, e *AuditEntry) error
	ListByTenant(ctx context.Context, tenantID string, limit int) ([]AuditEntry, error)
}

// Store is the DAL facade used by the control plane and agent HTTP surfaces.
type Store interface {
	Pinger
	EnsureIndexes(ctx context.Context) error
	Close(ctx context.Context) error

	Tenants() TenantStore
	Users() UserStore
	Sessions() SessionStore
	Tokens() TokenStore
	Machines() MachineStore
	Policies() PolicyStore
	Jobs() JobStore
	Events() EventStore
	Facts() FactStore
	Usage() UsageStore
	Audits() AuditStore
}
