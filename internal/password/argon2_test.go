package password_test

import (
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/internal/password"
)

func TestHashAndVerify(t *testing.T) {
	t.Parallel()
	hash, err := password.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("unexpected encoding: %s", hash)
	}
	ok, err := password.Verify(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify good: ok=%v err=%v", ok, err)
	}
	ok, err = password.Verify(hash, "wrong password")
	if err != nil || ok {
		t.Fatalf("verify bad: ok=%v err=%v", ok, err)
	}
}

func TestVerifyOrDummy(t *testing.T) {
	t.Parallel()
	hash, err := password.Hash("secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if !password.VerifyOrDummy(hash, "secret-pass") {
		t.Fatal("expected match")
	}
	if password.VerifyOrDummy(hash, "nope") {
		t.Fatal("expected mismatch")
	}
	if password.VerifyOrDummy("", "anything") {
		t.Fatal("empty hash must never match")
	}
	if password.VerifyOrDummy("not-a-hash", "anything") {
		t.Fatal("invalid hash must never match")
	}
}

func TestVerifyInvalidHash(t *testing.T) {
	t.Parallel()
	_, err := password.Verify("garbage", "x")
	if err == nil {
		t.Fatal("expected error")
	}
}
