// Package usage holds pure day-bucketing, zone checks, and app aggregate helpers.
// It has no I/O so DST/zone logic is unit-testable without Mongo.
package usage

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// AppBucketPrefix is the usage_daily bucket prefix for proc.sample seconds.
	AppBucketPrefix = "app:"
	// FgBucketPrefix is the usage_daily bucket prefix for fg.sample seconds.
	FgBucketPrefix = "fg:"
)

// LocalDate returns the calendar date (YYYY-MM-DD) of instant in the named IANA zone.
func LocalDate(instant time.Time, timeZone string) (string, error) {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return "", fmt.Errorf("invalid time_zone %q: %w", timeZone, err)
	}
	return instant.In(loc).Format("2006-01-02"), nil
}

// OffsetMinutesAt returns the zone's UTC offset in minutes at instant.
func OffsetMinutesAt(instant time.Time, timeZone string) (int32, error) {
	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return 0, fmt.Errorf("invalid time_zone %q: %w", timeZone, err)
	}
	_, offset := instant.In(loc).Zone()
	return int32(offset / 60), nil
}

// CheckZoneOffset loads timeZone and verifies utcOffsetMinutes matches the
// zone's offset at instant (typically sent_at).
func CheckZoneOffset(instant time.Time, timeZone string, utcOffsetMinutes int32) error {
	got, err := OffsetMinutesAt(instant, timeZone)
	if err != nil {
		return err
	}
	if got != utcOffsetMinutes {
		return fmt.Errorf("utc_offset_minutes %d does not match time_zone %q at %s (want %d)",
			utcOffsetMinutes, timeZone, instant.UTC().Format(time.RFC3339), got)
	}
	return nil
}

// LoadZone returns an error when timeZone is not a loadable IANA id.
func LoadZone(timeZone string) error {
	if strings.TrimSpace(timeZone) == "" {
		return fmt.Errorf("time_zone is required")
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		return fmt.Errorf("invalid time_zone %q", timeZone)
	}
	return nil
}

// TSServer corrects a client fact timestamp for clock skew:
// ts_server = ts + (serverNow − sentAt).
func TSServer(ts, sentAt, serverNow time.Time) time.Time {
	skew := serverNow.Sub(sentAt)
	return ts.Add(skew)
}

// TruncateRunes truncates s to at most maxRunes without splitting a code point.
// Invalid UTF-8 returns an error.
func TruncateRunes(s string, maxRunes int) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("invalid UTF-8")
	}
	if maxRunes < 0 {
		maxRunes = 0
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s, nil
	}
	i := 0
	for n := 0; n < maxRunes; n++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i], nil
}

// Delta is one in-memory usage increment before persistence.
type Delta struct {
	Date     string
	TimeZone string
	Bucket   string
	Seconds  int64
}

// SampleInput is the subset of a fact needed to build usage deltas.
type SampleInput struct {
	Kind     string
	DurS     int32
	TSServer time.Time
	TimeZone string
	Exes     []string // proc.sample
	Exe      string   // fg.sample
}

// BuildDeltas converts sample facts into summed-per-_id usage deltas.
// Zero-second and non-contributing kinds are dropped. Exe names are stored
// as given (case-sensitive). Duplicate exes within one fact are deduped.
func BuildDeltas(samples []SampleInput) ([]Delta, error) {
	sum := make(map[string]*Delta) // key = date|bucket
	for _, s := range samples {
		date, err := LocalDate(s.TSServer, s.TimeZone)
		if err != nil {
			return nil, err
		}
		switch s.Kind {
		case "proc.sample":
			if s.DurS <= 0 {
				continue
			}
			seen := make(map[string]struct{}, len(s.Exes))
			for _, exe := range s.Exes {
				if exe == "" {
					continue
				}
				if _, ok := seen[exe]; ok {
					continue
				}
				seen[exe] = struct{}{}
				addDelta(sum, date, s.TimeZone, AppBucketPrefix+exe, int64(s.DurS))
			}
		case "fg.sample":
			if s.DurS <= 0 || s.Exe == "" {
				continue
			}
			addDelta(sum, date, s.TimeZone, FgBucketPrefix+s.Exe, int64(s.DurS))
		default:
			// session.state, queue.dropped, session.restarted: no usage.
		}
	}
	out := make([]Delta, 0, len(sum))
	for _, d := range sum {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Bucket < out[j].Bucket
	})
	return out, nil
}

func addDelta(sum map[string]*Delta, date, tz, bucket string, seconds int64) {
	if seconds == 0 {
		return
	}
	key := date + "|" + bucket
	if cur, ok := sum[key]; ok {
		cur.Seconds += seconds
		cur.TimeZone = tz // latest batch zone for that day/bucket in this call
		return
	}
	sum[key] = &Delta{Date: date, TimeZone: tz, Bucket: bucket, Seconds: seconds}
}

// AppRow is the ControlAPI/MCP per-app usage shape.
type AppRow struct {
	Exe       string
	Seconds   int64
	FgSeconds int64
}

// MergeApps folds app:/fg: bucket rows into AppRow list sorted by exe ascending.
// Empty input yields a non-nil empty slice.
func MergeApps(buckets []struct {
	Bucket  string
	Seconds int64
}) []AppRow {
	byExe := make(map[string]*AppRow)
	for _, b := range buckets {
		switch {
		case strings.HasPrefix(b.Bucket, AppBucketPrefix):
			exe := strings.TrimPrefix(b.Bucket, AppBucketPrefix)
			row := byExe[exe]
			if row == nil {
				row = &AppRow{Exe: exe}
				byExe[exe] = row
			}
			row.Seconds += b.Seconds
		case strings.HasPrefix(b.Bucket, FgBucketPrefix):
			exe := strings.TrimPrefix(b.Bucket, FgBucketPrefix)
			row := byExe[exe]
			if row == nil {
				row = &AppRow{Exe: exe}
				byExe[exe] = row
			}
			row.FgSeconds += b.Seconds
		}
	}
	out := make([]AppRow, 0, len(byExe))
	for _, row := range byExe {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Exe < out[j].Exe })
	return out
}

// DailyID builds usage_daily _id: "<tenant>|<machine>|<YYYY-MM-DD>|<bucket>".
func DailyID(tenantID, machineID, date, bucket string) string {
	return tenantID + "|" + machineID + "|" + date + "|" + bucket
}
