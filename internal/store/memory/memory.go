// Package memory provides an in-process Store for unit tests.
package memory

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

// Store is a mutex-guarded in-memory Store.
type Store struct {
	mu          sync.Mutex
	pingErr     error
	tenants     map[string]store.Tenant
	byName      map[string]string
	users       map[string]store.User
	byEmail     map[string]string
	idents      map[string]store.UserIdentity // provider\0subject
	sessions    map[string]store.Session
	sessionHash map[string]string // hash -> id
	tokens      map[string]store.Token
	tokenHash   map[string]string // kind+"|"+hash -> id
	machines    map[string]store.Machine
	policies    map[string]store.Policy
	jobs        map[string]store.Job
	events      map[string]store.Event // tenant|machine|event_id
	facts       map[string]store.Fact  // tenant|machine|stream|seq|idx
	usage       map[string]usageRow    // daily id
	audits      []store.AuditEntry

	// AfterFactInsertErr, when set, is returned by InsertBatch after a
	// successful insert (fault injection between insert and usage Apply).
	AfterFactInsertErr error
}

type usageRow struct {
	store.UsageDaily
	HW map[string]int64 // stream_id → high-water seq
}

// New returns an empty memory Store.
func New() *Store {
	return &Store{
		tenants:     make(map[string]store.Tenant),
		byName:      make(map[string]string),
		users:       make(map[string]store.User),
		byEmail:     make(map[string]string),
		idents:      make(map[string]store.UserIdentity),
		sessions:    make(map[string]store.Session),
		sessionHash: make(map[string]string),
		tokens:      make(map[string]store.Token),
		tokenHash:   make(map[string]string),
		machines:    make(map[string]store.Machine),
		policies:    make(map[string]store.Policy),
		jobs:        make(map[string]store.Job),
		events:      make(map[string]store.Event),
		facts:       make(map[string]store.Fact),
		usage:       make(map[string]usageRow),
	}
}

// SetPingError forces Ping to fail (for readiness tests).
func (s *Store) SetPingError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pingErr = err
}

func (s *Store) Ping(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pingErr
}

func (s *Store) EnsureIndexes(context.Context) error { return nil }

func (s *Store) Close(context.Context) error { return nil }

func (s *Store) Tenants() store.TenantStore   { return tenantStore{s} }
func (s *Store) Users() store.UserStore       { return userStore{s} }
func (s *Store) Sessions() store.SessionStore { return sessionStore{s} }
func (s *Store) Tokens() store.TokenStore     { return tokenStore{s} }
func (s *Store) Machines() store.MachineStore { return machineStore{s} }
func (s *Store) Policies() store.PolicyStore  { return policyStore{s} }
func (s *Store) Jobs() store.JobStore         { return jobStore{s} }
func (s *Store) Events() store.EventStore     { return eventStore{s} }
func (s *Store) Facts() store.FactStore       { return factStore{s} }
func (s *Store) Usage() store.UsageStore      { return usageStore{s} }
func (s *Store) Audits() store.AuditStore     { return auditStore{s} }

type tenantStore struct{ s *Store }

func (t tenantStore) Create(_ context.Context, ten *store.Tenant) error {
	if ten == nil || strings.TrimSpace(ten.Name) == "" {
		return store.ErrInvalid
	}
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	name := strings.TrimSpace(ten.Name)
	if _, ok := t.s.byName[name]; ok {
		return store.ErrConflict
	}
	cp := *ten
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	cp.Name = name
	if _, ok := t.s.tenants[cp.ID]; ok {
		return store.ErrConflict
	}
	t.s.tenants[cp.ID] = cp
	t.s.byName[name] = cp.ID
	*ten = cp
	return nil
}

func (t tenantStore) GetByID(_ context.Context, id string) (*store.Tenant, error) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	ten, ok := t.s.tenants[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := ten
	return &cp, nil
}

func (t tenantStore) GetByName(_ context.Context, name string) (*store.Tenant, error) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	id, ok := t.s.byName[strings.TrimSpace(name)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := t.s.tenants[id]
	return &cp, nil
}

func (t tenantStore) Delete(_ context.Context, id string) error {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	ten, ok := t.s.tenants[id]
	if !ok {
		return store.ErrNotFound
	}
	delete(t.s.tenants, id)
	delete(t.s.byName, ten.Name)
	return nil
}

type userStore struct{ s *Store }

