// Package password hashes and verifies passwords with argon2id.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// OWASP-recommended argon2id parameters (as of common 2024 guidance).
	timeParam   = 3
	memoryKiB   = 64 * 1024 // 64 MiB
	parallelism = 4
	saltLen     = 16
	keyLen      = 32
)

// ErrInvalidHash is returned when a stored hash cannot be parsed.
var ErrInvalidHash = errors.New("password: invalid hash encoding")

// dummyHash is a valid argon2id hash used only to equalize login timing when
// the email does not exist. It is never a real user's password.
var dummyHash = mustHash("timing-equalization-not-a-real-password")

func mustHash(pw string) string {
	h, err := Hash(pw)
	if err != nil {
		panic(err)
	}
	return h
}

// Hash returns a PHC-encoded argon2id hash of password with a fresh random salt.
func Hash(password string) (string, error) {
	var salt [saltLen]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}
	sum := argon2.IDKey([]byte(password), salt[:], timeParam, memoryKiB, parallelism, keyLen)
	return encode(salt[:], sum), nil
}

// Verify reports whether password matches encodedHash using constant-time compare.
func Verify(encodedHash, password string) (bool, error) {
	salt, want, t, mem, par, err := decode(encodedHash)
	if err != nil {
		return false, err
	}
	keyLen := len(want)
	if keyLen < 16 || keyLen > 64 {
		return false, ErrInvalidHash
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, par, uint32(keyLen)) // #nosec G115 -- keyLen bounded 16..64
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyOrDummy runs a real argon2id verify. When encodedHash is empty it still
// burns dummyHash work so missing-email and wrong-password paths have similar timing.
func VerifyOrDummy(encodedHash, password string) (ok bool) {
	if encodedHash == "" {
		_, _ = Verify(dummyHash, password)
		return false
	}
	ok, err := Verify(encodedHash, password)
	if err != nil {
		_, _ = Verify(dummyHash, password)
		return false
	}
	return ok
}

func encode(salt, sum []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memoryKiB,
		timeParam,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	)
}

func decode(encoded string) (salt, sum []byte, time uint32, memory uint32, threads uint8, err error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	var m, t, p int
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	// Bound parameters before narrowing conversions (gosec G115).
	if t < 1 || t > 100 || m < 8*1024 || m > 4*1024*1024 || p < 1 || p > 16 {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	sum, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(sum) == 0 {
		return nil, nil, 0, 0, 0, ErrInvalidHash
	}
	return salt, sum, uint32(t), uint32(m), uint8(p), nil // #nosec G115 -- t/m/p bounded above
}
