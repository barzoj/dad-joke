package jobs

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/internal/store"
)

// MaxOutputBytes is the hard cap for captured stdout/stderr (64 KiB).
const MaxOutputBytes = store.JobIOMaxBytes

// ErrUnsupportedType is returned when a claimed job uses an unknown type.
var ErrUnsupportedType = fmt.Errorf("jobs: unsupported job type")

// ErrPayloadTooLarge is returned when payload exceeds 64 KiB.
var ErrPayloadTooLarge = fmt.Errorf("jobs: payload exceeds 64 KiB")

// ValidateClaimed rejects unknown types and oversized payloads before spawn.
func ValidateClaimed(jobType, payload string) error {
	switch jobType {
	case store.JobTypePowerShell, store.JobTypeCmd:
		// ok
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedType, jobType)
	}
	if len(payload) > store.JobIOMaxBytes {
		return fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(payload))
	}
	return nil
}

// ClampTimeout returns a hard timeout duration (default 60s, max 300s).
func ClampTimeout(timeoutSeconds *int32) time.Duration {
	sec := int32(store.JobTimeoutDefaultSeconds)
	if timeoutSeconds != nil && *timeoutSeconds > 0 {
		sec = *timeoutSeconds
	}
	if sec > store.JobTimeoutMaxSeconds {
		sec = store.JobTimeoutMaxSeconds
	}
	return time.Duration(sec) * time.Second
}

// TruncateUTF8 returns b capped at max bytes on a valid UTF-8 rune boundary.
// If max < 0, max is treated as 0. The returned slice may share b's backing array.
func TruncateUTF8(b []byte, max int) []byte {
	if max < 0 {
		max = 0
	}
	if len(b) <= max {
		return b
	}
	b = b[:max]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size != 1 {
			break
		}
		// Incomplete trailing multi-byte sequence — drop the last byte.
		b = b[:len(b)-1]
	}
	return b
}

// TruncateUTF8String is TruncateUTF8 for strings.
func TruncateUTF8String(s string, max int) string {
	return string(TruncateUTF8([]byte(s), max))
}
