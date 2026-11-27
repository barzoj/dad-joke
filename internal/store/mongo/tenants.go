package mongo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type tenantStore struct{ s *Store }

func (t tenantStore) Create(ctx context.Context, ten *store.Tenant) error {
	if ten == nil || strings.TrimSpace(ten.Name) == "" {
		return store.ErrInvalid
	}
	cp := *ten
	cp.Name = strings.TrimSpace(ten.Name)
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := t.s.coll(collTenants).InsertOne(ctx, tenantToDoc(&cp))
	if err != nil {
		return mapWriteError(err)
	}
	*ten = cp
	return nil
}

func (t tenantStore) GetByID(ctx context.Context, id string) (*store.Tenant, error) {
	var d tenantDoc
	err := t.s.coll(collTenants).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return tenantFromDoc(d), nil
}

func (t tenantStore) GetByName(ctx context.Context, name string) (*store.Tenant, error) {
	var d tenantDoc
	err := t.s.coll(collTenants).FindOne(ctx, bson.D{{Key: "name", Value: strings.TrimSpace(name)}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return tenantFromDoc(d), nil
}

func (t tenantStore) Delete(ctx context.Context, id string) error {
	res, err := t.s.coll(collTenants).DeleteOne(ctx, bson.D{{Key: "_id", Value: id}})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}
