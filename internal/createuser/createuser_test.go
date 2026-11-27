package createuser_test

import (
	"context"
	"errors"
	"testing"

	"github.com/barzoj/dad-joke/internal/createuser"
	"github.com/barzoj/dad-joke/internal/password"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestCreateUser(t *testing.T) {
	t.Parallel()
	st := memory.New()
	email := "cli-" + uuid.NewString()[:8] + "@example.com"
	res, err := createuser.Run(context.Background(), st, email, "password123", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.UserID == "" || res.TenantID == "" {
		t.Fatalf("empty ids: %+v", res)
	}
	user, err := st.Users().GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := password.Verify(user.PasswordHash, "password123")
	if err != nil || !ok {
		t.Fatalf("hash verify: ok=%v err=%v", ok, err)
	}
}

func TestCreateUserConflictRollsBackTenant(t *testing.T) {
	t.Parallel()
	st := memory.New()
	email := "cli-dup-" + uuid.NewString()[:8] + "@example.com"
	if _, err := createuser.Run(context.Background(), st, email, "password123", "t1"); err != nil {
		t.Fatal(err)
	}
	_, err := createuser.Run(context.Background(), st, email, "password123", "t2")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if _, err := st.Tenants().GetByName(context.Background(), "t2"); err == nil {
		t.Fatal("orphan tenant")
	}
}

func TestCreateUserValidation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	if _, err := createuser.Run(context.Background(), st, "", "password123", ""); err == nil {
		t.Fatal("expected email error")
	}
	if _, err := createuser.Run(context.Background(), st, "a@b.c", "short", ""); err == nil {
		t.Fatal("expected password error")
	}
}
