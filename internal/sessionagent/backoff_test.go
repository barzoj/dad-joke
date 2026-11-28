package sessionagent

import (
	"testing"
	"time"
)

func TestBackoffSequence(t *testing.T) {
	t.Parallel()
	var b sessionBackoff
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var got []time.Duration
	for i := 0; i < 8; i++ {
		d, ok := b.allowRelaunch(now)
		if !ok {
			t.Fatalf("step %d not ok", i)
		}
		got = append(got, d)
		now = now.Add(d)
	}
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestBackoffCapThenClear(t *testing.T) {
	t.Parallel()
	var b sessionBackoff
	now := time.Now()
	for i := 0; i < relaunchMaxInWindow; i++ {
		if _, ok := b.allowRelaunch(now); !ok {
			t.Fatalf("unexpected cap at %d", i)
		}
	}
	if _, ok := b.allowRelaunch(now); ok {
		t.Fatal("expected cap")
	}
	if !b.capped {
		t.Fatal("capped flag")
	}
	b.clearCapOnEvent()
	if b.capped {
		t.Fatal("cap not cleared")
	}
	d, ok := b.allowRelaunch(now)
	if !ok || d != time.Second {
		t.Fatalf("after clear: %v %v", d, ok)
	}
}
