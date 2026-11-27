package heartbeat

import (
	"strings"

	"github.com/barzoj/dad-joke/internal/config"
)

// redactSecret replaces any occurrence of secret in s with the redaction marker.
// Empty secret is a no-op. Used so error strings from HTTP stacks never leak
// the raw agent token into logs.
func redactSecret(s, secret string) string {
	if secret == "" || s == "" {
		return s
	}
	if !strings.Contains(s, secret) {
		return s
	}
	return strings.ReplaceAll(s, secret, config.RedactedToken)
}

// redactErr formats err for logs with secret scrubbed.
func redactErr(err error, secret string) string {
	if err == nil {
		return ""
	}
	return redactSecret(err.Error(), secret)
}
