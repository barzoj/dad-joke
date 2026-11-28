// Package mongo implements store.Store with the official MongoDB Go driver.
//
// BSON / ObjectID types stay private to this package. Callers use only
// github.com/barzoj/dad-joke/internal/store domain models and interfaces.
package mongo

import (
	"context"
	"fmt"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const (
	defaultDB = "dadjoke"

	collTenants        = "tenants"
	collUsers          = "users"
	collUserIdentities = "user_identities"
	collSessions       = "sessions"
	collTokens         = "tokens"
	collMachines       = "machines"
	collPolicies       = "policies"
	collJobs           = "jobs"
	collEvents         = "events"
	collFacts          = "facts"
	collUsageDaily     = "usage_daily"
	collAuditLogs      = "audit_logs"
)

// Config configures a MongoDB-backed Store.
type Config struct {
	URI                string
	Database           string
	FactsRetentionDays int // 1..365; 0 → store.FactRetentionDaysDefault
}

// Store is a MongoDB-backed store.Store.
type Store struct {
	client             *mongo.Client
	db                 *mongo.Database
	factsRetentionDays int
}

// Open connects to MongoDB. Call EnsureIndexes after Open before serving traffic.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.URI == "" {
		return nil, fmt.Errorf("mongo: URI is required")
	}
	dbName := cfg.Database
	if dbName == "" {
		dbName = defaultDB
	}
	retention := cfg.FactsRetentionDays
	if retention == 0 {
		retention = store.FactRetentionDaysDefault
	}
	if retention < store.FactRetentionDaysMin || retention > store.FactRetentionDaysMax {
		return nil, fmt.Errorf("mongo: FactsRetentionDays must be %d..%d", store.FactRetentionDaysMin, store.FactRetentionDaysMax)
	}
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.URI))
	if err != nil {
		return nil, fmt.Errorf("mongo: connect: %w", err)
	}
	// Do not nest a shorter ping deadline when the caller already set one
	// (e.g. 30s test budgets under parallel go test load).
	pingCtx, cancel := pingContext(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo: ping: %w", err)
	}
	return &Store{
		client:             client,
		db:                 client.Database(dbName),
		factsRetentionDays: retention,
	}, nil
}

// FactsRetentionDays returns the configured facts TTL retention in days.
func (s *Store) FactsRetentionDays() int {
	if s == nil || s.factsRetentionDays == 0 {
		return store.FactRetentionDaysDefault
	}
	return s.factsRetentionDays
}

// pingContext returns ctx unchanged when it already has a deadline; otherwise
// wraps it with a defaultTimeout so production Open still fails closed promptly.
func pingContext(ctx context.Context, defaultTimeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

// Ping implements store.Pinger.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("mongo: not connected")
	}
	return s.client.Ping(ctx, readpref.Primary())
}

// Close disconnects the client.
func (s *Store) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Disconnect(ctx)
}

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

func (s *Store) coll(name string) *mongo.Collection {
	return s.db.Collection(name)
}

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	if mongo.IsDuplicateKeyError(err) {
		return store.ErrConflict
	}
	return err
}
