//go:build windows

package installer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/winscm"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// Bound for SCM transitional waits inside StartService (StopPending → Stopped).
const scmTransitionTimeout = 60 * time.Second

// SDDL for ProgramData and Program Files: disable inheritance;
// SYSTEM + Administrators Full Control only (ARCHITECTURE.md / AGENTS.md).
const dataACLSDDL = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// binaryACLSDDL matches dataACLSDDL — LocalSystem does not need Users RX.
const binaryACLSDDL = dataACLSDDL

// WindowsHost implements Host using Win32 APIs / SCM / netsh.
type WindowsHost struct {
	elog *eventlog.Log
}

// NewWindowsHost constructs the production Host. Event log is best-effort.
func NewWindowsHost() *WindowsHost {
	h := &WindowsHost{}
	_ = eventlog.InstallAsEventCreate(ServiceName, eventlog.Error|eventlog.Warning|eventlog.Info)
	if el, err := eventlog.Open(ServiceName); err == nil {
		h.elog = el
	}
	return h
}

func (h *WindowsHost) Log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(os.Stderr, msg)
	if h.elog != nil {
		_ = h.elog.Info(1, msg)
	}
}

func (h *WindowsHost) IsElevated() (bool, error) {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false, err
	}
	defer token.Close()
	return token.IsElevated(), nil
}

func (h *WindowsHost) ExecutablePath() (string, error) {
	return os.Executable()
}

func (h *WindowsHost) MkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

func (h *WindowsHost) CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	// Write to a temp file then rename for a more atomic replace.
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		// Destination may be locked (running image). On Windows a running image
		// can be renamed aside even when it cannot be overwritten/deleted.
		bak := dst + ".old"
		_ = os.Remove(bak)
		if renErr := os.Rename(dst, bak); renErr != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("replace locked destination: rename aside: %w (after %v)", renErr, err)
		}
		if err2 := os.Rename(tmp, dst); err2 != nil {
			_ = os.Rename(bak, dst) // best-effort restore
			_ = os.Remove(tmp)
			return fmt.Errorf("replace locked destination: place new: %w", err2)
		}
		_ = os.Remove(bak)
	}
	return nil
}

func (h *WindowsHost) WriteFileIfAbsent(path string, data []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := h.WriteFile(path, data); err != nil {
		// Race: another install created it — treat as already present.
		if _, statErr := os.Stat(path); statErr == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (h *WindowsHost) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (h *WindowsHost) WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (h *WindowsHost) FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (h *WindowsHost) RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	// Self-deletion: if we cannot remove because the binary is locked, schedule
	// a best-effort deferred delete via cmd after this process exits.
	exe, exeErr := os.Executable()
	if exeErr == nil {
		exeAbs, _ := filepath.Abs(exe)
		pathAbs, _ := filepath.Abs(path)
		if strings.HasPrefix(strings.ToLower(exeAbs), strings.ToLower(pathAbs)) {
			h.scheduleDeferredDelete(pathAbs)
			return nil
		}
	}
	return err
}

func (h *WindowsHost) scheduleDeferredDelete(path string) {
	// ping delay ~2s then rmdir; fire-and-forget.
	cmd := exec.Command("cmd", "/C", fmt.Sprintf(`ping 127.0.0.1 -n 3 >nul & rmdir /S /Q "%s"`, path))
	_ = cmd.Start()
}

func (h *WindowsHost) SameFile(a, b string) (bool, error) {
	aAbs, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	bAbs, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if strings.EqualFold(aAbs, bAbs) {
		return true, nil
	}
	ai, err := os.Stat(aAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	bi, err := os.Stat(bAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

func (h *WindowsHost) ApplyDataACL(path string) error {
	return setFileSDDL(path, dataACLSDDL)
}

func (h *WindowsHost) ApplyBinaryACL(path string) error {
	return setFileSDDL(path, binaryACLSDDL)
}

func setFileSDDL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("parse SDDL: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("get DACL: %w", err)
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
	if err != nil {
		return fmt.Errorf("SetNamedSecurityInfo(%s): %w", path, err)
	}
	return nil
}

func (h *WindowsHost) EnsureService(spec ServiceSpec) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	binPath := spec.BinaryPath
	if len(spec.Args) > 0 {
		// Quote binary path for SCM when it contains spaces.
		binPath = `"` + spec.BinaryPath + `"`
		for _, a := range spec.Args {
			binPath += " " + a
		}
	}

	cfg := mgr.Config{
		DisplayName:      spec.DisplayName,
		Description:      spec.Description,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "", // LocalSystem
		DelayedAutoStart: spec.DelayedAutoStart,
	}

	s, err := m.OpenService(spec.Name)
	if err == nil {
		defer s.Close()
		cur, err := s.Config()
		if err != nil {
			return err
		}
		cur.DisplayName = cfg.DisplayName
		cur.Description = cfg.Description
		cur.StartType = cfg.StartType
		cur.BinaryPathName = binPath
		cur.ServiceStartName = cfg.ServiceStartName
		cur.DelayedAutoStart = cfg.DelayedAutoStart
		if err := s.UpdateConfig(cur); err != nil {
			return fmt.Errorf("UpdateConfig: %w", err)
		}
	} else {
		s, err = m.CreateService(spec.Name, spec.BinaryPath, cfg, spec.Args...)
		if err != nil {
			return fmt.Errorf("CreateService: %w", err)
		}
		defer s.Close()
	}

	actions := make([]mgr.RecoveryAction, 0, len(spec.Recovery))
	for _, r := range spec.Recovery {
		var t int
		switch strings.ToLower(r.Type) {
		case "restart":
			t = mgr.ServiceRestart
		case "reboot":
			t = mgr.ComputerReboot
		case "run_command":
			t = mgr.RunCommand
		default:
			t = mgr.NoAction
		}
		actions = append(actions, mgr.RecoveryAction{Type: t, Delay: r.Delay})
	}
	if err := s.SetRecoveryActions(actions, spec.ResetPeriodSec); err != nil {
		return fmt.Errorf("SetRecoveryActions: %w", err)
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("SetRecoveryActionsOnNonCrashFailures: %w", err)
	}

	if spec.SecurityDescriptorSDDL != "" {
		if err := setServiceSDDL(spec.Name, spec.SecurityDescriptorSDDL); err != nil {
			return fmt.Errorf("service SDDL: %w", err)
		}
	}
	return nil
}

func setServiceSDDL(name, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		name,
		windows.SE_SERVICE,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func (h *WindowsHost) StartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	switch winscm.PlanStart(uint32(status.State)) {
	case winscm.StartNop:
		// Running or StartPending: WaitForState(Running) follows.
		return nil
	case winscm.StartAfterStop:
		// Still stopping — wait for SERVICE_STOPPED before StartServiceW.
		if err := waitHandleState(s, svc.Stopped, scmTransitionTimeout); err != nil {
			return err
		}
	}
	if err := s.Start(); err != nil {
		// TOCTOU: another starter won the race, or we queried during StartPending.
		if winscm.IsAlreadyRunning(err) {
			return nil
		}
		return err
	}
	return nil
}

func (h *WindowsHost) StopService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil
		}
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if winscm.StopNoop(uint32(status.State)) {
		// Stopped or StopPending: WaitForState(Stopped) follows.
		return nil
	}
	_, err = s.Control(svc.Stop)
	if err != nil && winscm.IsBenignStopError(err) {
		return nil
	}
	return err
}

func (h *WindowsHost) DeleteService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil
		}
		return err
	}
	defer s.Close()
	err = s.Delete()
	if err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return err
	}
	return nil
}

