package usage_test

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/internal/usage"
)

func TestLocalDate_ZonesAndBoundaries(t *testing.T) {
	t.Parallel()
	// 2026-11-28T23:30:00Z
	instant := time.Date(2026, 11, 28, 23, 30, 0, 0, time.UTC)
	cases := []struct {
		zone string
		want string
	}{
		{"UTC", "2026-11-28"},
		{"Europe/Amsterdam", "2026-11-29"}, // UTC+1 in November
		{"America/New_York", "2026-11-28"}, // UTC-5
		{"Asia/Kolkata", "2026-11-29"},     // UTC+5:30 → 05:00 next day
		{"Pacific/Kiritimati", "2026-11-29"}, // UTC+14
		{"Etc/GMT+12", "2026-11-28"},         // Etc/GMT+12 is UTC-12
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()
			got, err := usage.LocalDate(instant, tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestLocalDate_MidnightAndAlmostMidnight(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	midnight := time.Date(2026, 11, 29, 0, 0, 0, 0, loc)
	got, err := usage.LocalDate(midnight.UTC(), "Europe/Amsterdam")
	if err != nil || got != "2026-11-29" {
		t.Fatalf("midnight: %s %v", got, err)
	}
	almost := time.Date(2026, 11, 28, 23, 59, 59, 999000000, loc)
	got, err = usage.LocalDate(almost.UTC(), "Europe/Amsterdam")
	if err != nil || got != "2026-11-28" {
		t.Fatalf("23:59:59.999: %s %v", got, err)
	}
}

func TestLocalDate_DSTSpringAndFall(t *testing.T) {
	t.Parallel()
	// Netherlands spring-forward 2026-03-29 02:00 → 03:00
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	for _, local := range []time.Time{
		time.Date(2026, 3, 29, 0, 30, 0, 0, loc),
		time.Date(2026, 3, 29, 1, 30, 0, 0, loc),
	} {
		got, err := usage.LocalDate(local.UTC(), "Europe/Amsterdam")
		if err != nil || got != "2026-03-29" {
			t.Fatalf("spring %v → %s (%v)", local, got, err)
		}
	}
	// Fall-back 2026-10-25: repeated 02:00 hour — both land on 2026-10-25
	before := time.Date(2026, 10, 25, 2, 30, 0, 0, loc)
	got, err := usage.LocalDate(before.UTC(), "Europe/Amsterdam")
	if err != nil || got != "2026-10-25" {
		t.Fatalf("fall-back: %s %v", got, err)
	}
}

func TestLocalDate_LeapDayAndYearBoundary(t *testing.T) {
	t.Parallel()
	leap := time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC)
	got, err := usage.LocalDate(leap, "UTC")
	if err != nil || got != "2028-02-29" {
		t.Fatalf("leap: %s %v", got, err)
	}
	nye := time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC)
	got, err = usage.LocalDate(nye, "Europe/Amsterdam")
	if err != nil || got != "2027-01-01" {
		t.Fatalf("year boundary: %s %v", got, err)
	}
}

func TestLocalDate_InvalidZone(t *testing.T) {
	t.Parallel()
	if _, err := usage.LocalDate(time.Now().UTC(), "Not/AZone"); err == nil {
		t.Fatal("expected error")
	}
	if err := usage.LoadZone(""); err == nil {
		t.Fatal("empty zone")
	}
	if err := usage.LoadZone("Nope"); err == nil {
		t.Fatal("invalid zone")
	}
}

func TestCheckZoneOffset(t *testing.T) {
	t.Parallel()
	sent := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	if err := usage.CheckZoneOffset(sent, "UTC", 0); err != nil {
		t.Fatal(err)
	}
	if err := usage.CheckZoneOffset(sent, "UTC", 60); err == nil {
		t.Fatal("want mismatch")
	}
	// Asia/Kolkata +330
	if err := usage.CheckZoneOffset(sent, "Asia/Kolkata", 330); err != nil {
		t.Fatal(err)
	}
	// DST transition: Europe/Amsterdam spring-forward night 2026-03-29
	// After 01:00 CET → CEST (+120). At 00:30 local still CET (+60).
	loc, _ := time.LoadLocation("Europe/Amsterdam")
	before := time.Date(2026, 3, 29, 0, 30, 0, 0, loc)
	off, err := usage.OffsetMinutesAt(before.UTC(), "Europe/Amsterdam")
	if err != nil || off != 60 {
		t.Fatalf("before spring off=%d err=%v", off, err)
	}
	after := time.Date(2026, 3, 29, 3, 30, 0, 0, loc)
	off, err = usage.OffsetMinutesAt(after.UTC(), "Europe/Amsterdam")
	if err != nil || off != 120 {
		t.Fatalf("after spring off=%d err=%v", off, err)
	}
	if err := usage.CheckZoneOffset(before.UTC(), "Europe/Amsterdam", 60); err != nil {
		t.Fatal(err)
	}
	if err := usage.CheckZoneOffset(after.UTC(), "Europe/Amsterdam", 120); err != nil {
		t.Fatal(err)
	}
}

func TestTSServer(t *testing.T) {
	t.Parallel()
	serverNow := time.Date(2026, 11, 28, 12, 10, 0, 0, time.UTC)
	sentAt := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC) // 10 min behind
	ts := sentAt.Add(-time.Minute)
	got := usage.TSServer(ts, sentAt, serverNow)
	want := serverNow.Add(-time.Minute)
	if !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
	// Future sent_at
	sentFuture := serverNow.Add(5 * time.Minute)
	got = usage.TSServer(sentFuture, sentFuture, serverNow)
	if !got.Equal(serverNow) {
		t.Fatalf("future sent_at: got %s want %s", got, serverNow)
	}
}

