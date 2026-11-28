package ipc

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
)

// MemHub is an in-memory pipe fabric for non-Windows tests.
type MemHub struct {
	mu      sync.Mutex
	pending map[uint32]chan memAccept
}

type memAccept struct {
	server net.Conn
	client ExpectedClient
}

// NewMemHub returns an empty hub.
func NewMemHub() *MemHub {
	return &MemHub{pending: make(map[uint32]chan memAccept)}
}

// Listen implements ListenFunc.
func (h *MemHub) Listen(_ context.Context, sessionID uint32, userSID string) (Acceptor, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.pending[sessionID]; ok {
		return nil, fmt.Errorf("ipc: mem pipe squat for session %d", sessionID)
	}
	ch := make(chan memAccept, 1)
	h.pending[sessionID] = ch
	return &memAcceptor{hub: h, sessionID: sessionID, userSID: userSID, ch: ch}, nil
}

// Dial implements DialFunc. client identity is taken from opts when set via DialAs.
func (h *MemHub) Dial(ctx context.Context, sessionID uint32) (io.ReadWriteCloser, error) {
	return h.DialAs(ctx, sessionID, ExpectedClient{})
}

// DialAs dials with an explicit claimed OS identity (for spoof tests).
func (h *MemHub) DialAs(ctx context.Context, sessionID uint32, client ExpectedClient) (io.ReadWriteCloser, error) {
	h.mu.Lock()
	ch := h.pending[sessionID]
	h.mu.Unlock()
	if ch == nil {
		return nil, fmt.Errorf("ipc: no listener for session %d", sessionID)
	}
	c1, c2 := net.Pipe()
	select {
	case <-ctx.Done():
		_ = c1.Close()
		_ = c2.Close()
		return nil, ctx.Err()
	case ch <- memAccept{server: c1, client: client}:
		return c2, nil
	}
}

type memAcceptor struct {
	hub       *MemHub
	sessionID uint32
	userSID   string
	ch        chan memAccept
	closed    bool
}

func (a *memAcceptor) Name() string { return PipeName(a.sessionID) }

func (a *memAcceptor) Close() error {
	a.hub.mu.Lock()
	defer a.hub.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.hub.pending[a.sessionID] == a.ch {
		delete(a.hub.pending, a.sessionID)
	}
	return nil
}

func (a *memAcceptor) Accept(ctx context.Context) (io.ReadWriteCloser, ExpectedClient, error) {
	select {
	case <-ctx.Done():
		return nil, ExpectedClient{}, ctx.Err()
	case acc, ok := <-a.ch:
		if !ok {
			return nil, ExpectedClient{}, fmt.Errorf("ipc: acceptor closed")
		}
		// Free the name immediately so a concurrent Dial gets "no listener"
		// while this connection is served (mirrors first-instance pipe semantics).
		_ = a.Close()
		client := acc.client
		if client.SessionID == 0 {
			client.SessionID = a.sessionID
		}
		client.UserSID = a.userSID
		return acc.server, client, nil
	}
}
