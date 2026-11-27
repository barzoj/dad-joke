package mongo

import (
	"encoding/json"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
)

type tenantDoc struct {
	ID        string    `bson:"_id"`
	Name      string    `bson:"name"`
	CreatedAt time.Time `bson:"created_at"`
}

func tenantToDoc(t *store.Tenant) tenantDoc {
	return tenantDoc{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt.UTC()}
}

func tenantFromDoc(d tenantDoc) *store.Tenant {
	return &store.Tenant{ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt.UTC()}
}

type userDoc struct {
	ID           string    `bson:"_id"`
	TenantID     string    `bson:"tenant_id"`
	Email        string    `bson:"email"`
	PasswordHash string    `bson:"password_hash"`
	CreatedAt    time.Time `bson:"created_at"`
}

func userToDoc(u *store.User) userDoc {
	return userDoc{
		ID: u.ID, TenantID: u.TenantID, Email: u.Email,
		PasswordHash: u.PasswordHash, CreatedAt: u.CreatedAt.UTC(),
	}
}

func userFromDoc(d userDoc) *store.User {
	return &store.User{
		ID: d.ID, TenantID: d.TenantID, Email: d.Email,
		PasswordHash: d.PasswordHash, CreatedAt: d.CreatedAt.UTC(),
	}
}

type userIdentityDoc struct {
	ID        string    `bson:"_id"`
	UserID    string    `bson:"user_id"`
	Provider  string    `bson:"provider"`
	Subject   string    `bson:"subject"`
	CreatedAt time.Time `bson:"created_at"`
}

func identityToDoc(id *store.UserIdentity) userIdentityDoc {
	return userIdentityDoc{
		ID: id.ID, UserID: id.UserID, Provider: id.Provider,
		Subject: id.Subject, CreatedAt: id.CreatedAt.UTC(),
	}
}

func identityFromDoc(d userIdentityDoc) *store.UserIdentity {
	return &store.UserIdentity{
		ID: d.ID, UserID: d.UserID, Provider: d.Provider,
		Subject: d.Subject, CreatedAt: d.CreatedAt.UTC(),
	}
}

type sessionDoc struct {
	ID        string     `bson:"_id"`
	UserID    string     `bson:"user_id"`
	TenantID  string     `bson:"tenant_id"`
	Hash      string     `bson:"hash"`
	CreatedAt time.Time  `bson:"created_at"`
	ExpiresAt time.Time  `bson:"expires_at"`
	RevokedAt *time.Time `bson:"revoked_at,omitempty"`
}

func sessionToDoc(s *store.Session) sessionDoc {
	d := sessionDoc{
		ID: s.ID, UserID: s.UserID, TenantID: s.TenantID, Hash: s.Hash,
		CreatedAt: s.CreatedAt.UTC(), ExpiresAt: s.ExpiresAt.UTC(),
	}
	if s.RevokedAt != nil {
		u := s.RevokedAt.UTC()
		d.RevokedAt = &u
	}
	return d
}

func sessionFromDoc(d sessionDoc) *store.Session {
	s := &store.Session{
		ID: d.ID, UserID: d.UserID, TenantID: d.TenantID, Hash: d.Hash,
		CreatedAt: d.CreatedAt.UTC(), ExpiresAt: d.ExpiresAt.UTC(),
	}
	if d.RevokedAt != nil {
		u := d.RevokedAt.UTC()
		s.RevokedAt = &u
	}
	return s
}

type tokenDoc struct {
	ID         string     `bson:"_id"`
	TenantID   string     `bson:"tenant_id"`
	Kind       string     `bson:"kind"`
	Name       string     `bson:"name"`
	Prefix     string     `bson:"prefix"`
	Hash       string     `bson:"hash"`
	CreatedAt  time.Time  `bson:"created_at"`
	LastUsedAt *time.Time `bson:"last_used_at,omitempty"`
	RevokedAt  *time.Time `bson:"revoked_at,omitempty"`
	ExpiresAt  *time.Time `bson:"expires_at,omitempty"`
}

