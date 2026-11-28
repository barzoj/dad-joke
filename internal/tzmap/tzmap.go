// Package tzmap maps Windows time-zone ids to IANA names and reports the
// machine's current zone + UTC offset for heartbeats and fact batches.
package tzmap

import (
	"fmt"
	"log"
	"time"

	// Embed IANA zone database so LoadLocation works on Windows/minimal images.
	_ "time/tzdata"
)

// Resolve returns the machine's IANA zone id and UTC offset (minutes east of
// UTC) at now. On Windows this uses GetDynamicTimeZoneInformation + the
// embedded CLDR table; elsewhere it uses the host local zone.
func Resolve(now time.Time) (iana string, offsetMinutes int32) {
	if winID, ok := windowsZoneID(); ok && winID != "" {
		return FromWindows(winID, now)
	}
	iana = localIANAName()
	if iana == "" {
		iana = "UTC"
	}
	loc, err := time.LoadLocation(iana)
	if err != nil {
		_, off := now.In(time.Local).Zone()
		return "UTC", offsetMinutesFromSec(off)
	}
	_, off := now.In(loc).Zone()
	return iana, offsetMinutesFromSec(off)
}

// FromWindows maps a Windows time-zone key name to an IANA id. Unmapped zones
// fall back to Etc/GMT±H when the offset is a whole number of hours, else UTC,
// and emit a warning.
func FromWindows(windowsID string, now time.Time) (iana string, offsetMinutes int32) {
	if mapped, ok := windowsToIANA[windowsID]; ok {
		if loc, err := time.LoadLocation(mapped); err == nil {
			_, off := now.In(loc).Zone()
			return mapped, offsetMinutesFromSec(off)
		}
	}
	_, offsetSec := now.In(time.Local).Zone()
	offsetMinutes = offsetMinutesFromSec(offsetSec)
	iana = fallbackIANA(offsetMinutes)
	log.Printf("tzmap: unmapped Windows zone %q; falling back to %s (offset=%d)", windowsID, iana, offsetMinutes)
	return iana, offsetMinutes
}

// LookupIANA returns the embedded IANA id for a Windows zone key, if any.
func LookupIANA(windowsID string) (string, bool) {
	v, ok := windowsToIANA[windowsID]
	return v, ok
}

// FallbackIANA exposes the unmapped-zone fallback for tests.
func FallbackIANA(offsetMinutes int32) string {
	return fallbackIANA(offsetMinutes)
}

func offsetMinutesFromSec(offsetSec int) int32 {
	mins := offsetSec / 60
	if mins > 840 {
		return 840
	}
	if mins < -840 {
		return -840
	}
	return int32(mins) // #nosec G115 -- clamped to ±840 above
}

func fallbackIANA(offsetMinutes int32) string {
	if offsetMinutes%60 != 0 {
		return "UTC"
	}
	hours := int(offsetMinutes / 60)
	// Etc/GMT signs are inverted relative to the usual UTC offset.
	if hours == 0 {
		return "Etc/UTC"
	}
	if hours > 0 {
		return fmt.Sprintf("Etc/GMT-%d", hours)
	}
	return fmt.Sprintf("Etc/GMT+%d", -hours)
}