func TestTruncateRunes(t *testing.T) {
	t.Parallel()
	got, err := usage.TruncateRunes(strings.Repeat("a", 255), 256)
	if err != nil || utf8.RuneCountInString(got) != 255 {
		t.Fatalf("255 ascii: %q %v", got, err)
	}
	got, err = usage.TruncateRunes(strings.Repeat("a", 256), 256)
	if err != nil || utf8.RuneCountInString(got) != 256 {
		t.Fatalf("256 ascii")
	}
	got, err = usage.TruncateRunes(strings.Repeat("a", 257), 256)
	if err != nil || utf8.RuneCountInString(got) != 256 {
		t.Fatalf("257 ascii → %d", utf8.RuneCountInString(got))
	}
	cyr := strings.Repeat("я", 300)
	got, err = usage.TruncateRunes(cyr, 256)
	if err != nil || utf8.RuneCountInString(got) != 256 {
		t.Fatalf("cyrillic: %d %v", utf8.RuneCountInString(got), err)
	}
	// Emoji ZWJ family sequence — truncate by runes, never mid-code-point
	emoji := strings.Repeat("👨‍👩‍👧‍👦", 100) // each cluster is multiple runes
	got, err = usage.TruncateRunes(emoji, 256)
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(got) != 256 {
		t.Fatalf("emoji runes=%d", utf8.RuneCountInString(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("invalid utf8 after truncate")
	}
	// Combining marks
	comb := strings.Repeat("e\u0301", 200) // e + combining acute
	got, err = usage.TruncateRunes(comb, 256)
	if err != nil || utf8.RuneCountInString(got) != 256 {
		t.Fatalf("combining: %d %v", utf8.RuneCountInString(got), err)
	}
	if _, err := usage.TruncateRunes(string([]byte{0xff, 0xfe}), 10); err == nil {
		t.Fatal("invalid utf8")
	}
}

func TestBuildDeltas(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	deltas, err := usage.BuildDeltas([]usage.SampleInput{
		{Kind: "proc.sample", DurS: 60, TSServer: ts, TimeZone: "UTC", Exes: []string{"notepad.exe", "notepad.exe", "chrome.exe"}},
		{Kind: "fg.sample", DurS: 60, TSServer: ts, TimeZone: "UTC", Exe: "notepad.exe"},
		{Kind: "fg.sample", DurS: 30, TSServer: ts, TimeZone: "UTC", Exe: ""}, // empty exe ignored
		{Kind: "session.state", DurS: 10, TSServer: ts, TimeZone: "UTC"},
		{Kind: "proc.sample", DurS: 0, TSServer: ts, TimeZone: "UTC", Exes: []string{"x.exe"}}, // 0 dropped
	})
	if err != nil {
		t.Fatal(err)
	}
	byBucket := map[string]int64{}
	for _, d := range deltas {
		byBucket[d.Bucket] = d.Seconds
		if d.Date != "2026-11-28" {
			t.Fatalf("date=%s", d.Date)
		}
	}
	if byBucket["app:notepad.exe"] != 60 {
		t.Fatalf("notepad app=%d", byBucket["app:notepad.exe"])
	}
	if byBucket["app:chrome.exe"] != 60 {
		t.Fatalf("chrome app=%d", byBucket["app:chrome.exe"])
	}
	if byBucket["fg:notepad.exe"] != 60 {
		t.Fatalf("notepad fg=%d", byBucket["fg:notepad.exe"])
	}
	if _, ok := byBucket["app:x.exe"]; ok {
		t.Fatal("0-second delta should be dropped")
	}
	// Case sensitivity: Notepad.exe ≠ notepad.exe
	deltas, err = usage.BuildDeltas([]usage.SampleInput{
		{Kind: "proc.sample", DurS: 10, TSServer: ts, TimeZone: "UTC", Exes: []string{"Notepad.exe", "notepad.exe"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 2 {
		t.Fatalf("case-sensitive buckets want 2 got %d", len(deltas))
	}
}

func TestMergeApps(t *testing.T) {
	t.Parallel()
	apps := usage.MergeApps([]struct {
		Bucket  string
		Seconds int64
	}{
		{Bucket: "app:z.exe", Seconds: 10},
		{Bucket: "app:a.exe", Seconds: 5},
		{Bucket: "fg:a.exe", Seconds: 3},
		{Bucket: "fg:a.exe", Seconds: 2},
	})
	if len(apps) != 2 {
		t.Fatalf("len=%d", len(apps))
	}
	if apps[0].Exe != "a.exe" || apps[0].Seconds != 5 || apps[0].FgSeconds != 5 {
		t.Fatalf("a.exe = %+v", apps[0])
	}
	if apps[1].Exe != "z.exe" || apps[1].Seconds != 10 || apps[1].FgSeconds != 0 {
		t.Fatalf("z.exe = %+v", apps[1])
	}
	empty := usage.MergeApps(nil)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v", empty)
	}
}
