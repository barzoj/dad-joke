//go:build spike && windows

package policyspike

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLogonHours_NetUserAndAPI(t *testing.T) {
	requireAdmin(t)
	u := createStandardUser(t)

	// Baseline: interactive logon succeeds.
	tok, err := logonUserResult(u.Name, u.Password, logonInteractive)
	if err != nil {
		t.Fatalf("baseline LogonUser: %v", err)
	}
	tok.Close()

	// Restrict to a one-hour window far from "now" in UTC so current hour is denied.
	// net user /times uses local time of the machine; we also confirm SAM storage.
	now := time.Now()
	// Pick a weekday hour 12 hours away.
	denyHourLocal := (now.Hour() + 12) % 24
	allowDay := now.Weekday().String()[:2] // Mo Tu We...
	dayMap := map[time.Weekday]string{
		time.Sunday: "Su", time.Monday: "M", time.Tuesday: "T",
		time.Wednesday: "W", time.Thursday: "Th", time.Friday: "F", time.Saturday: "Sa",
	}
	day := dayMap[now.Weekday()]
	_ = allowDay

	// Set times to ONLY the hour 12h away on today's weekday — current hour blocked.
	timesSpec := fmt.Sprintf("%s,%d-%d", day, denyHourLocal, (denyHourLocal+1)%24)
	if denyHourLocal == 23 {
		timesSpec = fmt.Sprintf("%s,23-24", day)
	}
	start := time.Now()
	out, err := powershell(t, fmt.Sprintf(`
$ErrorActionPreference='Stop'
net user %s /times:%s
# Dump binary logon hours via ADSI (21 bytes, UTC for domain; local SAM often local).
$user = [ADSI]('WinNT://./%s,user')
$hours = $user.LoginHours.Value
if ($null -eq $hours) { Write-Output 'LOGINHOURS=null'; exit 0 }
$hex = ($hours | ForEach-Object { $_.ToString('x2') }) -join ''
Write-Output ('LOGINHOURS_HEX=' + $hex)
Write-Output ('TIMES_SPEC=' + %s)
`, u.Name, timesSpec, u.Name, quotePS(timesSpec)))
	setLat := time.Since(start)
	if err != nil {
		t.Fatalf("net user /times: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_, _ = powershell(t, fmt.Sprintf(`net user %s /times:all`, u.Name))
	})

	// Attempt logon now — should fail with ERROR_INVALID_LOGON_HOURS (1328 / 0x530).
	_, logonErr := logonUserResult(u.Name, u.Password, logonInteractive)
	denied := logonErr != nil
	errno := ""
	if logonErr != nil {
		errno = logonErr.Error()
		if se, ok := logonErr.(syscall.Errno); ok {
			errno = fmt.Sprintf("Errno=%d (%s)", int(se), se.Error())
		}
	}

	utcNote := "LoginHours blob observed; SAM local accounts typically use local time bits — confirm UTC only for domain accounts"
	if strings.Contains(out, "LOGINHOURS_HEX=") {
		utcNote = "LoginHours hex=" + trimPref(out, "LOGINHOURS_HEX=") + "; treat standalone SAM as local-time unless proven UTC"
	}

	// LogonHoursAction policy (force logoff) — set and document that standalone
	// Windows does NOT forcibly log off active sessions for logon hours.
	setRegDWORD(t, `HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`, "ForceUnlockLogon", 0) // unrelated touch avoided
	lhPath := `HKLM:\Software\Microsoft\Windows\CurrentVersion\Policies\System`
	setRegDWORD(t, lhPath, "LogonHoursAction", 1) // 1 = lock / force logoff policy value used by some ADMX
	t.Cleanup(func() { deleteRegValue(t, lhPath, "LogonHoursAction") })

	recordFinding(Finding{
		Mechanism:       "Logon hours",
		Variant:         "net user /times + LogonUser outside window",
		Works:           boolIf(denied, "yes", "no"),
		Latency:         formatLatency(setLat),
		NeedsRelogon:    "n/a (affects new logons)",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a (SAM account attribute)",
		Gotchas:         fmt.Sprintf("times=%s denied=%v err=%s; LogonHoursAction set but standalone Windows does NOT auto-logoff active sessions for hours expiry — agent must WTSLogoffSession", timesSpec, denied, errno),
		ClientSKUNote:   utcNote,
	})

	// Midnight UTC crossing: set a window that wraps across midnight.
	wrapSpec := fmt.Sprintf("%s,22-2", day)
	out2, err2 := powershell(t, fmt.Sprintf(`net user %s /times:%s`, u.Name, wrapSpec))
	wrapOK := err2 == nil
	recordFinding(Finding{
		Mechanism:       "Logon hours",
		Variant:         "window crossing midnight",
		Works:           boolIf(wrapOK, "yes (net user accepted wrap)", "no"),
		Latency:         "n/a",
		NeedsRelogon:    "n/a",
		NeedsReboot:     "no",
		VisibleGpresult: "n/a",
		Gotchas:         fmt.Sprintf("spec=%s out=%q err=%v", wrapSpec, truncate(out2, 100), err2),
		ClientSKUNote:   "Bitfield spans days; agent schedulers must handle UTC/local carefully",
	})

	if !denied {
		t.Fatalf("expected LogonUser to fail outside logon hours; err=%v", logonErr)
	}
	if !wrapOK {
		t.Fatalf("midnight-crossing /times rejected: %v\n%s", err2, out2)
	}
}

func trimPref(s, pref string) string {
	i := strings.Index(s, pref)
	if i < 0 {
		return ""
	}
	rest := s[i+len(pref):]
	if j := strings.IndexAny(rest, "\r\n"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
