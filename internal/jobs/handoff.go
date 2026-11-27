package jobs

import "sync"

// PendingSink receives pending job ids from the heartbeat loop without blocking.
type PendingSink interface {
	Enqueue(ids []string)
}

// Handoff is a non-blocking, deduplicating queue of pending job ids.
// Heartbeat calls Enqueue; the Worker drains sequentially via Pop.
type Handoff struct {
	mu      sync.Mutex
	pending []string
	set     map[string]struct{}
	wake    chan struct{}
}

// NewHandoff returns an empty handoff queue.
func NewHandoff() *Handoff {
	return &Handoff{
		set:  make(map[string]struct{}),
		wake: make(chan struct{}, 1),
	}
}

// Enqueue adds ids that are not already queued. Never blocks the caller.
func (h *Handoff) Enqueue(ids []string) {
	if h == nil || len(ids) == 0 {
		return
	}
	h.mu.Lock()
	added := false
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := h.set[id]; ok {
			continue
		}
		h.set[id] = struct{}{}
		h.pending = append(h.pending, id)
		added = true
	}
	h.mu.Unlock()
	if !added {
		return
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Pop removes and returns the next queued id, or ("", false) when empty.
func (h *Handoff) Pop() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) == 0 {
		return "", false
	}
	id := h.pending[0]
	h.pending = h.pending[1:]
	delete(h.set, id)
	return id, true
}

// Wake returns a channel signaled when new ids are enqueued.
func (h *Handoff) Wake() <-chan struct{} {
	return h.wake
}

// Len returns the number of queued ids (tests).
func (h *Handoff) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pending)
}

var _ PendingSink = (*Handoff)(nil)
