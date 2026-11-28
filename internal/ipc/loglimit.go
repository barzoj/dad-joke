package ipc

import (
	"sync"
	"time"
)

// LogFunc is a minimal logger (format + args).
type LogFunc func(format string, args ...any)

// RateLimiter bounds log spam for repeated framing/handshake errors.
type RateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
	suppressed int
	Clock    func() time.Time
}

// NewRateLimiter returns a limiter that allows one log every interval.
func NewRateLimiter(interval time.Duration) *RateLimiter {
	if interval <= 0 {
		interval = time.Second
	}
	return &RateLimiter{interval: interval, Clock: time.Now}
}

// Allow reports whether a log line should be emitted now. When false, the
// suppressed count is incremented; the next allowed call receives that count
// via Took() so callers can mention "suppressed N similar".
func (r *RateLimiter) Allow() (ok bool, suppressed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Clock()
	if r.last.IsZero() || now.Sub(r.last) >= r.interval {
		suppressed = r.suppressed
		r.suppressed = 0
		r.last = now
		return true, suppressed
	}
	r.suppressed++
	return false, 0
}
