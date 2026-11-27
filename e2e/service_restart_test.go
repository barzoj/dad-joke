//go:build e2e && windows

package e2e_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestTC16_ServiceRestartPersistsConfigAndToken covers Issue #16 AC #1/#8:
// Restart-Service keeps config + token; agent reconnects and heartbeats without re-enrollment.
func TestTC16_ServiceRestartPersistsConfigAndToken(t *testing.T) {
	bin := testBinary(t)
	cleanupInstall(t, bin)
	t.Cleanup(func() { cleanupInstall(t, bin) })

	mock := startMockAgentAPI(t)
	token := "e2e-restart-token-" + randomSuffix(8)

	out, err := runAdmin(t, bin,
		"install",
		"--token", token,
		"--backend-url", mock.URL(),
		"--heartbeat-interval", "2",
	)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	waitServiceState(t, "Running", 60*time.Second)

	cfgBefore := readConfigJSON(t)
	machineID, _ := cfgBefore["machine_id"].(string)
	if machineID == "" {
		t.Fatal("missing machine_id")
	}
	if cfgBefore["token"] != token {
		t.Fatalf("token mismatch before restart")
	}
	if cfgBefore["backend_url"] != mock.URL() {
		t.Fatalf("backend_url = %v", cfgBefore["backend_url"])
	}

	hitsBefore := waitForHeartbeat(t, mock, 1, 20*time.Second)
	if hitsBefore[0].Auth != "Bearer "+token {
		t.Fatalf("pre-restart auth = %q", hitsBefore[0].Auth)
	}

	mustPowershell(t, fmt.Sprintf(`Restart-Service -Name '%s' -Force`, serviceName))
	waitServiceState(t, "Running", 60*time.Second)

	cfgAfter := readConfigJSON(t)
	if cfgAfter["machine_id"] != machineID {
		t.Fatalf("machine_id changed across restart: %v -> %v", machineID, cfgAfter["machine_id"])
	}
	if cfgAfter["token"] != token {
		t.Fatal("token not persisted across Restart-Service")
	}
	if cfgAfter["backend_url"] != mock.URL() {
		t.Fatalf("backend_url not persisted: %v", cfgAfter["backend_url"])
	}
	if int(cfgAfter["heartbeat_interval_seconds"].(float64)) != 2 {
		t.Fatalf("heartbeat_interval_seconds not persisted: %v", cfgAfter["heartbeat_interval_seconds"])
	}

	// Agent must reconnect and heartbeat again without re-enrollment (same token + machine_id).
	need := len(hitsBefore) + 1
	hitsAfter := waitForHeartbeat(t, mock, need, 30*time.Second)
	last := hitsAfter[len(hitsAfter)-1]
	if last.Auth != "Bearer "+token {
		t.Fatalf("post-restart auth = %q", last.Auth)
	}
	if !strings.Contains(last.Body, `"machine_id":"`+machineID+`"`) {
		t.Fatalf("post-restart body missing machine_id: %s", last.Body)
	}
}
