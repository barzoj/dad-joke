package installer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
)

func TestStubReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	var s Stub
	if _, err := s.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Install = %v, want ErrNotImplemented", err)
	}
	if err := s.Uninstall(context.Background()); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Uninstall = %v, want ErrNotImplemented", err)
	}
}

func TestDefaultPaths(t *testing.T) {
	t.Parallel()
	p := DefaultPaths()
	if !strings.Contains(p.BinaryPath, BinaryName) {
		t.Fatalf("BinaryPath = %q", p.BinaryPath)
	}
	if !strings.Contains(p.ConfigPath, ConfigFileName) {
		t.Fatalf("ConfigPath = %q", p.ConfigPath)
	}
}

func TestInstallRejectsNonAdmin(t *testing.T) {
	t.Parallel()
	h := newMockHost()
	h.elevated = false
	s := NewService(Options{Host: h, Paths: testPaths(t), RequireElevated: true})
	_, err := s.Install(context.Background(), InstallOptions{})
	if !errors.Is(err, ErrNotElevated) {
		t.Fatalf("Install = %v, want ErrNotElevated", err)
	}
}

func TestInstallHappyPathIdempotent(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source-dad-joke.exe")
	h.files[h.exePath] = []byte("binary-v1")

	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true, StartTimeout: time.Second})
	out, err := s.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if string(h.files[paths.BinaryPath]) != "binary-v1" {
		t.Fatalf("binary not installed: %v", h.files)
	}
	if _, ok := h.files[paths.ConfigPath]; !ok {
		t.Fatal("config not written")
	}
	if out.TokenStatus != TokenStatusGenerated || out.MachineID == "" || out.Token == "" {
		t.Fatalf("outcome = %+v, want generated identity", out)
	}
	if !h.aclsData[paths.DataDir] || !h.aclsData[paths.ConfigPath] {
		t.Fatal("expected data ACLs on dir and config")
	}
	if !h.serviceEnsured {
		t.Fatal("service not ensured")
	}
	if !h.firewallEnsured {
		t.Fatal("firewall not ensured")
	}
	if h.serviceState[ServiceName] != StateRunning {
		t.Fatalf("service state = %q, want Running", h.serviceState[ServiceName])
	}

	// Re-install must preserve existing config contents including identity.
	preserved := append([]byte(nil), h.files[paths.ConfigPath]...)
	out2, err := s.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatalf("re-Install: %v", err)
	}
	if got := string(h.files[paths.ConfigPath]); got != string(preserved) {
		t.Fatalf("config changed on re-install:\n%s\nvs\n%s", got, preserved)
	}
	if out2.MachineID != out.MachineID || out2.TokenStatus != TokenStatusPreserved || out2.Token != "" {
		t.Fatalf("reinstall outcome = %+v, want preserved %s", out2, out.MachineID)
	}
}

func TestInstallReinstallWhileRunning(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source-v2.exe")
	h.files[h.exePath] = []byte("binary-v2")
	h.files[paths.BinaryPath] = []byte("binary-v1")
	h.files[paths.ConfigPath] = []byte(testConfigJSON("http://x", 60, "550e8400-e29b-41d4-a716-446655440000", strings.Repeat("ab", 32)))
	h.dirs[paths.InstallDir] = true
	h.dirs[paths.DataDir] = true
	h.services[ServiceName] = true
	h.serviceState[ServiceName] = StateRunning

	s := NewService(Options{
		Host: h, Paths: paths, RequireElevated: true,
		StartTimeout: time.Second, StopTimeout: time.Second,
	})
	if _, err := s.Install(context.Background(), InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if h.stopCalls < 1 {
		t.Fatal("expected StopService before binary replace")
	}
	if string(h.files[paths.BinaryPath]) != "binary-v2" {
		t.Fatalf("binary not replaced: %q", h.files[paths.BinaryPath])
	}
	if h.serviceState[ServiceName] != StateRunning {
		t.Fatalf("service state = %q, want Running", h.serviceState[ServiceName])
	}
	if got := string(h.files[paths.ConfigPath]); !strings.Contains(got, "http://x") || !strings.Contains(got, "550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("config should be preserved: %s", got)
	}
}

func TestInstallRollbackOnServiceFailure(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	h.ensureServiceErr = errors.New("scm boom")

	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true})
	_, err := s.Install(context.Background(), InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "scm boom") {
		t.Fatalf("Install = %v, want scm boom", err)
	}
	// Fresh install: rollback removes the data dir created this run.
	if _, ok := h.files[paths.ConfigPath]; ok {
		t.Fatal("expected config removed during rollback")
	}
}

