//go:build e2e && windows

package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type pollHit struct {
	At           time.Time
	UserAgent    string
	AgentVersion string
	Path         string
}

type mockReleaseServer struct {
	mu       sync.Mutex
	tag      string
	exe      []byte
	shaBody  []byte // if nil, derived from exe; if set, used as-is (for mismatch tests)
	polls    []pollHit
	listener net.Listener
	srv      *http.Server
	url      string
}

func startMockReleaseServer(t *testing.T) *mockReleaseServer {
	t.Helper()
	m := &mockReleaseServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", m.handleLatest)
	mux.HandleFunc("/download/", m.handleDownload)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	m.listener = ln
	m.url = "http://" + ln.Addr().String()
	m.srv = &http.Server{Handler: mux}
	go func() { _ = m.srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = m.srv.Close()
	})
	return m
}

func (m *mockReleaseServer) LatestURL() string {
	return m.url + "/releases/latest"
}

func (m *mockReleaseServer) SetRelease(tag string, exe []byte, shaOverride []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tag = tag
	m.exe = append([]byte(nil), exe...)
	m.shaBody = shaOverride
}

func (m *mockReleaseServer) Polls() []pollHit {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]pollHit, len(m.polls))
	copy(out, m.polls)
	return out
}

func (m *mockReleaseServer) ResetPolls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls = nil
}