func tokenToDoc(t *store.Token) tokenDoc {
	d := tokenDoc{
		ID: t.ID, TenantID: t.TenantID, Kind: string(t.Kind),
		Name: t.Name, Prefix: t.Prefix, Hash: t.Hash,
		CreatedAt: t.CreatedAt.UTC(),
	}
	if t.LastUsedAt != nil {
		u := t.LastUsedAt.UTC()
		d.LastUsedAt = &u
	}
	if t.RevokedAt != nil {
		u := t.RevokedAt.UTC()
		d.RevokedAt = &u
	}
	if t.ExpiresAt != nil {
		u := t.ExpiresAt.UTC()
		d.ExpiresAt = &u
	}
	return d
}

func tokenFromDoc(d tokenDoc) *store.Token {
	t := &store.Token{
		ID: d.ID, TenantID: d.TenantID, Kind: store.TokenKind(d.Kind),
		Name: d.Name, Prefix: d.Prefix, Hash: d.Hash,
		CreatedAt: d.CreatedAt.UTC(),
	}
	if d.LastUsedAt != nil {
		u := d.LastUsedAt.UTC()
		t.LastUsedAt = &u
	}
	if d.RevokedAt != nil {
		u := d.RevokedAt.UTC()
		t.RevokedAt = &u
	}
	if d.ExpiresAt != nil {
		u := d.ExpiresAt.UTC()
		t.ExpiresAt = &u
	}
	return t
}

type machineDoc struct {
	ID                   string    `bson:"_id"`
	TenantID             string    `bson:"tenant_id"`
	Hostname             string    `bson:"hostname"`
	OSVersion            string    `bson:"os_version"`
	AgentVersion         string    `bson:"agent_version"`
	LastSeenAt           time.Time `bson:"last_seen_at"`
	AppliedPolicyVersion int32     `bson:"applied_policy_version"`
	DesiredPolicyVersion int32     `bson:"desired_policy_version"`
}

func machineToDoc(m *store.Machine) machineDoc {
	return machineDoc{
		ID: m.ID, TenantID: m.TenantID, Hostname: m.Hostname,
		OSVersion: m.OSVersion, AgentVersion: m.AgentVersion,
		LastSeenAt:           m.LastSeenAt.UTC(),
		AppliedPolicyVersion: m.AppliedPolicyVersion,
		DesiredPolicyVersion: m.DesiredPolicyVersion,
	}
}

func machineFromDoc(d machineDoc) *store.Machine {
	return &store.Machine{
		ID: d.ID, TenantID: d.TenantID, Hostname: d.Hostname,
		OSVersion: d.OSVersion, AgentVersion: d.AgentVersion,
		LastSeenAt:           d.LastSeenAt.UTC(),
		AppliedPolicyVersion: d.AppliedPolicyVersion,
		DesiredPolicyVersion: d.DesiredPolicyVersion,
	}
}

type policyDoc struct {
	ID        string          `bson:"_id"` // tenant_id|machine_id
	TenantID  string          `bson:"tenant_id"`
	MachineID string          `bson:"machine_id"`
	Version   int32           `bson:"version"`
	Document  json.RawMessage `bson:"document"`
	UpdatedAt time.Time       `bson:"updated_at"`
}

func policyKey(tenantID, machineID string) string {
	return tenantID + "|" + machineID
}

func policyToDoc(p *store.Policy) policyDoc {
	doc := json.RawMessage(nil)
	if p.Document != nil {
		doc = append(json.RawMessage(nil), p.Document...)
	}
	return policyDoc{
		ID:       policyKey(p.TenantID, p.MachineID),
		TenantID: p.TenantID, MachineID: p.MachineID,
		Version: p.Version, Document: doc, UpdatedAt: p.UpdatedAt.UTC(),
	}
}

func policyFromDoc(d policyDoc) *store.Policy {
	doc := json.RawMessage(nil)
	if d.Document != nil {
		doc = append(json.RawMessage(nil), d.Document...)
	}
	return &store.Policy{
		TenantID: d.TenantID, MachineID: d.MachineID,
		Version: d.Version, Document: doc, UpdatedAt: d.UpdatedAt.UTC(),
	}
}

type jobDoc struct {
	ID         string     `bson:"_id"`
	TenantID   string     `bson:"tenant_id"`
	MachineID  string     `bson:"machine_id"`
	Status     string     `bson:"status"`
	Payload    string     `bson:"payload"`
	ExitCode   *int       `bson:"exit_code,omitempty"`
	Stdout     string     `bson:"stdout,omitempty"`
	Stderr     string     `bson:"stderr,omitempty"`
	DurationMS *int64     `bson:"duration_ms,omitempty"`
	CreatedAt  time.Time  `bson:"created_at"`
	ExpiresAt  time.Time  `bson:"expires_at"`
	ClaimedAt  *time.Time `bson:"claimed_at,omitempty"`
	FinishedAt *time.Time `bson:"finished_at,omitempty"`
}

