// Package ratelimit provides a process-local sliding-window rate limiter.
//
// It is intentionally Redis-free: in-memory, bounded, and injectable-clock for tests.
package ratelimit

import (
	"sync"
	"time"
)

// Clock returns the current time. Tests inject a deterministic clock.
type Clock func() time.Time

// Limiter is a sliding-window counter keyed by opaque strings (IP, token id, …).
type Limiter struct {
	mu      sync.Mutex
	clock   Clock
	window  time.Duration
	maxHits int
	maxKeys int
	buckets map[string]*bucket
}

type bucket struct {
	hits []time.Time
}

// Config configures a Limiter. Zero values get safe defaults.
type Config struct {
	Window  time.Duration // lookback window (default 1m)
	MaxHits int           // max events per key per window (default 60)
	MaxKeys int           // bound on tracked keys (default 10_000)
	Clock   Clock         // default time.Now
}

// New builds a Limiter.
func New(cfg Config) *Limiter {
	if cfg.Window <= 0 {
		cfg.Window = time.Minute
	}
	if cfg.MaxHits <= 0 {
		cfg.MaxHits = 60
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = 10_000
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Limiter{
		clock:   cfg.Clock,
		window:  cfg.Window,
		maxHits: cfg.MaxHits,
		maxKeys: cfg.MaxKeys,
		buckets: make(map[string]*bucket),
	}
}

// Exceeded reports whether key is already at/over the limit without recording a hit.
// retryAfter is a whole-second duration until the oldest hit leaves the window (min 1s).
func (l *Limiter) Exceeded(key string) (retryAfter time.Duration, exceeded bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock().UTC()
	b := l.buckets[key]
	if b == nil {
		return 0, false
	}
	l.pruneLocked(b, now)
	if len(b.hits) < l.maxHits {
		return 0, false
	}
	return retryAfterFrom(b.hits[0], now, l.window), true
}

// Record adds a hit for key.
func (l *Limiter) Record(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock().UTC()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= l.maxKeys {
			l.evictOneLocked(now)
		}
		b = &bucket{}
		l.buckets[key] = b
	}
	l.pruneLocked(b, now)
	b.hits = append(b.hits, now)
}

// Allow records a hit when under the limit. Returns ok=false with Retry-After when
// the key is already exhausted (the rejected attempt is not recorded again).
func (l *Limiter) Allow(key string) (retryAfter time.Duration, ok bool) {
	if retry, exceeded := l.Exceeded(key); exceeded {
		return retry, false
	}
	l.Record(key)
	return 0, true
}

func (l *Limiter) pruneLocked(b *bucket, now time.Time) {
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(b.hits) && !b.hits[i].After(cutoff) {
		i++
	}
	if i > 0 {
		b.hits = append([]time.Time(nil), b.hits[i:]...)
	}
}

func (l *Limiter) evictOneLocked(now time.Time) {
	for k, b := range l.buckets {
		l.pruneLocked(b, now)
		if len(b.hits) == 0 {
			delete(l.buckets, k)
			return
		}
	}
	for k := range l.buckets {
		delete(l.buckets, k)
		return
	}
}

func retryAfterFrom(oldest, now time.Time, window time.Duration) time.Duration {
	until := oldest.Add(window).Sub(now)
	if until < time.Second {
		return time.Second
	}
	secs := until / time.Second
	if until%time.Second != 0 {
		secs++
	}
	return secs * time.Second
}