func TestInstallRollbackPreservesExistingConfig(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	existing := []byte(testConfigJSON("http://keep-me", 2, "550e8400-e29b-41d4-a716-446655440000", strings.Repeat("ab", 32)))
	h.files[paths.ConfigPath] = existing
	h.dirs[paths.DataDir] = true
	h.ensureServiceErr = errors.New("scm boom")

	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true})
	_, err := s.Install(context.Background(), InstallOptions{TokenSet: true, Token: "replacement-token-value"})
	if err == nil || !strings.Contains(err.Error(), "scm boom") {
		t.Fatalf("Install = %v, want scm boom", err)
	}
	if got := string(h.files[paths.ConfigPath]); got != string(existing) {
		t.Fatalf("pre-existing config wiped on rollback: %q", got)
	}
	if !h.dirs[paths.DataDir] {
		t.Fatal("pre-existing data dir wiped on rollback")
	}
}

func TestInstallRollbackOnFirewallFailure(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	h.ensureFirewallErr = errors.New("fw boom")

	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true})
	_, err := s.Install(context.Background(), InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "fw boom") {
		t.Fatalf("Install = %v, want fw boom", err)
	}
	if h.services[ServiceName] {
		t.Fatal("expected service deleted during rollback of fresh install")
	}
	if _, ok := h.files[paths.ConfigPath]; ok {
		t.Fatal("expected fresh config removed during rollback")
	}
}

func TestInstallRollbackRestartsServiceAndRestoresBinary(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source-v2.exe")
	h.files[h.exePath] = []byte("binary-v2")
	h.files[paths.BinaryPath] = []byte("binary-v1")
	h.files[paths.ConfigPath] = []byte(testConfigJSON("http://x", 60, "550e8400-e29b-41d4-a716-446655440000", strings.Repeat("ab", 32)))
	h.dirs[paths.InstallDir] = true
	h.dirs[paths.DataDir] = true
	h.services[ServiceName] = true
	h.serviceState[ServiceName] = StateRunning
	h.ensureServiceErr = errors.New("scm boom after stop")

	s := NewService(Options{
		Host: h, Paths: paths, RequireElevated: true,
		StartTimeout: time.Second, StopTimeout: time.Second,
	})
	_, err := s.Install(context.Background(), InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "scm boom") {
		t.Fatalf("Install = %v, want scm boom", err)
	}
	if h.stopCalls < 1 {
		t.Fatal("expected service stop before replace")
	}
	if string(h.files[paths.BinaryPath]) != "binary-v1" {
		t.Fatalf("binary not restored on rollback: %q", h.files[paths.BinaryPath])
	}
	if h.serviceState[ServiceName] != StateRunning {
		t.Fatalf("service state = %q, want Running after rollback restart", h.serviceState[ServiceName])
	}
}

