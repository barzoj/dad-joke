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
}

// UserStore persists users and external identities.
type UserStore interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, tenantID, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	CreateIdentity(ctx context.Context, id *UserIdentity) error
	GetIdentity(ctx context.Context, provider, subject string) (*UserIdentity, error)
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

// JobResult is the terminal outcome of a claimed job.
type JobResult struct {
	Status     JobStatus
	ExitCode   *int
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
	Claim(ctx context.Context, tenantID, machineID, id string, at time.Time) (*Job, error)
	Complete(ctx context.Context, tenantID, id string, result JobResult) error
	Cancel(ctx context.Context, tenantID, id string, at time.Time) error
	ListPendingIDs(ctx context.Context, tenantID, machineID string, now time.Time) ([]string, error)
}

// EventQuery filters control-plane event reads.
type EventQuery struct {
	TenantID  string
	MachineID string
	Type      string
	From      *time.Time
	To        *time.Time
	Limit     int
}

// EventStore persists agent telemetry with idempotent inserts.
type EventStore interface {
	InsertBatch(ctx context.Context, events []Event) error
	List(ctx context.Context, q EventQuery) ([]Event, error)
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
	Tokens() TokenStore
	Machines() MachineStore
	Policies() PolicyStore
	Jobs() JobStore
	Events() EventStore
	Audits() AuditStore
}
