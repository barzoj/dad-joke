package mongo

import (
	"context"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type auditStore struct{ s *Store }

func (a auditStore) Append(ctx context.Context, entry *store.AuditEntry) error {
	if entry == nil || entry.TenantID == "" || entry.Action == "" {
		return store.ErrInvalid
	}
	cp := *entry
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := a.s.coll(collAuditLogs).InsertOne(ctx, auditToDoc(&cp))
	if err != nil {
		return err
	}
	*entry = cp
	return nil
}

func (a auditStore) ListByTenant(ctx context.Context, tenantID string, limit int) ([]store.AuditEntry, error) {
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})
	if limit > 0 {
		opts.SetLimit(int64(limit))
	}
	cur, err := a.s.coll(collAuditLogs).Find(ctx, bson.D{{Key: "tenant_id", Value: tenantID}}, opts)
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.AuditEntry, 0)
	for cur.Next(ctx) {
		var d auditDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, *auditFromDoc(d))
	}
	return out, cur.Err()
}