func (u userStore) Create(_ context.Context, user *store.User) error {
	if user == nil || user.TenantID == "" || strings.TrimSpace(user.Email) == "" {
		return store.ErrInvalid
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	email := strings.ToLower(strings.TrimSpace(user.Email))
	if _, ok := u.s.byEmail[email]; ok {
		return store.ErrConflict
	}
	cp := *user
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	cp.Email = email
	u.s.users[cp.ID] = cp
	u.s.byEmail[email] = cp.ID
	*user = cp
	return nil
}

func (u userStore) GetByID(_ context.Context, tenantID, id string) (*store.User, error) {
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	user, ok := u.s.users[id]
	if !ok || user.TenantID != tenantID {
		return nil, store.ErrNotFound
	}
	cp := user
	return &cp, nil
}

func (u userStore) GetByEmail(_ context.Context, email string) (*store.User, error) {
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	id, ok := u.s.byEmail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := u.s.users[id]
	return &cp, nil
}

func (u userStore) CreateIdentity(_ context.Context, id *store.UserIdentity) error {
	if id == nil || id.UserID == "" || id.Provider == "" || id.Subject == "" {
		return store.ErrInvalid
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	key := id.Provider + "\x00" + id.Subject
	if _, ok := u.s.idents[key]; ok {
		return store.ErrConflict
	}
	cp := *id
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	u.s.idents[key] = cp
	*id = cp
	return nil
}

func (u userStore) GetIdentity(_ context.Context, provider, subject string) (*store.UserIdentity, error) {
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	id, ok := u.s.idents[provider+"\x00"+subject]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := id
	return &cp, nil
}

type sessionStore struct{ s *Store }

func (ss sessionStore) Create(_ context.Context, ses *store.Session) error {
	if ses == nil || ses.UserID == "" || ses.TenantID == "" || ses.Hash == "" {
		return store.ErrInvalid
	}
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	if _, ok := ss.s.sessionHash[ses.Hash]; ok {
		return store.ErrConflict
	}
	cp := *ses
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	if _, ok := ss.s.sessions[cp.ID]; ok {
		return store.ErrConflict
	}
	ss.s.sessions[cp.ID] = cp
	ss.s.sessionHash[cp.Hash] = cp.ID
	*ses = cp
	return nil
}

func (ss sessionStore) GetByHash(_ context.Context, hash string) (*store.Session, error) {
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	id, ok := ss.s.sessionHash[hash]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := ss.s.sessions[id]
	return &cp, nil
}

func (ss sessionStore) GetByID(_ context.Context, id string) (*store.Session, error) {
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	cp, ok := ss.s.sessions[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &cp, nil
}

func (ss sessionStore) Revoke(_ context.Context, id string, at time.Time) error {
	ss.s.mu.Lock()
	defer ss.s.mu.Unlock()
	ses, ok := ss.s.sessions[id]
	if !ok {
		return store.ErrNotFound
	}
	if ses.RevokedAt == nil {
		ts := at.UTC()
		ses.RevokedAt = &ts
		ss.s.sessions[id] = ses
	}
	return nil
}

type tokenStore struct{ s *Store }

func (t tokenStore) Create(_ context.Context, tok *store.Token) error {
	if tok == nil || tok.TenantID == "" || tok.Hash == "" || tok.Kind == "" {
		return store.ErrInvalid
	}
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	hk := string(tok.Kind) + "|" + tok.Hash
	if _, ok := t.s.tokenHash[hk]; ok {
		return store.ErrConflict
	}
	cp := *tok
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	if _, ok := t.s.tokens[cp.ID]; ok {
		return store.ErrConflict
	}
	t.s.tokens[cp.ID] = cp
	t.s.tokenHash[hk] = cp.ID
	*tok = cp
	return nil
}

func (t tokenStore) GetByID(_ context.Context, tenantID, id string) (*store.Token, error) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	tok, ok := t.s.tokens[id]
	if !ok || tok.TenantID != tenantID {
		return nil, store.ErrNotFound
	}
	cp := tok
	return &cp, nil
}

func (t tokenStore) GetByHash(_ context.Context, kind store.TokenKind, hash string) (*store.Token, error) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	id, ok := t.s.tokenHash[string(kind)+"|"+hash]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := t.s.tokens[id]
	return &cp, nil
}

func (t tokenStore) ListByTenant(_ context.Context, tenantID string, kind store.TokenKind) ([]store.Token, error) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	out := make([]store.Token, 0)
	for _, tok := range t.s.tokens {
		if tok.TenantID == tenantID && tok.Kind == kind {
			out = append(out, tok)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (t tokenStore) Revoke(_ context.Context, tenantID, id string, at time.Time) error {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	tok, ok := t.s.tokens[id]
	if !ok || tok.TenantID != tenantID {
		return store.ErrNotFound
	}
	tok.RevokedAt = &at
	t.s.tokens[id] = tok
	return nil
}

func (t tokenStore) TouchLastUsed(_ context.Context, tenantID, id string, at time.Time) error {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	tok, ok := t.s.tokens[id]
	if !ok || tok.TenantID != tenantID {
		return store.ErrNotFound
	}
	tok.LastUsedAt = &at
	t.s.tokens[id] = tok
	return nil
}

type machineStore struct{ s *Store }

func machineKey(tenantID, machineID string) string { return tenantID + "|" + machineID }

func (m machineStore) Upsert(_ context.Context, mach *store.Machine) error {
	if mach == nil || mach.TenantID == "" || mach.ID == "" {
		return store.ErrInvalid
	}
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	cp := *mach
	m.s.machines[machineKey(cp.TenantID, cp.ID)] = cp
	return nil
}

func (m machineStore) Get(_ context.Context, tenantID, machineID string) (*store.Machine, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	mach, ok := m.s.machines[machineKey(tenantID, machineID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := mach
	return &cp, nil
}

func (m machineStore) ListByTenant(_ context.Context, tenantID string) ([]store.Machine, error) {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	out := make([]store.Machine, 0)
	for _, mach := range m.s.machines {
		if mach.TenantID == tenantID {
			out = append(out, mach)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type policyStore struct{ s *Store }

func (p policyStore) Get(_ context.Context, tenantID, machineID string) (*store.Policy, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	pol, ok := p.s.policies[machineKey(tenantID, machineID)]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := pol
	if pol.Document != nil {
		cp.Document = append(json.RawMessage(nil), pol.Document...)
	}
	return &cp, nil
}

func (p policyStore) Put(_ context.Context, pol *store.Policy, expectedVersion int32) error {
	if pol == nil || pol.TenantID == "" || pol.MachineID == "" {
		return store.ErrInvalid
	}
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	key := machineKey(pol.TenantID, pol.MachineID)
	cur, ok := p.s.policies[key]
	if !ok {
		if expectedVersion != 0 {
			return store.ErrConflict
		}
		cp := *pol
		cp.Version = 1
		if cp.UpdatedAt.IsZero() {
			cp.UpdatedAt = time.Now().UTC()
		}
		p.s.policies[key] = cp
		*pol = cp
		return nil
	}
	if cur.Version != expectedVersion {
		return store.ErrConflict
	}
	cp := *pol
	cp.Version = cur.Version + 1
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = time.Now().UTC()
	}
	p.s.policies[key] = cp
	*pol = cp
	return nil
}

type jobStore struct{ s *Store }

func (j jobStore) Create(_ context.Context, job *store.Job) error {
	if job == nil || job.TenantID == "" || job.MachineID == "" {
		return store.ErrInvalid
	}
	if job.ExpiresAt.IsZero() {
		return store.ErrInvalid
	}
	if len(job.Payload) > store.JobIOMaxBytes {
		return store.ErrInvalid
	}
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	cp := *job
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.Status == "" {
		cp.Status = store.JobPending
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	cp.ExpiresAt = cp.ExpiresAt.UTC()
	if _, ok := j.s.jobs[cp.ID]; ok {
		return store.ErrConflict
	}
	j.s.jobs[cp.ID] = cp
	*job = cp
	return nil
}

func (j jobStore) Get(_ context.Context, tenantID, id string) (*store.Job, error) {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	job, ok := j.s.jobs[id]
	if !ok || job.TenantID != tenantID {
		return nil, store.ErrNotFound
	}
	cp := job
	return &cp, nil
}

func (j jobStore) ListByMachine(_ context.Context, tenantID, machineID string) ([]store.Job, error) {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	out := make([]store.Job, 0)
	for _, job := range j.s.jobs {
		if job.TenantID == tenantID && job.MachineID == machineID {
			out = append(out, job)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].CreatedAt.Equal(out[k].CreatedAt) {
			return out[i].ID < out[k].ID
		}
		return out[i].CreatedAt.Before(out[k].CreatedAt)
	})
	return out, nil
}

func (j jobStore) Claim(_ context.Context, tenantID, machineID, id string, at time.Time) (*store.Job, error) {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	job, ok := j.s.jobs[id]
	if !ok || job.TenantID != tenantID || job.MachineID != machineID {
		return nil, store.ErrNotFound
	}
	at = at.UTC()
	if job.Status == store.JobPending && !job.ExpiresAt.After(at) {
		job.Status = store.JobTimedOut
		job.FinishedAt = &at
		j.s.jobs[id] = job
		return nil, store.ErrConflict
	}
	if job.Status != store.JobPending {
		return nil, store.ErrConflict
	}
	job.Status = store.JobRunning
	job.ClaimedAt = &at
	j.s.jobs[id] = job
	cp := job
	return &cp, nil
}

func (j jobStore) Complete(_ context.Context, tenantID, machineID, id string, result store.JobResult) error {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	job, ok := j.s.jobs[id]
	if !ok || job.TenantID != tenantID || job.MachineID != machineID {
		return store.ErrNotFound
	}
	if job.Status != store.JobRunning {
		return store.ErrConflict
	}
	if len(result.Stdout) > store.JobIOMaxBytes || len(result.Stderr) > store.JobIOMaxBytes {
		return store.ErrInvalid
	}
	switch result.Status {
	case store.JobSucceeded, store.JobFailed:
	default:
		return store.ErrInvalid
	}
	job.Status = result.Status
	job.ExitCode = result.ExitCode
	job.Stdout = result.Stdout
	job.Stderr = result.Stderr
	job.DurationMS = result.DurationMS
	fin := result.FinishedAt.UTC()
	job.FinishedAt = &fin
	j.s.jobs[id] = job
	return nil
}

func (j jobStore) Cancel(_ context.Context, tenantID, id string, at time.Time) error {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	job, ok := j.s.jobs[id]
	if !ok || job.TenantID != tenantID {
		return store.ErrNotFound
	}
	if job.Status != store.JobPending {
		return store.ErrConflict
	}
	at = at.UTC()
	job.Status = store.JobCanceled
	job.FinishedAt = &at
	j.s.jobs[id] = job
	return nil
}

func (j jobStore) ListPendingIDs(_ context.Context, tenantID, machineID string, now time.Time) ([]string, error) {
	j.s.mu.Lock()
	defer j.s.mu.Unlock()
	now = now.UTC()
	out := make([]string, 0)
	for id, job := range j.s.jobs {
		if job.TenantID != tenantID || job.MachineID != machineID || job.Status != store.JobPending {
			continue
		}
		if !job.ExpiresAt.After(now) {
			job.Status = store.JobTimedOut
			fin := now
			job.FinishedAt = &fin
			j.s.jobs[id] = job
			continue
		}
		out = append(out, job.ID)
	}
	sort.Strings(out)
	return out, nil
}

type eventStore struct{ s *Store }

func eventKey(tenantID, machineID, eventID string) string {
	return tenantID + "|" + machineID + "|" + eventID
}

func (e eventStore) InsertBatch(_ context.Context, events []store.Event) error {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	seen := make(map[string]struct{}, len(events))
	for i := range events {
		ev := events[i]
		if ev.TenantID == "" || ev.MachineID == "" || ev.EventID == "" {
			return store.ErrInvalid
		}
		key := eventKey(ev.TenantID, ev.MachineID, ev.EventID)
		if _, ok := seen[key]; ok {
			continue // within-batch idempotent skip
		}
		seen[key] = struct{}{}
		if _, ok := e.s.events[key]; ok {
			continue // across-batch idempotent skip
		}
		if ev.ID == "" {
			ev.ID = uuid.NewString()
		}
		e.s.events[key] = ev
		events[i] = ev
	}
	return nil
}

func (e eventStore) List(_ context.Context, q store.EventQuery) (store.EventPage, error) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()

	var cursorTS time.Time
	var cursorEventID string
	if q.Cursor != "" {
		ts, id, err := store.DecodeEventCursor(q.Cursor)
		if err != nil {
			return store.EventPage{}, err
		}
		cursorTS, cursorEventID = ts, id
	}

	out := make([]store.Event, 0)
	for _, ev := range e.s.events {
		if ev.TenantID != q.TenantID || ev.MachineID != q.MachineID {
			continue
		}
		if q.Type != "" && ev.Type != q.Type {
			continue
		}
		if q.From != nil && ev.TS.Before(*q.From) {
			continue
		}
		if q.To != nil && !ev.TS.Before(*q.To) {
			continue
		}
		if q.Cursor != "" && !store.EventAfterCursor(ev, cursorTS, cursorEventID) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		return store.CompareEvents(out[i], out[j]) < 0
	})

	limit := q.Limit
	if limit <= 0 {
		limit = store.EventListDefaultLimit
	}
	page := store.EventPage{Events: out}
	if len(out) > limit {
		last := out[limit-1]
		page.Events = out[:limit]
		page.NextCursor = store.EncodeEventCursor(last.TS, last.EventID)
	}
	return page, nil
}

type auditStore struct{ s *Store }

func (a auditStore) Append(_ context.Context, entry *store.AuditEntry) error {
	if entry == nil || entry.TenantID == "" || entry.Action == "" {
		return store.ErrInvalid
	}
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	cp := *entry
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	if cp.Details != nil {
		cp.Details = copyStringMap(cp.Details)
	}
	a.s.audits = append(a.s.audits, cp)
	*entry = cp
	return nil
}

func (a auditStore) ListByTenant(_ context.Context, tenantID string, limit int) ([]store.AuditEntry, error) {
	a.s.mu.Lock()
	defer a.s.mu.Unlock()
	out := make([]store.AuditEntry, 0)
	for _, e := range a.s.audits {
		if e.TenantID == tenantID {
			cp := e
			if e.Details != nil {
				cp.Details = copyStringMap(e.Details)
			}
			out = append(out, cp)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func copyStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type factStore struct{ s *Store }

func factKey(tenantID, machineID, streamID string, seq int64, idx int32) string {
	return tenantID + "|" + machineID + "|" + streamID + "|" +
		strconv.FormatInt(seq, 10) + "|" + strconv.FormatInt(int64(idx), 10)
}

func (f factStore) InsertBatch(_ context.Context, facts []store.Fact) (int, error) {
	f.s.mu.Lock()
	defer f.s.mu.Unlock()
	inserted := 0
	for i := range facts {
		ft := facts[i]
		if ft.TenantID == "" || ft.MachineID == "" || ft.StreamID == "" {
			return inserted, store.ErrInvalid
		}
		key := factKey(ft.TenantID, ft.MachineID, ft.StreamID, ft.Seq, ft.Idx)
		if _, ok := f.s.facts[key]; ok {
			continue
		}
		if ft.ID == "" {
			ft.ID = uuid.NewString()
		}
		if ft.Data != nil {
			ft.Data = append(json.RawMessage(nil), ft.Data...)
		}
		f.s.facts[key] = ft
		facts[i] = ft
		inserted++
	}
	if inserted > 0 && f.s.AfterFactInsertErr != nil {
		return inserted, f.s.AfterFactInsertErr
	}
	return inserted, nil
}

// FactCount returns the number of stored facts (test helper).
func (s *Store) FactCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.facts)
}

type usageStore struct{ s *Store }

func (u usageStore) Apply(_ context.Context, tenantID, machineID, streamID string, seq int64, deltas []store.UsageDelta) (int, error) {
	if tenantID == "" || machineID == "" || streamID == "" {
		return 0, store.ErrInvalid
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	updated := 0
	for _, d := range deltas {
		if d.Date == "" || d.Bucket == "" || d.Seconds == 0 {
			continue
		}
		id := tenantID + "|" + machineID + "|" + d.Date + "|" + d.Bucket
		row, ok := u.s.usage[id]
		if !ok {
			row = usageRow{
				UsageDaily: store.UsageDaily{
					TenantID: tenantID, MachineID: machineID,
					Date: d.Date, TimeZone: d.TimeZone, Bucket: d.Bucket,
				},
				HW: make(map[string]int64),
			}
		}
		if row.HW == nil {
			row.HW = make(map[string]int64)
		}
		if hw, exists := row.HW[streamID]; exists && hw >= seq {
			u.s.usage[id] = row
			continue // already applied
		}
		row.Seconds += d.Seconds
		row.TimeZone = d.TimeZone
		row.HW[streamID] = seq
		u.s.usage[id] = row
		updated++
	}
	return updated, nil
}

func (u usageStore) ListByMachine(_ context.Context, tenantID, machineID, date string) ([]store.UsageDaily, error) {
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	out := make([]store.UsageDaily, 0)
	for _, row := range u.s.usage {
		if row.TenantID != tenantID || row.MachineID != machineID || row.Date != date {
			continue
		}
		cp := row.UsageDaily
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bucket < out[j].Bucket })
	return out, nil
}
