package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type tokenStore struct{ s *Store }

func (t tokenStore) Create(ctx context.Context, tok *store.Token) error {
	if tok == nil || tok.TenantID == "" || tok.Hash == "" || tok.Kind == "" {
		return store.ErrInvalid
	}
	cp := *tok
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := t.s.coll(collTokens).InsertOne(ctx, tokenToDoc(&cp))
	if err != nil {
		return mapWriteError(err)
	}
	*tok = cp
	return nil
}

func (t tokenStore) GetByID(ctx context.Context, tenantID, id string) (*store.Token, error) {
	var d tokenDoc
	err := t.s.coll(collTokens).FindOne(ctx, bson.D{
		{Key: "_id", Value: id},
		{Key: "tenant_id", Value: tenantID},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return tokenFromDoc(d), nil
}

func (t tokenStore) GetByHash(ctx context.Context, kind store.TokenKind, hash string) (*store.Token, error) {
	var d tokenDoc
	err := t.s.coll(collTokens).FindOne(ctx, bson.D{
		{Key: "kind", Value: string(kind)},
		{Key: "hash", Value: hash},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return tokenFromDoc(d), nil
}

func (t tokenStore) ListByTenant(ctx context.Context, tenantID string, kind store.TokenKind) ([]store.Token, error) {
	cur, err := t.s.coll(collTokens).Find(ctx, bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "kind", Value: string(kind)},
	}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.Token, 0)
	for cur.Next(ctx) {
		var d tokenDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, *tokenFromDoc(d))
	}
	return out, cur.Err()
}

func (t tokenStore) Revoke(ctx context.Context, tenantID, id string, at time.Time) error {
	res, err := t.s.coll(collTokens).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "tenant_id", Value: tenantID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: at.UTC()}}}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (t tokenStore) TouchLastUsed(ctx context.Context, tenantID, id string, at time.Time) error {
	res, err := t.s.coll(collTokens).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "tenant_id", Value: tenantID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "last_used_at", Value: at.UTC()}}}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}
