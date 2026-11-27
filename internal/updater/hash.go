package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// ParseSHA256File parses a GNU coreutils-style ".sha256" sidecar:
//
//	"<hex>  <filename>" (two spaces preferred; any whitespace accepted).
//
// Returns the lowercase hex digest. The filename token is optional for
// validation purposes but when present must be non-empty.
func ParseSHA256File(contents []byte) (hashHex string, filename string, err error) {
	line := strings.TrimSpace(string(contents))
	if line == "" {
		return "", "", fmt.Errorf("empty sha256 file")
	}
	// Take first line only.
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	fields := strings.Fields(line)
	if len(fields) < 1 {
		return "", "", fmt.Errorf("malformed sha256 file")
	}
	h := strings.ToLower(fields[0])
	if len(h) != 64 {
		return "", "", fmt.Errorf("malformed sha256 digest: want 64 hex chars, got %d", len(h))
	}
	if _, err := hex.DecodeString(h); err != nil {
		return "", "", fmt.Errorf("malformed sha256 digest: %w", err)
	}
	if len(fields) >= 2 {
		filename = fields[1]
		// Allow optional leading '*' from `sha256sum` binary mode.
		filename = strings.TrimPrefix(filename, "*")
	}
	return h, filename, nil
}

// HashReader returns the lowercase hex SHA-256 of r.
func HashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashBytes returns the lowercase hex SHA-256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// VerifySHA256 compares fileHashHex against expectedHex (case-insensitive).
func VerifySHA256(fileHashHex, expectedHex string) error {
	if !strings.EqualFold(fileHashHex, expectedHex) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", strings.ToLower(fileHashHex), strings.ToLower(expectedHex))
	}
	return nil
}
