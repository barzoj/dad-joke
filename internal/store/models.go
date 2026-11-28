package store

import (
	"encoding/json"
	"time"
)

// Tenant is an isolated customer/account boundary.
type Tenant struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// User is a control-plane login principal within a tenant.
type User struct {
	ID           string
	TenantID     string
	Email        string
	PasswordHash string `json:"-"` // never serialize; argon2id at rest
	CreatedAt    time.Time
}

// Session is a revocable user login session. The raw secret is never persisted.
type Session struct {
	ID        string
	UserID    string
	TenantID  string
	Hash      string // SHA-256 hex of the raw dj_ses_... secret
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// UserIdentity links an external IdP subject to a user (e.g. future Google OAuth).
type UserIdentity struct {
	ID        string
	UserID    string
	Provider  string
	Subject   string
	CreatedAt time.Time
}

// TokenKind distinguishes agent credentials from control-plane PATs.
type TokenKind string

const (
	// TokenKindAgent is a dj_agt_... credential for AgentAPI.
	TokenKindAgent TokenKind = "agent"
	// TokenKindPAT is a dj_pat_... credential for ControlAPI / automation.
	TokenKindPAT TokenKind = "pat"
)

// Token is a hashed API credential. The raw secret is never persisted.
type Token struct {
	ID         string
	TenantID   string
	Kind       TokenKind
	Name       string
	Prefix     string // non-secret identifying prefix (includes dj_agt_/dj_pat_)
	Hash       string // SHA-256 hex of the raw secret
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	ExpiresAt  *time.Time
}

// Machine is an enrolled Windows agent endpoint.
type Machine struct {
	ID                   string // RFC 4122 UUIDv4 machine_id
	TenantID             string
	Hostname             string
	OSVersion            string
	AgentVersion         string
	LastSeenAt           time.Time
	AppliedPolicyVersion int32
	DesiredPolicyVersion int32
	TimeZone             string // IANA zone from latest heartbeat (may be empty)
}

// Fact is one append-only agent fact row (never updated/deleted by app code).
type Fact struct {
	ID         string // server-assigned UUID
	TenantID   string
	MachineID  string
	StreamID   string
	Seq        int64
	Idx        int32 // index within the batch (0..n-1)
	TS         time.Time
	TSServer   time.Time
	Kind       string
	Plugin     string
	UserSID    string
	DurS       int32
	Data       json.RawMessage
	TimeZone   string
	SentAt     time.Time
}

// UsageDelta is one guarded increment applied to usage_daily.
type UsageDelta struct {
	Date     string // YYYY-MM-DD in the batch's time zone
	TimeZone string
	Bucket   string // "app:<exe>" or "fg:<exe>"
	Seconds  int64
}

// UsageDaily is one per-(day,bucket) aggregate row.
type UsageDaily struct {
	TenantID  string
	MachineID string
	Date      string
	TimeZone  string
	Bucket    string
	Seconds   int64
}

// Policy is the versioned desired-state document for one machine.
type Policy struct {
	TenantID  string
	MachineID string
	Version   int32
	Document  json.RawMessage
	UpdatedAt time.Time
}

// JobStatus is the job state machine.
type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCanceled  JobStatus = "canceled"
	JobTimedOut  JobStatus = "timed_out"
)

// Supported job interpreter types (mirrored in api/common/jobs.tsp).
const (
	JobTypePowerShell = "powershell"
	JobTypeCmd        = "cmd"
)

// JobTimeoutDefaultSeconds / JobTimeoutMaxSeconds mirror the agent hard-timeout contract.
const (
	JobTimeoutDefaultSeconds = 60
	JobTimeoutMaxSeconds     = 300
)

// Job is a one-shot remote command/script assignment.
type Job struct {
	ID              string
	TenantID        string
	MachineID       string
	Status          JobStatus
	Type            string // powershell | cmd
	Payload         string
	TimeoutSeconds  *int32
	ExitCode        *int32
	Stdout          string
	Stderr          string
	DurationMS      *int64
	CreatedAt       time.Time
	ExpiresAt       time.Time
	ClaimedAt       *time.Time
	FinishedAt      *time.Time
}

// Event is a compact agent telemetry record.
type Event struct {
	ID        string // server-assigned UUID
	TenantID  string
	MachineID string
	EventID   string // client-supplied idempotency key
	Type      string
	TS        time.Time
	Payload   json.RawMessage
}

// AuditEntry is an append-only sensitive-action record (never stores secrets).
type AuditEntry struct {
	ID        string
	TenantID  string
	Action    string
	ActorID   string
	Resource  string
	Details   map[string]string
	CreatedAt time.Time
}
