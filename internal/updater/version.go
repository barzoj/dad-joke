package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Version comparison rules (Issue #3):
//
//  1. Strip a leading "v" from both sides (v0.0.1 → 0.0.1).
//  2. A -dev build (e.g. 0.0.1-dev) updates to any numbered release whose
//     numeric core is >= the -dev build's numeric prefix.
//  3. A numbered release updates strictly when latest > current.
//  4. Equal versions → not newer. Invalid tags → error.

// Version is a parsed major.minor.patch with an optional prerelease suffix.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string // without leading '-', e.g. "dev"; empty for numbered releases
	Raw        string // normalized form without leading v
}

// NormalizeVersion strips a leading "v" or "V" and trims space.
func NormalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	return s
}

// ParseVersion parses a semver-like version string used by dad-joke tags.
func ParseVersion(s string) (Version, error) {
	raw := NormalizeVersion(s)
	if raw == "" {
		return Version{}, fmt.Errorf("empty version")
	}

	core := raw
	pre := ""
	if i := strings.IndexByte(raw, '-'); i >= 0 {
		core = raw[:i]
		pre = raw[i+1:]
	}

	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: want major.minor.patch", s)
	}
	major, err := parseNonNegInt(parts[0])
	if err != nil {
		return Version{}, fmt.Errorf("invalid version %q: %w", s, err)
	}
	minor, err := parseNonNegInt(parts[1])
	if err != nil {
		return Version{}, fmt.Errorf("invalid version %q: %w", s, err)
	}
	patch, err := parseNonNegInt(parts[2])
	if err != nil {
		return Version{}, fmt.Errorf("invalid version %q: %w", s, err)
	}
	return Version{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Prerelease: pre,
		Raw:        raw,
	}, nil
}

func parseNonNegInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("invalid numeric component %q", s)
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, fmt.Errorf("invalid numeric component %q", s)
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric component %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative component %q", s)
	}
	return n, nil
}

// IsDev reports whether v is a -dev (or other prerelease) build.
func (v Version) IsDev() bool {
	return v.Prerelease != ""
}

// CoreEQ reports equal major.minor.patch ignoring prerelease.
func (v Version) CoreEQ(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor && v.Patch == o.Patch
}

// CompareCore returns -1 / 0 / 1 comparing numeric cores only.
func (v Version) CompareCore(o Version) int {
	if v.Major != o.Major {
		if v.Major < o.Major {
			return -1
		}
		return 1
	}
	if v.Minor != o.Minor {
		if v.Minor < o.Minor {
			return -1
		}
		return 1
	}
	if v.Patch != o.Patch {
		if v.Patch < o.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// IsNewer reports whether latest should replace current per Issue #3 rules.
func IsNewer(latest, current string) (bool, error) {
	l, err := ParseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("latest: %w", err)
	}
	c, err := ParseVersion(current)
	if err != nil {
		return false, fmt.Errorf("current: %w", err)
	}

	cmp := l.CompareCore(c)
	if c.IsDev() {
		// -dev updates to any numbered release >= its numeric prefix.
		if l.IsDev() {
			// Prefer a numbered (or lexicographically later) release only when core is greater;
			// equal-core -dev → -dev is not an upgrade.
			return cmp > 0, nil
		}
		return cmp >= 0, nil
	}
	// Numbered builds: strict greater core. Prerelease latest never upgrades a numbered current.
	if l.IsDev() {
		return false, nil
	}
	return cmp > 0, nil
}
