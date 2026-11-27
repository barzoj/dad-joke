// Package tokenhash generates and hashes Dad Joke API credentials.
//
// Raw secrets (dj_pat_... / dj_agt_...) must never be logged or persisted.
// Only SHA-256 hex digests and short non-secret prefixes are safe to store.
package tokenhash

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/barzoj/dad-joke/internal/store"
)

const (
	prefixAgent   = "dj_agt_"
	prefixPAT     = "dj_pat_"
	prefixSession = "dj_ses_"
	entropyBytes  = 32 // 256-bit
	// prefixLen is the non-secret identifying prefix length stored in the DB
	// (scheme prefix + 8 hex chars of entropy).
	prefixLen = len(prefixPAT) + 8
)

// SessionKind is a pseudo-kind for user session secrets (not a TokenKind).
const SessionKind = "session"

// GenerateSession creates a raw dj_ses_... secret and its SHA-256 hex hash.
func GenerateSession() (raw, hash string, err error) {
	var buf [entropyBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("tokenhash: rand: %w", err)
	}
	raw = prefixSession + hex.EncodeToString(buf[:])
	return raw, Hash(raw), nil
}

// IsSession reports whether raw looks like a user session secret.
func IsSession(raw string) bool {
	return strings.HasPrefix(raw, prefixSession)
}

// Generate creates a new raw credential for kind, its stored prefix, and the
// SHA-256 hex hash of the raw secret. The raw value must be shown once and discarded.
func Generate(kind store.TokenKind) (raw, prefix, hash string, err error) {
	var scheme string
	switch kind {
	case store.TokenKindAgent:
		scheme = prefixAgent
	case store.TokenKindPAT:
		scheme = prefixPAT
	default:
		return "", "", "", fmt.Errorf("tokenhash: unknown kind %q", kind)
	}
	var buf [entropyBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", "", fmt.Errorf("tokenhash: rand: %w", err)
	}
	raw = scheme + hex.EncodeToString(buf[:])
	prefix = raw[:prefixLen]
	hash = Hash(raw)
	return raw, prefix, hash, nil
}

// Hash returns the lowercase hex SHA-256 digest of raw.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// EqualHash compares a stored hex digest to Hash(raw) in constant time.
func EqualHash(storedHex, raw string) bool {
	want := Hash(raw)
	if len(storedHex) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(storedHex), []byte(want)) == 1
}

// KindOf returns the token kind for a raw secret, or empty if the prefix is unknown.
func KindOf(raw string) store.TokenKind {
	switch {
	case strings.HasPrefix(raw, prefixAgent):
		return store.TokenKindAgent
	case strings.HasPrefix(raw, prefixPAT):
		return store.TokenKindPAT
	default:
		return ""
	}
}
