//go:build e2e

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseArtifactsShape (AC-03 dry-run) validates that CI-produced release
// packaging artifacts — sha256 sidecar and manifest.json — match the binary.
// The release workflow publishes the same shapes on v* tags.
func TestReleaseArtifactsShape(t *testing.T) {
	dir := os.Getenv("DAD_JOKE_DIST")
	if dir == "" {
		t.Skip("DAD_JOKE_DIST not set; skipping packaging dry-run")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs dist: %v", err)
	}

	exeName := "dad-joke-windows-amd64.exe"
	exePath := filepath.Join(dir, exeName)
	shaPath := filepath.Join(dir, exeName+".sha256")
	manifestPath := filepath.Join(dir, "manifest.json")

	exeBytes, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("read exe: %v", err)
	}
	sum := sha256.Sum256(exeBytes)
	wantHash := hex.EncodeToString(sum[:])

	shaLine, err := os.ReadFile(shaPath)
	if err != nil {
		t.Fatalf("read sha256: %v", err)
	}
	// Format: "<hex>  <filename>" (two spaces, GNU coreutils style).
	parts := strings.Fields(string(shaLine))
	if len(parts) < 2 {
		t.Fatalf("sha256 file format = %q, want \"<hash>  <filename>\"", string(shaLine))
	}
	if !strings.EqualFold(parts[0], wantHash) {
		t.Fatalf("sha256 = %q, want %q", parts[0], wantHash)
	}
	if parts[1] != exeName {
		t.Fatalf("sha256 filename = %q, want %q", parts[1], exeName)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m struct {
		Version  string `json:"version"`
		Filename string `json:"filename"`
		Asset    string `json:"asset"`
		SHA256   string `json:"sha256"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	expectVersion := os.Getenv("DAD_JOKE_EXPECT_VERSION")
	if expectVersion == "" {
		expectVersion = "0.0.1-dev"
	}
	if m.Version != expectVersion {
		t.Fatalf("manifest.version = %q, want %q", m.Version, expectVersion)
	}
	if m.Filename != exeName {
		t.Fatalf("manifest.filename = %q, want %q", m.Filename, exeName)
	}
	if m.Asset != exeName {
		t.Fatalf("manifest.asset = %q, want %q", m.Asset, exeName)
	}
	if !strings.EqualFold(m.SHA256, wantHash) {
		t.Fatalf("manifest.sha256 = %q, want %q", m.SHA256, wantHash)
	}
	if m.URL == "" {
		t.Fatal("manifest.url is empty")
	}
}
