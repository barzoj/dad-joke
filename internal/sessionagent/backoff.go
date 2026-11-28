package sessionagent

import "time"

const (
	backoffInitial     = time.Second
	backoffCap         = 60 * time.Second
	backoffStableAfter = 5 * time.Minute
	relaunchWindow     = 10 * time.Minute
	relaunchMaxInWindow = 10
)

// sessionBackoff tracks per-session relaunch timing and caps.
type sessionBackoff struct {
	nextDelay     time.Duration
	stableSince   time.Time
	windowStart   time.Time
	windowCount   int
	capped        bool // hit max relaunches; wait for next session event
	capLogged     bool
	pendingUntil  time.Time // zero = not waiting
	expectRestart bool      // next exit should emit session.restarted
}

func (b *sessionBackoff) onLaunched(now time.Time) {
	b.stableSince = now
	b.pendingUntil = time.Time{}
	b.expectRestart = true // first unexpected exit after launch is a restart
}

func (b *sessionBackoff) onStableTick(now time.Time) {
	if b.stableSince.IsZero() {
		return
	}
	if now.Sub(b.stableSince) >= backoffStableAfter {
		b.nextDelay = 0
		b.stableSince = now
	}
}

func (b *sessionBackoff) allowRelaunch(now time.Time) (delay time.Duration, ok bool) {
	if b.capped {
		return 0, false
	}
	if b.windowStart.IsZero() || now.Sub(b.windowStart) >= relaunchWindow {
		b.windowStart = now
		b.windowCount = 0
		b.capped = false
		b.capLogged = false
	}
	if b.windowCount >= relaunchMaxInWindow {
		b.capped = true
		return 0, false
	}
	b.windowCount++
	if b.nextDelay <= 0 {
		b.nextDelay = backoffInitial
	}
	delay = b.nextDelay
	next := b.nextDelay * 2
	if next > backoffCap {
		next = backoffCap
	}
	b.nextDelay = next
	b.pendingUntil = now.Add(delay)
	return delay, true
}

func (b *sessionBackoff) clearCapOnEvent() {
	if b.capped {
		b.capped = false
		b.capLogged = false
		b.windowStart = time.Time{}
		b.windowCount = 0
		b.nextDelay = 0
	}
}