func (m *mockReleaseServer) handleLatest(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.polls = append(m.polls, pollHit{
		At:           time.Now(),
		UserAgent:    r.Header.Get("User-Agent"),
		AgentVersion: r.Header.Get("X-Agent-Version"),
		Path:         r.URL.Path,
	})
	tag := m.tag
	base := m.url
	m.mu.Unlock()

	payload := map[string]any{
		"tag_name": tag,
		"assets": []map[string]string{
			{
				"name":                 "dad-joke-windows-amd64.exe",
				"browser_download_url": base + "/download/dad-joke-windows-amd64.exe",
			},
			{
				"name":                 "dad-joke-windows-amd64.exe.sha256",
				"browser_download_url": base + "/download/dad-joke-windows-amd64.exe.sha256",
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (m *mockReleaseServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/download/")
	m.mu.Lock()
	defer m.mu.Unlock()
	switch name {
	case "dad-joke-windows-amd64.exe":
		_, _ = w.Write(m.exe)
	case "dad-joke-windows-amd64.exe.sha256":
		if m.shaBody != nil {
			_, _ = w.Write(m.shaBody)
			return
		}
		sum := sha256.Sum256(m.exe)
		line := hex.EncodeToString(sum[:]) + "  dad-joke-windows-amd64.exe\n"
		_, _ = w.Write([]byte(line))
	default:
		http.NotFound(w, r)
	}
}

func buildVersionedExe(t *testing.T, version, outPath string) {
	t.Helper()
	modRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// e2e package lives in ./e2e; module root is parent.
	if _, err := os.Stat(filepath.Join(modRoot, "go.mod")); err != nil {
		modRoot, err = filepath.Abs(".")
		if err != nil {
			t.Fatal(err)
		}
	}
	pkg := "github.com/barzoj/dad-joke/internal/buildinfo"
	ldflags := fmt.Sprintf("-X %s.Version=%s -X %s.Commit=e2e -X %s.ReleaseAPIURL=http://127.0.0.1/unused",
		pkg, version, pkg, pkg)
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", outPath, "./cmd/dad-joke")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", version, err, out)
	}
}

func writeUpdateConfig(t *testing.T, releaseURL string, pollSeconds int) {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	machineID := "550e8400-e29b-41d4-a716-446655440000"
	token := strings.Repeat("ab", 32)
	// Preserve identity when overriding poll settings after install.
	if raw, err := os.ReadFile(configPath); err == nil {
		var existing map[string]any
		if json.Unmarshal(raw, &existing) == nil {
			if v, ok := existing["machine_id"].(string); ok && v != "" {
				machineID = v
			}
			if v, ok := existing["token"].(string); ok && v != "" {
				token = v
			}
		}
	}
	body := fmt.Sprintf(`{
  "schema_version": 1,
  "release_api_url": %q,
  "poll_interval_seconds": %d,
  "machine_id": %q,
  "token": %q
}
`, releaseURL, pollSeconds, machineID, token)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitServiceState(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		state := mustPowershell(t, fmt.Sprintf(`(Get-Service -Name '%s' -ErrorAction SilentlyContinue).Status`, serviceName))
		if state == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("service status=%q, want %s", state, want)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func waitForPollVersion(t *testing.T, mock *mockReleaseServer, want string, minNew int, timeout time.Duration) []pollHit {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		polls := mock.Polls()
		matched := 0
		var last []pollHit
		for _, p := range polls {
			if p.AgentVersion == want && strings.HasPrefix(p.UserAgent, "DadJoke/"+want) {
				matched++
				last = append(last, p)
			}
		}
		if matched >= minNew {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d polls with version %s; got %d total polls", minNew, want, len(polls))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestTC03_PollAndVersionHeaders covers Issue #3 TC-03.
func TestTC03_PollAndVersionHeaders(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockReleaseServer(t)
	// Serve an older tag so the 0.0.1-dev agent only polls (no update).
	exeBytes, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	mock.SetRelease("v0.0.0", exeBytes, nil)

	writeUpdateConfig(t, mock.LatestURL(), 2)
	out, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)

	polls := waitForPollVersion(t, mock, "0.0.1-dev", 3, 20*time.Second)
	// Spacing should be roughly the configured interval (2s) within tolerance.
	if len(polls) >= 2 {
		deltas := make([]time.Duration, 0, len(polls)-1)
		for i := 1; i < len(polls); i++ {
			deltas = append(deltas, polls[i].At.Sub(polls[i-1].At))
		}
		okSpacing := 0
		for _, d := range deltas {
			if d >= 1*time.Second && d <= 6*time.Second {
				okSpacing++
			}
		}
		if okSpacing == 0 {
			t.Fatalf("poll spacing not consistent with ~2s interval: %v", deltas)
		}
	}
}

// TestTC04_HappyPathUpdate covers Issue #3 TC-04.
func TestTC04_HappyPathUpdate(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() {
		cleanupInstall(t, bin)
		_, _ = powershell(t, fmt.Sprintf(`Remove-Item -LiteralPath '%s.old' -Force -ErrorAction SilentlyContinue`, installExe))
	})

	dir := t.TempDir()
	v2 := filepath.Join(dir, "v0.0.2.exe")
	buildVersionedExe(t, "0.0.2", v2)
	v2bytes, err := os.ReadFile(v2)
	if err != nil {
		t.Fatal(err)
	}

	mock := startMockReleaseServer(t)
	mock.SetRelease("v0.0.2", v2bytes, nil)

	writeUpdateConfig(t, mock.LatestURL(), 2)
	out, err := runAdmin(t, bin, "install")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)

	// Wait until the updated binary polls as 0.0.2.
	_ = waitForPollVersion(t, mock, "0.0.2", 1, 90*time.Second)
	waitServiceState(t, "Running", 30*time.Second)

	installedHash := fileSHA256(t, installExe)
	wantHash := sha256.Sum256(v2bytes)
	if !strings.EqualFold(installedHash, hex.EncodeToString(wantHash[:])) {
		t.Fatalf("installed hash = %s, want %s", installedHash, hex.EncodeToString(wantHash[:]))
	}
}

// TestTC05_RejectCorruptHash covers Issue #3 TC-05.
func TestTC05_RejectCorruptHash(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() {
		cleanupInstall(t, bin)
		_, _ = powershell(t, fmt.Sprintf(`Remove-Item -LiteralPath '%s.old' -Force -ErrorAction SilentlyContinue`, installExe))
	})

	dir := t.TempDir()
	v2 := filepath.Join(dir, "v0.0.2.exe")
	v3 := filepath.Join(dir, "v0.0.3.exe")
	buildVersionedExe(t, "0.0.2", v2)
	buildVersionedExe(t, "0.0.3", v3)
	v2bytes, _ := os.ReadFile(v2)
	v3bytes, _ := os.ReadFile(v3)

	mock := startMockReleaseServer(t)
	mock.SetRelease("v0.0.2", v2bytes, nil)

	writeUpdateConfig(t, mock.LatestURL(), 2)
	// Install the 0.0.2 binary directly so we start already on 0.0.2.
	out, err := runAdmin(t, v2, "install")
	if err != nil {
		t.Fatalf("install 0.0.2: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)
	_ = waitForPollVersion(t, mock, "0.0.2", 1, 30*time.Second)

	beforeHash := fileSHA256(t, installExe)
	mock.ResetPolls()

	// Offer 0.0.3 with a mismatched sha256.
	badSHA := []byte(strings.Repeat("ab", 32) + "  dad-joke-windows-amd64.exe\n")
	mock.SetRelease("v0.0.3", v3bytes, badSHA)

	// Give the poller time to attempt (and reject) the update.
	deadline := time.Now().Add(20 * time.Second)
	sawPoll := false
	for time.Now().Before(deadline) {
		for _, p := range mock.Polls() {
			if p.AgentVersion == "0.0.2" {
				sawPoll = true
			}
			if p.AgentVersion == "0.0.3" {
				t.Fatalf("service unexpectedly reported 0.0.3 after corrupt hash")
			}
		}
		if sawPoll {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !sawPoll {
		t.Fatal("expected continued polls as 0.0.2")
	}

	// Extra settle time for any in-flight download/reject.
	time.Sleep(5 * time.Second)
	waitServiceState(t, "Running", 30*time.Second)

	afterHash := fileSHA256(t, installExe)
	if afterHash != beforeHash {
		t.Fatalf("installed binary changed after rejected update: before=%s after=%s", beforeHash, afterHash)
	}
	wantV2 := sha256.Sum256(v2bytes)
	if !strings.EqualFold(afterHash, hex.EncodeToString(wantV2[:])) {
		t.Fatalf("installed hash = %s, want 0.0.2 hash %s", afterHash, hex.EncodeToString(wantV2[:]))
	}

	// Still polling as 0.0.2.
	_ = waitForPollVersion(t, mock, "0.0.2", 2, 20*time.Second)
}