func TestInstallRollbackRestoresFirewallOnReinstall(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source-v2.exe")
	h.files[h.exePath] = []byte("binary-v2")
	h.files[paths.BinaryPath] = []byte("binary-v1")
	h.files[paths.ConfigPath] = []byte(testConfigJSON("http://x", 60, "550e8400-e29b-41d4-a716-446655440000", strings.Repeat("ab", 32)))
	h.dirs[paths.InstallDir] = true
	h.dirs[paths.DataDir] = true
	h.services[ServiceName] = true
	h.serviceState[ServiceName] = StateRunning
	h.firewallRules[FirewallRuleName] = true
	h.firewallAlwaysReplace = true // force delete-then-add like a path correction
	h.ensureFirewallErr = errors.New("fw add boom")

	s := NewService(Options{
		Host: h, Paths: paths, RequireElevated: true,
		StartTimeout: time.Second, StopTimeout: time.Second,
	})
	_, err := s.Install(context.Background(), InstallOptions{})
	if err == nil || !strings.Contains(err.Error(), "fw add boom") {
		t.Fatalf("Install = %v, want fw add boom", err)
	}
	if !h.firewallRules[FirewallRuleName] {
		t.Fatal("expected firewall rule restored on reinstall rollback")
	}
	if string(h.files[paths.BinaryPath]) != "binary-v1" {
		t.Fatalf("binary not restored: %q", h.files[paths.BinaryPath])
	}
	if h.serviceState[ServiceName] != StateRunning {
		t.Fatalf("service state = %q, want Running after rollback", h.serviceState[ServiceName])
	}
}

func TestUninstallIdempotentWhenAbsent(t *testing.T) {
	t.Parallel()
	h := newMockHost()
	s := NewService(Options{Host: h, Paths: testPaths(t), RequireElevated: true})
	if err := s.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if err := s.Uninstall(context.Background()); err != nil {
		t.Fatalf("second Uninstall: %v", err)
	}
}

func TestUninstallStopsAndRemoves(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.serviceState[ServiceName] = StateRunning
	h.services[ServiceName] = true
	h.firewallRules[FirewallRuleName] = true
	h.files[paths.BinaryPath] = []byte("x")
	h.files[paths.ConfigPath] = []byte("{}")
	h.dirs[paths.InstallDir] = true
	h.dirs[paths.DataDir] = true

	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true, StopTimeout: time.Second})
	if err := s.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if h.services[ServiceName] {
		t.Fatal("service still registered")
	}
	if h.firewallRules[FirewallRuleName] {
		t.Fatal("firewall rule still present")
	}
	if h.dirs[paths.InstallDir] || h.dirs[paths.DataDir] {
		t.Fatal("dirs not removed")
	}
}

func TestUninstallRejectsNonAdmin(t *testing.T) {
	t.Parallel()
	h := newMockHost()
	h.elevated = false
	s := NewService(Options{Host: h, Paths: testPaths(t), RequireElevated: true})
	if err := s.Uninstall(context.Background()); !errors.Is(err, ErrNotElevated) {
		t.Fatalf("Uninstall = %v, want ErrNotElevated", err)
	}
}

func TestDefaultServiceSpec(t *testing.T) {
	t.Parallel()
	spec := DefaultServiceSpec(`C:\Program Files\DadJoke\dad-joke.exe`)
	if spec.Name != ServiceName {
		t.Fatalf("Name = %q", spec.Name)
	}
	if !spec.DelayedAutoStart {
		t.Fatal("DelayedAutoStart want true")
	}
	if spec.Account != "LocalSystem" {
		t.Fatalf("Account = %q", spec.Account)
	}
	if len(spec.Recovery) != 3 {
		t.Fatalf("Recovery len = %d", len(spec.Recovery))
	}
	for _, r := range spec.Recovery {
		if r.Type != "restart" {
			t.Fatalf("recovery type = %q", r.Type)
		}
	}
	if !strings.Contains(spec.SecurityDescriptorSDDL, "SY") || !strings.Contains(spec.SecurityDescriptorSDDL, "BA") {
		t.Fatalf("SDDL missing SY/BA: %s", spec.SecurityDescriptorSDDL)
	}
}

