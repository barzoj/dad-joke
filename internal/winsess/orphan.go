package winsess

import (
	"path/filepath"
	"strings"
	"unicode"
)

// OrphanSnapshot is a process identity captured for session-agent re-adopt.
// All fields must be re-verified on a freshly opened handle before terminate.
type OrphanSnapshot struct {
	PID          uint32
	SessionID    uint32
	ImagePath    string // full path (QueryFullProcessImageName)
	CommandLine  string
	CreationTime int64  // GetProcessTimes creation FILETIME as int64
}

// SameInstalledExe reports whether observed is the same path as installed
// (case-insensitive, cleaned). Basename-only matches are rejected.
func SameInstalledExe(installed, observed string) bool {
	a := filepath.Clean(strings.TrimSpace(installed))
	b := filepath.Clean(strings.TrimSpace(observed))
	if a == "" || b == "" || a == "." || b == "." {
		return false
	}
	return strings.EqualFold(a, b)
}

// IsSessionAgentCommandLine reports whether cmdLine invokes the session
// subcommand as a standalone argv token (not "session-foo", not install/run).
func IsSessionAgentCommandLine(cmdLine string) bool {
	for _, tok := range splitCmdTokens(cmdLine) {
		if strings.EqualFold(tok, "session") {
			return true
		}
	}
	return false
}

// ShouldTerminateOrphan is the pure filter for orphan re-adopt: only our
// installed image path, with a session argv token, in a non-zero session,
// with a known creation time. An unrelated dad-joke.exe copy elsewhere must
// return false.
func ShouldTerminateOrphan(installedExe string, c OrphanSnapshot) bool {
	if c.PID == 0 || c.SessionID == 0 {
		return false
	}
	if c.CreationTime == 0 {
		return false
	}
	if !SameInstalledExe(installedExe, c.ImagePath) {
		return false
	}
	if !IsSessionAgentCommandLine(c.CommandLine) {
		return false
	}
	return true
}

// OrphanReopenMatches guards PID reuse: after OpenProcess, image path, command
// line, and creation time must equal the snapshot taken earlier.
func OrphanReopenMatches(snap OrphanSnapshot, imagePath, cmdLine string, creationTime int64) bool {
	if snap.CreationTime == 0 || creationTime == 0 || snap.CreationTime != creationTime {
		return false
	}
	if !SameInstalledExe(snap.ImagePath, imagePath) {
		return false
	}
	// Require session marker still present on the reopened handle.
	if !IsSessionAgentCommandLine(cmdLine) {
		return false
	}
	return true
}

func splitCmdTokens(cmdLine string) []string {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		return nil
	}
	var out []string
	var b strings.Builder
	inQuote := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range cmdLine {
		switch {
		case r == '"':
			inQuote = !inQuote
		case unicode.IsSpace(r) && !inQuote:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}
