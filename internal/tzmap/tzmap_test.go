package tzmap

import (
	"testing"
	"time"
)

func TestWindowsTableLoads(t *testing.T) {
	t.Parallel()
	for win, iana := range windowsToIANA {
		if _, err := time.LoadLocation(iana); err != nil {
			t.Errorf("%q → %q: %v", win, iana, err)
		}
	}
}

func TestFromWindowsMapped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	iana, off := FromWindows("W. Europe Standard Time", now)
	if iana != "Europe/Berlin" {
		t.Fatalf("iana=%q", iana)
	}
	if off != 120 { // CEST
		t.Fatalf("offset=%d want 120", off)
	}
	nowWinter := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	_, offW := FromWindows("W. Europe Standard Time", nowWinter)
	if offW != 60 {
		t.Fatalf("winter offset=%d want 60", offW)
	}
}

func TestFromWindowsUnknownFallback(t *testing.T) {
	t.Parallel()
	if got := FallbackIANA(60); got != "Etc/GMT-1" {
		t.Fatalf("got %q", got)
	}
	if got := FallbackIANA(-60); got != "Etc/GMT+1" {
		t.Fatalf("got %q", got)
	}
	if got := FallbackIANA(30); got != "UTC" {
		t.Fatalf("half-hour got %q", got)
	}
	if got := FallbackIANA(0); got != "Etc/UTC" {
		t.Fatalf("got %q", got)
	}
}

func TestLookupIANA(t *testing.T) {
	t.Parallel()
	v, ok := LookupIANA("Tokyo Standard Time")
	if !ok || v != "Asia/Tokyo" {
		t.Fatalf("%q %v", v, ok)
	}
	if _, ok := LookupIANA("Not A Real Zone"); ok {
		t.Fatal("expected miss")
	}
}
