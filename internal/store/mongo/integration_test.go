package mongo

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func mongoURI(t *testing.T) string {
	t.Helper()
	uri := strings.TrimSpace(os.Getenv("DAD_JOKE_MONGODB_URI"))
	if uri == "" {
		t.Skip("DAD_JOKE_MONGODB_URI not set")
	}
	return uri
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	uri := mongoURI(t)
	dbName := "dadjoke_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	st, err := Open(ctx, Config{URI: uri, Database: dbName})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = st.db.Drop(dropCtx)
		_ = st.Close(context.Background())
	})
	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	return st
}

func TestIntegration_EnsureIndexesIdempotent(t *testing.T) {
	st := openTestStore(t)
	if err := st.EnsureIndexes(context.Background()); err != nil {
		t.Fatalf("second EnsureIndexes: %v", err)
	}
}

func TestIntegration_TenantTokenMachineAuditCRUD(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	ten := &store.Tenant{Name: "tenant-a"}
	if err := st.Tenants().Create(ctx, ten); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	if err := st.Tenants().Create(ctx, &store.Tenant{Name: "tenant-a"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate tenant err = %v", err)
	}
	got, err := st.Tenants().GetByName(ctx, "tenant-a")
	if err != nil || got.ID != ten.ID {
		t.Fatalf("GetByName: %v %+v", err, got)
	}

	_, prefix, hash, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		t.Fatal(err)
	}
	tok := &store.Token{
		TenantID: ten.ID, Kind: store.TokenKindPAT, Name: "k", Prefix: prefix, Hash: hash,
	}
	if err := st.Tokens().Create(ctx, tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := st.Tokens().Create(ctx, &store.Token{
		TenantID: ten.ID, Kind: store.TokenKindPAT, Name: "k2", Prefix: prefix, Hash: hash,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate hash err = %v", err)
	}
	byHash, err := st.Tokens().GetByHash(ctx, store.TokenKindPAT, hash)
	if err != nil || byHash.ID != tok.ID {
		t.Fatalf("GetByHash: %v %+v", err, byHash)
	}

	mach := &store.Machine{
		ID: uuid.NewString(), TenantID: ten.ID, Hostname: "host",
		OSVersion: "win", AgentVersion: "0.0.1", LastSeenAt: time.Now().UTC(),
	}
	if err := st.Machines().Upsert(ctx, mach); err != nil {
		t.Fatalf("upsert machine: %v", err)
	}
	gotM, err := st.Machines().Get(ctx, ten.ID, mach.ID)
	if err != nil || gotM.Hostname != "host" {
		t.Fatalf("Get machine: %v %+v", err, gotM)
	}

	if err := st.Audits().Append(ctx, &store.AuditEntry{
		TenantID: ten.ID, Action: "token.issue", ActorID: "test", Resource: "token:" + tok.ID,
		Details: map[string]string{"prefix": prefix},
	}); err != nil {
		t.Fatalf("audit: %v", err)
	}
	audits, err := st.Audits().ListByTenant(ctx, ten.ID, 10)
	if err != nil || len(audits) != 1 {
		t.Fatalf("list audits: %v %+v", err, audits)
	}
}

func TestIntegration_BootstrapRawKeyNeverPersisted(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	res, err := bootstrap.Run(ctx, st, "boot-tenant")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !strings.HasPrefix(res.RawKey, "dj_pat_") {
		t.Fatalf("raw key = %q", res.RawKey)
	}

	names, err := st.db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		t.Fatalf("ListCollectionNames: %v", err)
	}
	for _, name := range names {
		cur, err := st.db.Collection(name).Find(ctx, bson.D{})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		for cur.Next(ctx) {
			rawJSON, err := bson.MarshalExtJSON(cur.Current, false, false)
			if err != nil {
				_ = cur.Close(ctx)
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(rawJSON), res.RawKey) {
				_ = cur.Close(ctx)
				t.Fatalf("raw key found in collection %s document: %s", name, rawJSON)
			}
		}
		err = cur.Err()
		_ = cur.Close(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	tok, err := st.Tokens().GetByID(ctx, res.TenantID, res.TokenID)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Hash != tokenhash.Hash(res.RawKey) {
		t.Fatal("hash mismatch")
	}
}

func TestIntegration_EmptyTenantRejected(t *testing.T) {
	st := openTestStore(t)
	if err := st.Tenants().Create(context.Background(), &store.Tenant{Name: "  "}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
