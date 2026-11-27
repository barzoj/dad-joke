package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type sessionStore struct{ s *Store }

func (ss sessionStore) Create(ctx context.Context, ses *store.Session) error {
	if ses == nil || ses.UserID == "" || ses.TenantID == "" || ses.Hash == "" {
		return store.ErrInvalid
	}
	cp := *ses
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := ss.s.coll(collSessions).InsertOne(ctx, sessionToDoc(&cp))
	if err != nil {
		return mapWriteError(err)
	}
	*ses = cp
	return nil
}

func (ss sessionStore) GetByHash(ctx context.Context, hash string) (*store.Session, error) {
	var d sessionDoc
	err := ss.s.coll(collSessions).FindOne(ctx, bson.D{{Key: "hash", Value: hash}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return sessionFromDoc(d), nil
}

func (ss sessionStore) GetByID(ctx context.Context, id string) (*store.Session, error) {
	var d sessionDoc
	err := ss.s.coll(collSessions).FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return sessionFromDoc(d), nil
}

func (ss sessionStore) Revoke(ctx context.Context, id string, at time.Time) error {
	res, err := ss.s.coll(collSessions).UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: id},
			{Key: "revoked_at", Value: bson.D{{Key: "$exists", Value: false}}},
		},
		bson.D{{Key: "$set", Value: bson.D{{Key: "revoked_at", Value: at.UTC()}}}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		// Already revoked or missing: treat missing as not found; already revoked as OK.
		_, err := ss.GetByID(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return store.ErrNotFound
		}
		return err
	}
	return nil
}
