// Package buildinfo holds compile-time metadata injected via -ldflags.
package buildinfo

// Defaults used when -ldflags do not override the values (local/dev builds).
const (
	DefaultVersion       = "0.0.1-dev"
	DefaultCommit        = "unknown"
	DefaultReleaseAPIURL = "https://api.github.com/repos/barzoj/dad-joke/releases/latest"
)

// These package-level vars are set at link time:
//
//	-X github.com/barzoj/dad-joke/internal/buildinfo.Version=...
//	-X github.com/barzoj/dad-joke/internal/buildinfo.Commit=...
//	-X github.com/barzoj/dad-joke/internal/buildinfo.ReleaseAPIURL=...
var (
	Version       = DefaultVersion
	Commit        = DefaultCommit
	ReleaseAPIURL = DefaultReleaseAPIURL
)

// Format returns a human-readable version line for the version subcommand.
func Format() string {
	return FormatVersion(Version, Commit)
}

// FormatVersion formats version and commit for display. Empty commit is treated as "unknown".
func FormatVersion(version, commit string) string {
	if version == "" {
		version = DefaultVersion
	}
	if commit == "" {
		commit = DefaultCommit
	}
	return "dad-joke " + version + " (commit " + commit + ")"
}
