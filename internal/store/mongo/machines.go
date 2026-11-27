package mongo

import (
	"context"
	"errors"

	"github.com/barzoj/dad-joke/internal/store"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type machineStore struct{ s *Store }

func (m machineStore) Upsert(ctx context.Context, mach *store.Machine) error {
	if mach == nil || mach.TenantID == "" || mach.ID == "" {
		return store.ErrInvalid
	}
	doc := machineToDoc(mach)
	_, err := m.s.coll(collMachines).ReplaceOne(ctx,
		bson.D{{Key: "_id", Value: doc.ID}},
		doc,
		options.Replace().SetUpsert(true),
	)
	return err
}

func (m machineStore) Get(ctx context.Context, tenantID, machineID string) (*store.Machine, error) {
	var d machineDoc
	err := m.s.coll(collMachines).FindOne(ctx, bson.D{
		{Key: "_id", Value: machineKey(tenantID, machineID)},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return machineFromDoc(d), nil
}

func (m machineStore) ListByTenant(ctx context.Context, tenantID string) ([]store.Machine, error) {
	cur, err := m.s.coll(collMachines).Find(ctx,
		bson.D{{Key: "tenant_id", Value: tenantID}},
		options.Find().SetSort(bson.D{{Key: "machine_id", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.Machine, 0)
	for cur.Next(ctx) {
		var d machineDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, *machineFromDoc(d))
	}
	return out, cur.Err()
}
