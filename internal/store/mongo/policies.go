package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type policyStore struct{ s *Store }

func (p policyStore) Get(ctx context.Context, tenantID, machineID string) (*store.Policy, error) {
	var d policyDoc
	err := p.s.coll(collPolicies).FindOne(ctx, bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "machine_id", Value: machineID},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return policyFromDoc(d), nil
}

func (p policyStore) Put(ctx context.Context, pol *store.Policy, expectedVersion int32) error {
	if pol == nil || pol.TenantID == "" || pol.MachineID == "" {
		return store.ErrInvalid
	}
	now := time.Now().UTC()
	if expectedVersion == 0 {
		cp := *pol
		cp.Version = 1
		if cp.UpdatedAt.IsZero() {
			cp.UpdatedAt = now
		}
		_, err := p.s.coll(collPolicies).InsertOne(ctx, policyToDoc(&cp))
		if err != nil {
			return mapWriteError(err)
		}
		*pol = cp
		return nil
	}

	cp := *pol
	cp.Version = expectedVersion + 1
	if cp.UpdatedAt.IsZero() {
		cp.UpdatedAt = now
	}
	res, err := p.s.coll(collPolicies).ReplaceOne(ctx,
		bson.D{
			{Key: "tenant_id", Value: cp.TenantID},
			{Key: "machine_id", Value: cp.MachineID},
			{Key: "version", Value: expectedVersion},
		},
		policyToDoc(&cp),
		options.Replace(),
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return store.ErrConflict
	}
	*pol = cp
	return nil
}
