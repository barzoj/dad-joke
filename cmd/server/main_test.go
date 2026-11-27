package main

import (
	"testing"
)

func TestBootstrapRequiresTenant(t *testing.T) {
	t.Parallel()
	if code := run([]string{"bootstrap"}); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if code := run([]string{"bootstrap", "--tenant", ""}); code != 2 {
		t.Fatalf("empty tenant code = %d, want 2", code)
	}
}

func TestCreateUserRequiresEmailAndPassword(t *testing.T) {
	t.Parallel()
	if code := run([]string{"create-user"}); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if code := run([]string{"create-user", "--email", "a@b.c"}); code != 2 {
		t.Fatalf("missing password code = %d, want 2", code)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("DAD_JOKE_TEST_ENV_OR", "set")
	if got := envOr("DAD_JOKE_TEST_ENV_OR", "fallback"); got != "set" {
		t.Fatalf("got %q", got)
	}
	if got := envOr("DAD_JOKE_TEST_ENV_OR_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("DAD_JOKE_TEST_BOOL", "false")
	if envBool("DAD_JOKE_TEST_BOOL", true) {
		t.Fatal("expected false")
	}
	t.Setenv("DAD_JOKE_TEST_BOOL", "true")
	if !envBool("DAD_JOKE_TEST_BOOL", false) {
		t.Fatal("expected true")
	}
	if !envBool("DAD_JOKE_TEST_BOOL_MISSING", true) {
		t.Fatal("expected default true")
	}
}
