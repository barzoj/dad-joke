//go:build e2e && windows

package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/ipc"
	"golang.org/x/sys/windows"
)

const (
	ipcStatusPath  = `C:\ProgramData\DadJoke\ipc-status.json`
	ipcControlPath = `C:\ProgramData\DadJoke\ipc-control.json`
)

type ipcStatus struct {
	Connected map[string]bool `json:"connected"`
	Stats     ipc.Snapshot    `json:"stats"`
}

func ipcCmdDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "dadjoke-ipc-cmd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeForceProtocol(t *testing.T, n int) {
	t.Helper()
	path := filepath.Join(ipcCmdDir(t), "force_protocol")
	if n <= 0 {
		_ = os.Remove(path)
		return
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", n)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestT22_VersionedIPC covers Issue #45 acceptance on a live Windows session.
func TestT22_VersionedIPC(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(os.TempDir(), "dadjoke-ipc-cmd"))
	})

	mock := startMockFactsAPI(t)
	token := "e2e-ipc-token-" + randomSuffix(8)

	pinNoSelfUpdate(t)
	out, err := runAdmin(t, bin,
		"install",
		"--token", token,
		"--backend-url", mock.URL(),
		"--heartbeat-interval", "2",
		"--sample-interval", "15",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)
	requireInteractiveSession(t)

	pid := waitSessionAgent(t, 45*time.Second)
	status := waitIPCConnected(t, 45*time.Second)
	if status.Stats.HandshakeOK < 1 {
		t.Fatalf("handshake_ok=%d", status.Stats.HandshakeOK)
	}

	sessionID := firstConnectedSession(t, status)
	pipeName := ipc.PipeName(sessionID)
	assertPipeACL(t, pipeName, pid)

	// Spoof: same user, different PID — open pipe and send hello; must be refused.
	refusedBefore := readIPCStatus(t).Stats.RefusedClient
	spoofHello(t, pipeName, sessionID, uint32(pid+100000))
	waitIPCStat(t, func(s ipcStatus) bool {
		return s.Stats.RefusedClient > refusedBefore
	}, 15*time.Second, "refused_client")

	// Drop/reconnect: PID unchanged.
	pidBefore := pid
	writeIPCControl(t, map[string]any{"action": "drop"})
	waitIPCStat(t, func(s ipcStatus) bool {
		return !anyConnected(s)
	}, 15*time.Second, "disconnected after drop")
	waitIPCConnected(t, 30*time.Second)
	pidAfter := waitSessionAgent(t, 15*time.Second)
	if pidAfter != pidBefore {
		t.Fatalf("drop restarted process: %d → %d", pidBefore, pidAfter)
	}

	// Oversize from session agent (e2ediag AfterHello hook via TEMP marker).
	overBefore := readIPCStatus(t).Stats.Oversize
	overMarker := filepath.Join(ipcCmdDir(t), "oversize")
	if err := os.WriteFile(overMarker, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeIPCControl(t, map[string]any{"action": "drop"})
	waitIPCStat(t, func(s ipcStatus) bool {
		return s.Stats.Oversize > overBefore
	}, 30*time.Second, "oversize counter")
	waitServiceState(t, "Running", 30*time.Second)
	_ = os.Remove(overMarker)
	// Legitimate reconnect after oversize must succeed.
	waitIPCConnected(t, 45*time.Second)
	pidAfter = waitSessionAgent(t, 15*time.Second)

	// Version mismatch: TEMP force_protocol marker (user-writable).
	mismatchBefore := readIPCStatus(t).Stats.VersionMismatch
	hsBefore := readIPCStatus(t).Stats.HandshakeOK
	writeForceProtocol(t, 99)
	mustPowershell(t, fmt.Sprintf(`Stop-Process -Id %d -Force`, pidAfter))
	newPID := waitSessionAgentNot(t, pidAfter, 45*time.Second)
	waitIPCStat(t, func(s ipcStatus) bool {
		return s.Stats.VersionMismatch > mismatchBefore
	}, 30*time.Second, "version_mismatch counter")
	// Hold !connected for a window; handshake must not succeed for this agent.
	holdUntil := time.Now().Add(5 * time.Second)
	for time.Now().Before(holdUntil) {
		st := readIPCStatus(t)
		if anyConnected(st) {
			t.Fatalf("connected during version mismatch; status=%+v", st)
		}
		if st.Stats.HandshakeOK > hsBefore {
			t.Fatalf("handshake_ok advanced during mismatch: %d → %d", hsBefore, st.Stats.HandshakeOK)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if countSessionAgents(t) != 1 {
		t.Fatalf("respawn storm: agents=%d", countSessionAgents(t))
	}
	cur := sessionAgentPIDs(t)
	if len(cur) != 1 || cur[0] != newPID {
		t.Fatalf("PID churn during mismatch: %v want [%d]", cur, newPID)
	}
	waitServiceState(t, "Running", 10*time.Second)

	// Clear force_protocol; kill → good handshake again.
	writeForceProtocol(t, 0)
	mustPowershell(t, fmt.Sprintf(`Stop-Process -Id %d -Force`, newPID))
	_ = waitSessionAgentNot(t, newPID, 45*time.Second)
	waitIPCConnected(t, 45*time.Second)

	// ACL: different standard user cannot open the pipe.
	user, pass := createStandardUser(t, "djipc")
	err = attemptOpenPipeAs(user, pass, pipeName)
	if err == nil {
		t.Fatal("other user opened pipe")
	}
	if !isAccessDeniedErr(err) {
		t.Fatalf("other user pipe open: %v (want access denied)", err)
	}

	// No secrets on session agent.
	cmdLine := sessionAgentCommandLine(t, waitSessionAgent(t, 10*time.Second))
	if strings.Contains(strings.ToLower(cmdLine), "--token") || strings.Contains(cmdLine, token) {
		t.Fatalf("token on cmdline: %s", cmdLine)
	}
}

func waitIPCConnected(t *testing.T, timeout time.Duration) ipcStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st := readIPCStatus(t)
		if anyConnected(st) && st.Stats.HandshakeOK > 0 {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for IPC connected; status=%+v", st)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func waitIPCStat(t *testing.T, ok func(ipcStatus) bool, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st := readIPCStatus(t)
		if ok(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; status=%+v", label, st)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func readIPCStatus(t *testing.T) ipcStatus {
	t.Helper()
	raw, err := os.ReadFile(ipcStatusPath)
	if err != nil {
		return ipcStatus{Connected: map[string]bool{}}
	}
	var st ipcStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return ipcStatus{Connected: map[string]bool{}}
	}
	if st.Connected == nil {
		st.Connected = map[string]bool{}
	}
	return st
}

func anyConnected(st ipcStatus) bool {
	for _, v := range st.Connected {
		if v {
			return true
		}
	}
	return false
}

func firstConnectedSession(t *testing.T, st ipcStatus) uint32 {
	t.Helper()
	for k, v := range st.Connected {
		if !v {
			continue
		}
		var id uint32
		if _, err := fmt.Sscanf(k, "%d", &id); err == nil && id > 0 {
			return id
		}
	}
	t.Fatal("no connected session id")
	return 0
}

func writeIPCControl(t *testing.T, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ipcControlPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func spoofHello(t *testing.T, pipeName string, sessionID, fakePID uint32) {
	t.Helper()
	path, err := windows.UTF16PtrFromString(pipeName)
	if err != nil {
		t.Fatal(err)
	}
	var h windows.Handle
	deadline := time.Now().Add(15 * time.Second)
	for {
		h, err = windows.CreateFile(
			path,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err == nil {
			break
		}
		// Pipe may not be listening while a connection is active — drop and retry.
		writeIPCControl(t, map[string]any{"action": "drop"})
		if time.Now().After(deadline) {
			t.Fatalf("spoof CreateFile(%s) after retries: %v", pipeName, err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	f := os.NewFile(uintptr(h), pipeName)
	defer func() { _ = f.Close() }()
	msg := ipc.NewHello(ipc.ProtocolVersion, "spoof", sessionID, fakePID)
	payload, err := ipc.EncodeMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ipc.WriteFrame(f, payload); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

func assertPipeACL(t *testing.T, pipeName string, sessionAgentPID int) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		pipeName,
		windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		sd, err = windows.GetNamedSecurityInfo(
			pipeName,
			windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION,
		)
	}
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s): %v", pipeName, err)
	}
	sddl := sd.String()
	up := strings.ToUpper(sddl)
	if !strings.Contains(up, "SY") {
		t.Fatalf("pipe ACL missing SYSTEM: %s", sddl)
	}
	if hasAllowTrustee(sddl, "WD") || hasAllowTrustee(sddl, "AU") || hasAllowTrustee(sddl, "BU") {
		t.Fatalf("pipe ACL too open: %s", sddl)
	}
	if hasAllowTrustee(sddl, "BA") {
		t.Fatalf("pipe ACL must not grant Administrators: %s", sddl)
	}
	owner, err := processOwnerSID(uint32(sessionAgentPID))
	if err != nil {
		t.Fatalf("session agent SID: %v", err)
	}
	if owner != "" && !strings.Contains(strings.ToUpper(sddl), strings.ToUpper(owner)) {
		t.Fatalf("pipe ACL missing session user SID %s: %s", owner, sddl)
	}
}

func processOwnerSID(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			return "", err
		}
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return tu.User.Sid.String(), nil
}

func attemptOpenPipeAs(user, pass, pipeName string) error {
	return withUserToken(user, pass, func() error {
		_, err := ipc.OpenPipeFile(pipeName)
		return err
	})
}