func jobToDoc(j *store.Job) jobDoc {
	d := jobDoc{
		ID: j.ID, TenantID: j.TenantID, MachineID: j.MachineID,
		Status: string(j.Status), Payload: j.Payload,
		ExitCode: j.ExitCode, Stdout: j.Stdout, Stderr: j.Stderr,
		DurationMS: j.DurationMS,
		CreatedAt:  j.CreatedAt.UTC(),
		ExpiresAt:  j.ExpiresAt.UTC(),
	}
	if j.ClaimedAt != nil {
		u := j.ClaimedAt.UTC()
		d.ClaimedAt = &u
	}
	if j.FinishedAt != nil {
		u := j.FinishedAt.UTC()
		d.FinishedAt = &u
	}
	return d
}

func jobFromDoc(d jobDoc) *store.Job {
	j := &store.Job{
		ID: d.ID, TenantID: d.TenantID, MachineID: d.MachineID,
		Status: store.JobStatus(d.Status), Payload: d.Payload,
		ExitCode: d.ExitCode, Stdout: d.Stdout, Stderr: d.Stderr,
		DurationMS: d.DurationMS,
		CreatedAt:  d.CreatedAt.UTC(),
		ExpiresAt:  d.ExpiresAt.UTC(),
	}
	if d.ClaimedAt != nil {
		u := d.ClaimedAt.UTC()
		j.ClaimedAt = &u
	}
	if d.FinishedAt != nil {
		u := d.FinishedAt.UTC()
		j.FinishedAt = &u
	}
	return j
}

type eventDoc struct {
	ID        string          `bson:"_id"`
	TenantID  string          `bson:"tenant_id"`
	MachineID string          `bson:"machine_id"`
	EventID   string          `bson:"event_id"`
	Type      string          `bson:"type"`
	TS        time.Time       `bson:"ts"`
	Payload   json.RawMessage `bson:"payload,omitempty"`
}

func eventToDoc(e *store.Event) eventDoc {
	payload := json.RawMessage(nil)
	if e.Payload != nil {
		payload = append(json.RawMessage(nil), e.Payload...)
	}
	return eventDoc{
		ID: e.ID, TenantID: e.TenantID, MachineID: e.MachineID,
		EventID: e.EventID, Type: e.Type, TS: e.TS.UTC(), Payload: payload,
	}
}

func eventFromDoc(d eventDoc) *store.Event {
	payload := json.RawMessage(nil)
	if d.Payload != nil {
		payload = append(json.RawMessage(nil), d.Payload...)
	}
	return &store.Event{
		ID: d.ID, TenantID: d.TenantID, MachineID: d.MachineID,
		EventID: d.EventID, Type: d.Type, TS: d.TS.UTC(), Payload: payload,
	}
}

type auditDoc struct {
	ID        string            `bson:"_id"`
	TenantID  string            `bson:"tenant_id"`
	Action    string            `bson:"action"`
	ActorID   string            `bson:"actor_id"`
	Resource  string            `bson:"resource"`
	Details   map[string]string `bson:"details,omitempty"`
	CreatedAt time.Time         `bson:"created_at"`
}

func auditToDoc(e *store.AuditEntry) auditDoc {
	details := map[string]string(nil)
	if e.Details != nil {
		details = make(map[string]string, len(e.Details))
		for k, v := range e.Details {
			details[k] = v
		}
	}
	return auditDoc{
		ID: e.ID, TenantID: e.TenantID, Action: e.Action,
		ActorID: e.ActorID, Resource: e.Resource, Details: details,
		CreatedAt: e.CreatedAt.UTC(),
	}
}

func auditFromDoc(d auditDoc) *store.AuditEntry {
	details := map[string]string(nil)
	if d.Details != nil {
		details = make(map[string]string, len(d.Details))
		for k, v := range d.Details {
			details[k] = v
		}
	}
	return &store.AuditEntry{
		ID: d.ID, TenantID: d.TenantID, Action: d.Action,
		ActorID: d.ActorID, Resource: d.Resource, Details: details,
		CreatedAt: d.CreatedAt.UTC(),
	}
}