func TestACLAppliedBeforeConfigWrite(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	h.trackACLOrder = true

	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true, StartTimeout: time.Second})
	if _, err := s.Install(context.Background(), InstallOptions{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	dirIdx, cfgWriteIdx, cfgACLIdx := -1, -1, -1
	for i, step := range h.order {
		switch step {
		case "acl-data:" + paths.DataDir:
			dirIdx = i
		case "write:" + paths.ConfigPath:
			cfgWriteIdx = i
		case "acl-data:" + paths.ConfigPath:
			cfgACLIdx = i
		}
	}
	if dirIdx < 0 || cfgWriteIdx < 0 || cfgACLIdx < 0 {
		t.Fatalf("missing steps in order: %v", h.order)
	}
	if dirIdx >= cfgWriteIdx || cfgWriteIdx >= cfgACLIdx {
		t.Fatalf("expected ACL(dir) < write(config) < ACL(config); order=%v", h.order)
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	install := filepath.Join(root, "ProgramFiles", "DadJoke")
	data := filepath.Join(root, "ProgramData", "DadJoke")
	return Paths{
		InstallDir: install,
		BinaryPath: filepath.Join(install, BinaryName),
		DataDir:    data,
		ConfigPath: filepath.Join(data, ConfigFileName),
	}
}

func testConfigJSON(url string, poll int, machineID, token string) string {
	return fmt.Sprintf(`{"schema_version":1,"release_api_url":%q,"poll_interval_seconds":%d,"machine_id":%q,"token":%q}`,
		url, poll, machineID, token)
}

func TestReconcileConfigFreshGeneratesIdentity(t *testing.T) {
	t.Parallel()
	cfg, out, err := reconcileConfig(nil, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.TokenStatus != TokenStatusGenerated || out.Token == "" || out.MachineID == "" {
		t.Fatalf("outcome = %+v", out)
	}
	if cfg.MachineID != out.MachineID || cfg.Token != out.Token {
		t.Fatalf("cfg mismatch: %+v vs %+v", cfg, out)
	}
}

func TestReconcileConfigSuppliedTokenVerbatim(t *testing.T) {
	t.Parallel()
	secret := "backend-issued/token+with=special"
	cfg, out, err := reconcileConfig(nil, InstallOptions{TokenSet: true, Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != secret || out.TokenStatus != TokenStatusSupplied || out.Token != "" {
		t.Fatalf("cfg=%+v out=%+v", cfg, out)
	}
}

func TestReconcileConfigRejectsEmptyToken(t *testing.T) {
	t.Parallel()
	_, _, err := reconcileConfig(nil, InstallOptions{TokenSet: true, Token: "  "})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReconcileConfigPreservesOnReinstall(t *testing.T) {
	t.Parallel()
	existing := &config.Config{
		SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 3,
		MachineID: "550e8400-e29b-41d4-a716-446655440000", Token: strings.Repeat("ab", 32),
		BackendURL: "https://api.example.com", HeartbeatIntervalSeconds: 45, SampleIntervalSeconds: 60}
	cfg, out, err := reconcileConfig(existing, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineID != existing.MachineID || cfg.Token != existing.Token || out.TokenStatus != TokenStatusPreserved {
		t.Fatalf("cfg=%+v out=%+v", cfg, out)
	}
	if cfg.BackendURL != existing.BackendURL || cfg.HeartbeatIntervalSeconds != existing.HeartbeatIntervalSeconds {
		t.Fatalf("backend settings not preserved: %+v", cfg)
	}
}

func TestReconcileConfigSetsBackendURLFlag(t *testing.T) {
	t.Parallel()
	existing := &config.Config{
		SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 3,
		MachineID: "550e8400-e29b-41d4-a716-446655440000", Token: strings.Repeat("ab", 32),
		BackendURL: "https://old.example.com", HeartbeatIntervalSeconds: 45, SampleIntervalSeconds: 60}
	cfg, _, err := reconcileConfig(existing, InstallOptions{
		BackendURLSet: true, BackendURL: "http://127.0.0.1:8080",
		HeartbeatIntervalSet: true, HeartbeatIntervalSeconds: 15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackendURL != "http://127.0.0.1:8080" || cfg.HeartbeatIntervalSeconds != 15 {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestReconcileConfigRejectsExternalHTTPBackend(t *testing.T) {
	t.Parallel()
	_, _, err := reconcileConfig(nil, InstallOptions{
		TokenSet: true, Token: "tok",
		BackendURLSet: true, BackendURL: "http://example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReconcileConfigUpdatesTokenKeepsMachineID(t *testing.T) {
	t.Parallel()
	existing := &config.Config{
		SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 3,
		MachineID: "550e8400-e29b-41d4-a716-446655440000", Token: strings.Repeat("ab", 32), SampleIntervalSeconds: 60}
	cfg, out, err := reconcileConfig(existing, InstallOptions{TokenSet: true, Token: "new-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MachineID != existing.MachineID || cfg.Token != "new-secret" || out.TokenStatus != TokenStatusSupplied {
		t.Fatalf("cfg=%+v out=%+v", cfg, out)
	}
}

func TestInstallFreshAndReinstallIdentity(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true, StartTimeout: time.Second})

	out1, err := s.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw1 := string(h.files[paths.ConfigPath])
	if !strings.Contains(raw1, out1.MachineID) || !strings.Contains(raw1, out1.Token) {
		t.Fatalf("config missing identity: %s", raw1)
	}

	out2, err := s.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out2.MachineID != out1.MachineID || out2.TokenStatus != TokenStatusPreserved {
		t.Fatalf("out2=%+v out1=%+v", out2, out1)
	}
	if string(h.files[paths.ConfigPath]) != raw1 {
		t.Fatal("config rotated on reinstall without --token")
	}

	out3, err := s.Install(context.Background(), InstallOptions{TokenSet: true, Token: "explicit-new-token"})
	if err != nil {
		t.Fatal(err)
	}
	if out3.MachineID != out1.MachineID || out3.TokenStatus != TokenStatusSupplied {
		t.Fatalf("out3=%+v", out3)
	}
	raw3 := string(h.files[paths.ConfigPath])
	if !strings.Contains(raw3, "explicit-new-token") || strings.Contains(raw3, out1.Token) {
		t.Fatalf("token not updated: %s", raw3)
	}
}

func TestInstallRejectsEmptyTokenFlag(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true})
	_, err := s.Install(context.Background(), InstallOptions{TokenSet: true, Token: ""})
	if err == nil {
		t.Fatal("expected error for empty --token")
	}
}

func TestInstallBackfillsMissingIdentity(t *testing.T) {
	t.Parallel()
	paths := testPaths(t)
	h := newMockHost()
	h.exePath = filepath.Join(t.TempDir(), "source.exe")
	h.files[h.exePath] = []byte("bin")
	h.dirs[paths.DataDir] = true
	h.files[paths.ConfigPath] = []byte(`{"schema_version":1,"release_api_url":"http://custom","poll_interval_seconds":2}`)
	s := NewService(Options{Host: h, Paths: paths, RequireElevated: true, StartTimeout: time.Second})
	out, err := s.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if out.TokenStatus != TokenStatusGenerated || out.MachineID == "" {
		t.Fatalf("out=%+v", out)
	}
	got := string(h.files[paths.ConfigPath])
	if !strings.Contains(got, "http://custom") || !strings.Contains(got, out.MachineID) {
		t.Fatalf("backfill lost URL or id: %s", got)
	}
}

type mockHost struct {
	mu sync.Mutex

	elevated bool
	exePath  string

	files         map[string][]byte
	filesWritten  map[string]bool
	dirs          map[string]bool
	aclsData      map[string]bool
	aclsBinary    map[string]bool
	services      map[string]bool
	serviceState  map[string]ServiceState
	firewallRules map[string]bool

	serviceEnsured        bool
	firewallEnsured       bool
	ensureServiceErr      error
	ensureFirewallErr     error
	firewallAlwaysReplace bool
	stopCalls             int
	trackACLOrder         bool
	order                 []string
	logs                  []string
}

func newMockHost() *mockHost {
	return &mockHost{
		elevated:      true,
		files:         map[string][]byte{},
		filesWritten:  map[string]bool{},
		dirs:          map[string]bool{},
		aclsData:      map[string]bool{},
		aclsBinary:    map[string]bool{},
		services:      map[string]bool{},
		serviceState:  map[string]ServiceState{},
		firewallRules: map[string]bool{},
	}
}

func (m *mockHost) note(step string) {
	if m.trackACLOrder {
		m.order = append(m.order, step)
	}
}

func (m *mockHost) IsElevated() (bool, error) { return m.elevated, nil }
func (m *mockHost) ExecutablePath() (string, error) {
	if m.exePath == "" {
		return "", errors.New("no exe")
	}
	return m.exePath, nil
}
func (m *mockHost) MkdirAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[path] = true
	return nil
}
func (m *mockHost) CopyFile(src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[src]
	if !ok {
		return fmt.Errorf("src missing: %s", src)
	}
	cp := append([]byte(nil), b...)
	m.files[dst] = cp
	m.filesWritten[dst] = true
	m.dirs[filepath.Dir(dst)] = true
	return nil
}
func (m *mockHost) WriteFileIfAbsent(path string, data []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[path]; ok {
		return false, nil
	}
	m.files[path] = append([]byte(nil), data...)
	m.filesWritten[path] = true
	m.note("write:" + path)
	return true, nil
}
func (m *mockHost) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("read missing: %s", path)
	}
	return append([]byte(nil), b...), nil
}
func (m *mockHost) WriteFile(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = append([]byte(nil), data...)
	m.filesWritten[path] = true
	m.note("write:" + path)
	return nil
}
func (m *mockHost) FileExists(path string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs[path] {
		return true, nil
	}
	_, ok := m.files[path]
	return ok, nil
}
func (m *mockHost) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dirs, path)
	for k := range m.files {
		if k == path || strings.HasPrefix(k, path+string(filepath.Separator)) || strings.HasPrefix(k, path+"/") || strings.HasPrefix(k, path+`\`) {
			delete(m.files, k)
		}
	}
	return nil
}
func (m *mockHost) SameFile(a, b string) (bool, error) {
	return filepath.Clean(a) == filepath.Clean(b), nil
}
func (m *mockHost) ApplyDataACL(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aclsData[path] = true
	m.note("acl-data:" + path)
	return nil
}
func (m *mockHost) ApplyBinaryACL(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aclsBinary[path] = true
	return nil
}
func (m *mockHost) EnsureService(spec ServiceSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensureServiceErr != nil {
		return m.ensureServiceErr
	}
	m.serviceEnsured = true
	m.services[spec.Name] = true
	if _, ok := m.serviceState[spec.Name]; !ok {
		m.serviceState[spec.Name] = StateStopped
	}
	return nil
}
func (m *mockHost) StartService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serviceState[name] = StateRunning
	return nil
}
func (m *mockHost) StopService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls++
	m.serviceState[name] = StateStopped
	return nil
}
func (m *mockHost) DeleteService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.services, name)
	delete(m.serviceState, name)
	return nil
}
func (m *mockHost) ServiceExists(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.services[name], nil
}
func (m *mockHost) WaitForState(name string, state ServiceState, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.serviceState[name] != state {
		return fmt.Errorf("state=%s want=%s", m.serviceState[name], state)
	}
	return nil
}
func (m *mockHost) EnsureFirewallRule(rule FirewallRuleSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mirror Host contract: leave an existing correct rule alone unless tests
	// force a replace path (delete-then-add) to exercise rollback restore.
	if m.firewallRules[rule.Name] && m.ensureFirewallErr == nil && !m.firewallAlwaysReplace {
		m.firewallEnsured = true
		return nil
	}
	delete(m.firewallRules, rule.Name)
	if m.ensureFirewallErr != nil {
		err := m.ensureFirewallErr
		m.ensureFirewallErr = nil // one-shot so rollback EnsureFirewallRule can restore
		return err
	}
	m.firewallEnsured = true
	m.firewallRules[rule.Name] = true
	return nil
}
func (m *mockHost) RemoveFirewallRule(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.firewallRules, name)
	return nil
}
func (m *mockHost) Log(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, fmt.Sprintf(format, args...))
}
