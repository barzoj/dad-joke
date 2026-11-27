package updater

import (
	"fmt"
	"strings"

	"github.com/barzoj/dad-joke/internal/installer"
)

// UpdatesSubdir is the staging directory under ProgramData\DadJoke.
const UpdatesSubdir = "updates"

// ValidateApplyPaths enforces the LocalSystem apply-update path allowlist.
//
// Rules (Windows path semantics, even when unit-tested on Linux):
//   - staged must resolve under <dataDir>\updates\<subdir>\... (not the updates root itself)
//   - binary must equal the expected installed dad-joke.exe path
//   - path traversal (`..`) and empty paths are rejected
func ValidateApplyPaths(staged, binary, dataDir, expectedBinary string) error {
	if strings.TrimSpace(staged) == "" {
		return fmt.Errorf("staged path is required")
	}
	if strings.TrimSpace(binary) == "" {
		return fmt.Errorf("binary path is required")
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = installer.DefaultPaths().DataDir
	}
	if strings.TrimSpace(expectedBinary) == "" {
		expectedBinary = installer.DefaultPaths().BinaryPath
	}

	stagedClean, err := windowsClean(staged)
	if err != nil {
		return fmt.Errorf("staged path: %w", err)
	}
	binaryClean, err := windowsClean(binary)
	if err != nil {
		return fmt.Errorf("binary path: %w", err)
	}
	expectedClean, err := windowsClean(expectedBinary)
	if err != nil {
		return fmt.Errorf("expected binary path: %w", err)
	}
	dataClean, err := windowsClean(dataDir)
	if err != nil {
		return fmt.Errorf("data dir: %w", err)
	}

	updatesRoot, err := windowsClean(dataClean + `\` + UpdatesSubdir)
	if err != nil {
		return fmt.Errorf("updates root: %w", err)
	}

	if !windowsEqual(binaryClean, expectedClean) {
		return fmt.Errorf("binary path %q is not the allowed install path %q", binary, expectedBinary)
	}

	stagingDir, err := windowsClean(windowsDir(stagedClean))
	if err != nil {
		return fmt.Errorf("staging dir: %w", err)
	}
	if windowsEqual(stagingDir, updatesRoot) {
		return fmt.Errorf("staged path must not live directly in updates root %q", updatesRoot)
	}
	if !windowsUnder(stagingDir, updatesRoot) {
		return fmt.Errorf("staged path %q is outside updates tree %q", staged, updatesRoot)
	}
	if !windowsUnder(stagedClean, updatesRoot) {
		return fmt.Errorf("staged path %q is outside updates tree %q", staged, updatesRoot)
	}
	// Require a real file leaf (not a trailing separator / empty base).
	if windowsBase(stagedClean) == "" || windowsBase(stagedClean) == "." {
		return fmt.Errorf("staged path must be a file under a staging subdirectory")
	}
	return nil
}

// StagingDirFor returns the directory that Apply may RemoveAll after validation.
func StagingDirFor(staged string) (string, error) {
	clean, err := windowsClean(staged)
	if err != nil {
		return "", err
	}
	return windowsDir(clean), nil
}

// windowsClean normalizes a Windows-style path: slash → backslash, collapse
// `.` / `..`, reject escape above the root. Drive letters are upper-cased.
func windowsClean(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	p = strings.ReplaceAll(p, "/", `\`)

	var drive string
	if len(p) >= 2 && p[1] == ':' {
		drive = strings.ToUpper(p[:2])
		p = p[2:]
	}
	unc := false
	if strings.HasPrefix(p, `\\`) {
		// Reject UNC for apply-update allowlist (install paths are local).
		return "", fmt.Errorf("unc paths are not allowed")
	}
	if strings.HasPrefix(p, `\`) {
		unc = false
		p = strings.TrimPrefix(p, `\`)
	}

	parts := strings.Split(p, `\`)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) == 0 {
				return "", fmt.Errorf("path traversal")
			}
			out = out[:len(out)-1]
		default:
			out = append(out, part)
		}
	}
	_ = unc
	if drive != "" {
		return drive + `\` + strings.Join(out, `\`), nil
	}
	if len(out) == 0 {
		return `\`, nil
	}
	return `\` + strings.Join(out, `\`), nil
}

func windowsEqual(a, b string) bool {
	return strings.EqualFold(a, b)
}

func windowsUnder(child, parent string) bool {
	if windowsEqual(child, parent) {
		return false
	}
	prefix := parent
	if !strings.HasSuffix(prefix, `\`) {
		prefix += `\`
	}
	return strings.HasPrefix(strings.ToLower(child), strings.ToLower(prefix))
}

func windowsDir(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	i := strings.LastIndex(p, `\`)
	if i <= 0 {
		return `.`
	}
	if i == 2 && len(p) >= 2 && p[1] == ':' {
		// C:\file → C:\
		return p[:i+1]
	}
	return p[:i]
}

func windowsBase(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return p
	}
	return p[i+1:]
}

// AllowedStagedExample returns a canonical valid staged path for docs/tests.
func AllowedStagedExample(stagingID string) string {
	if stagingID == "" {
		stagingID = "1"
	}
	return installer.DefaultPaths().DataDir + `\` + UpdatesSubdir + `\` + stagingID + `\` + AssetEXE
}
