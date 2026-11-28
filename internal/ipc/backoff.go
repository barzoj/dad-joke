package ipc

import "time"

const (
	reconnectInitial = 250 * time.Millisecond
	reconnectCap     = 5 * time.Second
)

// ReconnectBackoff is the session-agent reconnect schedule: 250 ms → 5 s.
type ReconnectBackoff struct {
	next time.Duration
}

// Next returns the delay before the next reconnect attempt and advances.
func (b *ReconnectBackoff) Next() time.Duration {
	if b.next <= 0 {
		b.next = reconnectInitial
	}
	d := b.next
	next := b.next * 2
	if next > reconnectCap {
		next = reconnectCap
	}
	b.next = next
	return d
}

// Reset returns the schedule to the initial delay after a successful connect.
func (b *ReconnectBackoff) Reset() {
	b.next = 0
}
