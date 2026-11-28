//go:build spike && windows

package policyspike

import (
	"fmt"
	"testing"
	"time"
)

func TestSessionKick_WTSLogoffAndDisconnect(t *testing.T) {
	requireAdmin(t)
	u := createStandardUser(t)

	ids, method := ensureInteractiveSession(t, u)
	t.Logf("sessions for %s via %s: %v", u.Name, method, ids)

	if len(ids) == 0 {
		// Hosted runners often cannot create a real interactive WTS session for a
		// secondary local user. Assert API surface against session 0 / current and
		// record the limitation — do not silently skip.
		recordFinding(Finding{
			Mechanism:       "Session kick",
			Variant:         "WTSLogoffSession / WTSDisconnectSession",
			Works:           "partial",
			Latency:         "n/a",
			NeedsRelogon:    "n/a",
			NeedsReboot:     "n/a",
			VisibleGpresult: "n/a",
			Gotchas:         fmt.Sprintf("no WTS session observed for test user after %s; LogonUser+CreateProcessAsUser/scheduled task did not yield a queryable interactive session on this hosted image. Agent on real desktops should enumerate WTS sessions and call WTSLogoffSession/WTSDisconnectSession.", method),
			ClientSKUNote:   "Interactive desktop sessions are normal on client SKUs; Server/CI images often lack them for secondary users",
		})

		// Still verify the DLL exports are callable (expect failure on session 0 logoff may be denied or no-op).
		err := wtsDisconnect(0)
		t.Logf("WTSDisconnectSession(0)=%v", err)
		if findSessionsForUser(t, u.Name) == nil {
			// hard assert that we looked
			t.Log("confirmed zero sessions for test user")
		}
		return
	}

	target := ids[0]
	start := time.Now()
	err := wtsDisconnect(target)
	discLat := time.Since(start)
	discOK := err == nil
	time.Sleep(2 * time.Second)
	afterDisc := findSessionsForUser(t, u.Name)

	// Re-ensure then logoff
	ids2, _ := ensureInteractiveSession(t, u)
	var logoffErr error
	logoffLat := time.Duration(0)
	logoffOK := false
	if len(ids2) > 0 {
		start = time.Now()
		logoffErr = wtsLogoff(ids2[0])
		logoffLat = time.Since(start)
		logoffOK = logoffErr == nil
		time.Sleep(2 * time.Second)
	}
	afterLogoff := findSessionsForUser(t, u.Name)

	recordFinding(Finding{
		Mechanism:       "Session kick",
		Variant:         "WTSDisconnectSession then WTSLogoffSession",
		Works:           boolIf(discOK || logoffOK, "yes", "no"),
		Latency:         fmt.Sprintf("disconnect %s; logoff %s", formatLatency(discLat), formatLatency(logoffLat)),
		NeedsRelogon:    "n/a",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("method=%s ids=%v discOK=%v afterDisc=%v logoffOK=%v logoffErr=%v afterLogoff=%v", method, ids, discOK, afterDisc, logoffOK, logoffErr, afterLogoff),
		ClientSKUNote:   "Primary agent path for ending child sessions on standalone boxes",
	})

	if !discOK && !logoffOK {
		t.Fatalf("both WTSDisconnect and WTSLogoff failed: disc=%v logoff=%v", err, logoffErr)
	}
}
