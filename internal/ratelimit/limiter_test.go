package ratelimit_test

import (
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/ratelimit"
)

func TestLimiter_ExceededAfterMaxHits(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	l := ratelimit.New(ratelimit.Config{
		Window:  time.Minute,
		MaxHits: 3,
		Clock:   func() time.Time { return now },
	})

	for i := 0; i < 3; i++ {
		if _, exceeded := l.Exceeded("ip:1"); exceeded {
			t.Fatalf("hit %d: unexpectedly exceeded", i)
		}
		l.Record("ip:1")
	}
	retry, exceeded := l.Exceeded("ip:1")
	if !exceeded {
		t.Fatal("expected exceeded after 3 hits")
	}
	if retry < time.Second {
		t.Fatalf("retryAfter = %v", retry)
	}
}

func TestLimiter_WindowExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	l := ratelimit.New(ratelimit.Config{
		Window:  time.Minute,
		MaxHits: 1,
		Clock:   func() time.Time { return now },
	})
	l.Record("k")
	if _, exceeded := l.Exceeded("k"); !exceeded {
		t.Fatal("expected exceeded")
	}
	now = now.Add(61 * time.Second)
	if _, exceeded := l.Exceeded("k"); exceeded {
		t.Fatal("expected window to clear")
	}
}

func TestLimiter_Allow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	l := ratelimit.New(ratelimit.Config{
		Window:  time.Minute,
		MaxHits: 2,
		Clock:   func() time.Time { return now },
	})
	if _, ok := l.Allow("t"); !ok {
		t.Fatal("first allow")
	}
	if _, ok := l.Allow("t"); !ok {
		t.Fatal("second allow")
	}
	retry, ok := l.Allow("t")
	if ok {
		t.Fatal("third should be denied")
	}
	if retry < time.Second {
		t.Fatalf("retry = %v", retry)
	}
}

func TestLimiter_EvictsWhenFull(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	l := ratelimit.New(ratelimit.Config{
		Window:  time.Minute,
		MaxHits: 1,
		MaxKeys: 2,
		Clock:   func() time.Time { return now },
	})
	l.Record("a")
	l.Record("b")
	l.Record("c") // forces eviction
	// Should not panic; new key recorded.
	if _, exceeded := l.Exceeded("c"); !exceeded {
		// c has 1 hit with MaxHits=1 → exceeded
		t.Fatal("expected c exceeded")
	}
}