func (h *WindowsHost) ServiceExists(name string) (bool, error) {
	m, err := mgr.Connect()
	if err != nil {
		return false, err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return false, nil
		}
		return false, err
	}
	s.Close()
	return true, nil
}

func (h *WindowsHost) WaitForState(name string, state ServiceState, timeout time.Duration) error {
	want, err := toSvcState(state)
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	return waitHandleState(s, want, timeout)
}

func waitHandleState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for service state %d (last=%d)", want, status.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func toSvcState(state ServiceState) (svc.State, error) {
	switch state {
	case StateRunning:
		return svc.Running, nil
	case StateStopped:
		return svc.Stopped, nil
	default:
		return 0, fmt.Errorf("unknown service state %q", state)
	}
}

func (h *WindowsHost) EnsureFirewallRule(rule FirewallRuleSpec) error {
	// Idempotent: if a matching outbound allow rule already exists, leave it.
	// Otherwise replace (delete then add) so a stale program path is corrected.
	if firewallRuleMatches(rule) {
		return nil
	}
	_ = h.RemoveFirewallRule(rule.Name)
	cmd := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		"name="+rule.Name,
		"dir=out",
		"action=allow",
		"program="+rule.Program,
		"enable=yes",
		"profile=any",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh add rule: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func firewallRuleMatches(rule FirewallRuleSpec) bool {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+rule.Name, "verbose")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	low := strings.ToLower(string(out))
	if strings.Contains(low, "no rules match") {
		return false
	}
	return strings.Contains(low, strings.ToLower(rule.Program)) &&
		strings.Contains(low, "out") &&
		strings.Contains(low, "allow")
}

func (h *WindowsHost) RemoveFirewallRule(name string) error {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out))
		// Idempotent: no matching rules is success.
		if strings.Contains(msg, "no rules match") || strings.Contains(msg, "none of the specified") {
			return nil
		}
		// Some locales / versions: treat exit as soft if rule gone.
		if !firewallRuleExists(name) {
			return nil
		}
		return fmt.Errorf("netsh delete rule: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func firewallRuleExists(name string) bool {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return !strings.Contains(strings.ToLower(string(out)), "no rules match")
}
