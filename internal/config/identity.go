package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// TokenByteLen is the entropy size of an auto-generated token (256 bits).
const TokenByteLen = 32

// TokenHexLen is the lowercase-hex encoding length of an auto-generated token.
const TokenHexLen = TokenByteLen * 2 // 64

// RedactedToken is the placeholder used whenever a token must appear in logs
// or fmt output. The raw secret must never be printed by the service.
const RedactedToken = "[REDACTED]"

// NewMachineID returns a new RFC 4122 version-4 UUID string using crypto/rand.
func NewMachineID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate machine_id: %w", err)
	}
	// Set version (4) and variant (RFC 4122).
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// NewToken returns a 256-bit secret as 64-char lowercase hex (crypto/rand).
func NewToken() (string, error) {
	var b [TokenByteLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidateMachineID checks that s is a canonical RFC 4122 UUIDv4 string.
func ValidateMachineID(s string) error {
	if s == "" {
		return fmt.Errorf("%w: machine_id is required", ErrInvalid)
	}
	if len(s) != 36 {
		return fmt.Errorf("%w: machine_id must be a UUIDv4 string", ErrInvalid)
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return fmt.Errorf("%w: machine_id must be a UUIDv4 string", ErrInvalid)
			}
		default:
			if !isHexChar(c) {
				return fmt.Errorf("%w: machine_id must be a UUIDv4 string", ErrInvalid)
			}
		}
	}
	// Version nibble (character index 14) must be '4'.
	if s[14] != '4' {
		return fmt.Errorf("%w: machine_id must be UUIDv4 (version bits)", ErrInvalid)
	}
	// Variant: first hex of clock_seq must be 8, 9, a, or b.
	switch s[19] {
	case '8', '9', 'a', 'b', 'A', 'B':
		return nil
	default:
		return fmt.Errorf("%w: machine_id must be UUIDv4 (variant bits)", ErrInvalid)
	}
}

// ValidateTokenArg checks a CLI-supplied --token value: reject empty /
// whitespace-only. The value is otherwise accepted verbatim (no length or
// encoding mutation) so backend-issued tokens work as-is.
func ValidateTokenArg(token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%w: token must be non-empty", ErrInvalid)
	}
	return nil
}

// ValidateTokenPresence checks that a persisted token is non-empty.
func ValidateTokenPresence(token string) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%w: token is required", ErrInvalid)
	}
	return nil
}

func isHexChar(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
