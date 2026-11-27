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

type userStore struct{ s *Store }

func (u userStore) Create(ctx context.Context, user *store.User) error {
	if user == nil || user.TenantID == "" || strings.TrimSpace(user.Email) == "" {
		return store.ErrInvalid
	}
	cp := *user
	cp.Email = strings.ToLower(strings.TrimSpace(user.Email))
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := u.s.coll(collUsers).InsertOne(ctx, userToDoc(&cp))
	if err != nil {
		return mapWriteError(err)
	}
	*user = cp
	return nil
}

func (u userStore) GetByID(ctx context.Context, tenantID, id string) (*store.User, error) {
	var d userDoc
	err := u.s.coll(collUsers).FindOne(ctx, bson.D{
		{Key: "_id", Value: id},
		{Key: "tenant_id", Value: tenantID},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromDoc(d), nil
}

func (u userStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	var d userDoc
	err := u.s.coll(collUsers).FindOne(ctx, bson.D{
		{Key: "email", Value: strings.ToLower(strings.TrimSpace(email))},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return userFromDoc(d), nil
}

func (u userStore) CreateIdentity(ctx context.Context, id *store.UserIdentity) error {
	if id == nil || id.UserID == "" || id.Provider == "" || id.Subject == "" {
		return store.ErrInvalid
	}
	cp := *id
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := u.s.coll(collUserIdentities).InsertOne(ctx, identityToDoc(&cp))
	if err != nil {
		return mapWriteError(err)
	}
	*id = cp
	return nil
}

func (u userStore) GetIdentity(ctx context.Context, provider, subject string) (*store.UserIdentity, error) {
	var d userIdentityDoc
	err := u.s.coll(collUserIdentities).FindOne(ctx, bson.D{
		{Key: "provider", Value: provider},
		{Key: "subject", Value: subject},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return identityFromDoc(d), nil
}
