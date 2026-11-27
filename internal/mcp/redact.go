package mcp

import (
	"regexp"
	"strings"

	"github.com/barzoj/dad-joke/internal/config"
)

// tokenPattern matches raw Dad Joke credentials that must never appear in logs
// or error responses (PAT, agent token, or user session).
var tokenPattern = regexp.MustCompile(`dj_(?:pat|agt|ses)_[A-Za-z0-9]+`)

// RedactSecrets replaces raw dj_pat_ / dj_agt_ / dj_ses_ credentials and any
// explicitly known secrets with the standard redaction marker.
func RedactSecrets(s string, known ...string) string {
	if s == "" {
		return s
	}
	out := s
	for _, secret := range known {
		if secret == "" {
			continue
		}
		if strings.Contains(out, secret) {
			out = strings.ReplaceAll(out, secret, config.RedactedToken)
		}
	}
	return tokenPattern.ReplaceAllString(out, config.RedactedToken)
}

// redactErr formats err for logs/responses with secrets scrubbed.
func redactErr(err error, known ...string) string {
	if err == nil {
		return ""
	}
	return RedactSecrets(err.Error(), known...)
}
