package ipc

import (
	"io"
	"sync"
)

// Conn is a framed bidirectional IPC connection over an underlying stream.
type Conn struct {
	rwc io.ReadWriteCloser
	mu  sync.Mutex // serializes writes
}

// NewConn wraps rwc with framing.
func NewConn(rwc io.ReadWriteCloser) *Conn {
	return &Conn{rwc: rwc}
}

// ReadMessage reads one framed JSON message.
func (c *Conn) ReadMessage() (Message, error) {
	payload, err := ReadFrame(c.rwc)
	if err != nil {
		return Message{}, err
	}
	return DecodeMessage(payload)
}

// WriteMessage writes one framed JSON message.
func (c *Conn) WriteMessage(m Message) error {
	payload, err := EncodeMessage(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.rwc, payload)
}

// Close closes the underlying stream.
func (c *Conn) Close() error {
	if c == nil || c.rwc == nil {
		return nil
	}
	return c.rwc.Close()
}

// Raw returns the underlying stream (tests / transport helpers).
func (c *Conn) Raw() io.ReadWriteCloser { return c.rwc }
