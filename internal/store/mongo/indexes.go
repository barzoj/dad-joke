package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// EnsureIndexes creates idempotent indexes required by the DAL contract.
func (s *Store) EnsureIndexes(ctx context.Context) error {
	specs := []struct {
		coll   string
		models []mongo.IndexModel
	}{
		{
			coll: collTenants,
			models: []mongo.IndexModel{{
				Keys:    bson.D{{Key: "name", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("uniq_tenant_name"),
			}},
		},
		{
			coll: collUsers,
			models: []mongo.IndexModel{
				{
					Keys:    bson.D{{Key: "email", Value: 1}},
					Options: options.Index().SetUnique(true).SetName("uniq_user_email"),
				},
				{
					Keys:    bson.D{{Key: "tenant_id", Value: 1}},
					Options: options.Index().SetName("idx_users_tenant"),
				},
			},
		},
		{
			coll: collUserIdentities,
			models: []mongo.IndexModel{{
				Keys: bson.D{
					{Key: "provider", Value: 1},
					{Key: "subject", Value: 1},
				},
				Options: options.Index().SetUnique(true).SetName("uniq_identity_provider_subject"),
			}},
		},
		{
			coll: collTokens,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "kind", Value: 1},
						{Key: "hash", Value: 1},
					},
					Options: options.Index().SetUnique(true).SetName("uniq_token_kind_hash"),
				},
				{
					Keys: bson.D{
						{Key: "tenant_id", Value: 1},
						{Key: "kind", Value: 1},
					},
					Options: options.Index().SetName("idx_tokens_tenant_kind"),
				},
			},
		},
		{
			coll: collMachines,
			models: []mongo.IndexModel{{
				Keys: bson.D{
					{Key: "tenant_id", Value: 1},
					{Key: "_id", Value: 1},
				},
				Options: options.Index().SetName("idx_machines_tenant_id"),
			}},
		},
		{
			coll: collPolicies,
			models: []mongo.IndexModel{{
				Keys: bson.D{
					{Key: "tenant_id", Value: 1},
					{Key: "machine_id", Value: 1},
				},
				Options: options.Index().SetUnique(true).SetName("uniq_policy_tenant_machine"),
			}},
		},
		{
			coll: collJobs,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "tenant_id", Value: 1},
						{Key: "machine_id", Value: 1},
						{Key: "status", Value: 1},
						{Key: "expires_at", Value: 1},
					},
					Options: options.Index().SetName("idx_jobs_pending"),
				},
			},
		},
		{
			coll: collEvents,
			models: []mongo.IndexModel{
				{
					Keys: bson.D{
						{Key: "tenant_id", Value: 1},
						{Key: "machine_id", Value: 1},
						{Key: "event_id", Value: 1},
					},
					Options: options.Index().SetUnique(true).SetName("uniq_event_idempotency"),
				},
				{
					Keys: bson.D{
						{Key: "tenant_id", Value: 1},
						{Key: "machine_id", Value: 1},
						{Key: "ts", Value: 1},
					},
					Options: options.Index().SetName("idx_events_tenant_machine_ts"),
				},
				{
					Keys:    bson.D{{Key: "ts", Value: 1}},
					Options: options.Index().SetExpireAfterSeconds(30 * 24 * 60 * 60).SetName("ttl_events_ts"),
				},
			},
		},
		{
			coll: collAuditLogs,
			models: []mongo.IndexModel{{
				Keys: bson.D{
					{Key: "tenant_id", Value: 1},
					{Key: "created_at", Value: 1},
				},
				Options: options.Index().SetName("idx_audit_tenant_created"),
			}},
		},
	}

	for _, spec := range specs {
		if _, err := s.coll(spec.coll).Indexes().CreateMany(ctx, spec.models); err != nil {
			return fmt.Errorf("mongo: EnsureIndexes %s: %w", spec.coll, err)
		}
	}
	return nil
}
