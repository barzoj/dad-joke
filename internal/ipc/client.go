package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// errDropAfterHandshake marks a clean post-handshake disconnect so backoff resets.
var errDropAfterHandshake = errors.New("ipc: drop after handshake")

// Client is the session-agent IPC client: dial, hello, reconnect with backoff.
type Client struct {
	cfg ClientConfig
}

// NewClient returns a session-agent client.
func NewClient(cfg ClientConfig) *Client {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Protocol == 0 {
		cfg.Protocol = ProtocolVersion
	}
	if cfg.Dial == nil {
		cfg.Dial = DefaultDial
	}
	if cfg.AgentVersion == "" {
		cfg.AgentVersion = "unknown"
	}
	if cfg.PID == 0 {
		cfg.PID = pidU32(os.Getpid())
	}
	return &Client{cfg: cfg}
}

// pidU32 converts an OS pid to uint32 with an explicit bounds check.
func pidU32(pid int) uint32 {
	if pid < 0 {
		return 0
	}
	const maxUint32 = 1<<32 - 1
	if pid > maxUint32 {
		return maxUint32
	}
	return uint32(pid)
}

// Run dials and maintains a connection until ctx is cancelled.
func (c *Client) Run(ctx context.Context) error {
	var bo ReconnectBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := c.session(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			return ctx.Err()
		}
		if errors.Is(err, errDropAfterHandshake) {
			bo.Reset()
		}
		c.cfg.Log("ipc: client session ended: %v", err)
		delay := bo.Next()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) session(ctx context.Context) error {
	stream, err := c.cfg.Dial(ctx, c.cfg.SessionID)
	if err != nil {
		return err
	}
	defer stream.Close()

	conn := NewConn(stream)
	hello := NewHello(c.cfg.Protocol, c.cfg.AgentVersion, c.cfg.SessionID, c.cfg.PID)
	if err := conn.WriteMessage(hello); err != nil {
		return err
	}
	if c.cfg.AfterHello != nil {
		if err := c.cfg.AfterHello(conn); err != nil {
			return err
		}
	}
	ack, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	switch ack.Type {
	case TypeReject:
		return fmt.Errorf("ipc: rejected: %s (%s)", ack.Code, ack.Message)
	case TypeHelloAck:
		if c.cfg.OnAck != nil {
			c.cfg.OnAck(ack)
		}
	default:
		return fmt.Errorf("ipc: expected hello_ack, got %q", ack.Type)
	}

	for {
		msg, err := readMessageContext(ctx, conn)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return context.Canceled
			}
			return fmt.Errorf("%w: %v", errDropAfterHandshake, err)
		}
		switch msg.Type {
		case TypePing:
			if err := conn.WriteMessage(NewPong()); err != nil {
				return fmt.Errorf("%w: %v", errDropAfterHandshake, err)
			}
		default:
			// Session agent ignores unknown server messages.
		}
	}
}

// DefaultDial is set by platform files.
var DefaultDial DialFunc = dialUnsupported

func dialUnsupported(context.Context, uint32) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("ipc: dial not available on this platform")
}
