package config

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestNewMachineIDFormatVersionVariant(t *testing.T) {
	t.Parallel()
	ids := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		id, err := NewMachineID()
		if err != nil {
			t.Fatalf("NewMachineID: %v", err)
		}
		if err := ValidateMachineID(id); err != nil {
			t.Fatalf("ValidateMachineID(%q): %v", id, err)
		}
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate machine_id %q", id)
		}
		ids[id] = struct{}{}
	}
}

func TestValidateMachineIDRejectsBad(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"not-a-uuid",
		"550e8400-e29b-11d4-a716-446655440000", // version 1
		"550e8400-e29b-41d4-c716-446655440000", // bad variant
		"550e8400e29b41d4a716446655440000",     // missing dashes
		"550e8400-e29b-41d4-a716-44665544000",  // too short
	}
	for _, s := range cases {
		if err := ValidateMachineID(s); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ValidateMachineID(%q) = %v, want ErrInvalid", s, err)
		}
	}
	// Known-good UUIDv4.
	if err := ValidateMachineID("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Fatalf("valid id rejected: %v", err)
	}
}

func TestNewTokenEntropyAndUniqueness(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 32)
	for i := 0; i < 32; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if len(tok) != TokenHexLen {
			t.Fatalf("len = %d, want %d", len(tok), TokenHexLen)
		}
		raw, err := hex.DecodeString(tok)
		if err != nil {
			t.Fatalf("hex decode: %v", err)
		}
		if len(raw) != TokenByteLen {
			t.Fatalf("decoded len = %d, want %d", len(raw), TokenByteLen)
		}
		if tok != strings.ToLower(tok) {
			t.Fatalf("token not lowercase hex: %q", tok)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token")
		}
		seen[tok] = struct{}{}
	}
}

func TestValidateTokenArg(t *testing.T) {
	t.Parallel()
	if err := ValidateTokenArg(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if err := ValidateTokenArg("   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("whitespace: %v", err)
	}
	if err := ValidateTokenArg("backend-issued-token-value"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	// Verbatim acceptance includes unusual lengths / alphabets.
	if err := ValidateTokenArg("short"); err != nil {
		t.Fatalf("short supplied token: %v", err)
	}
}
