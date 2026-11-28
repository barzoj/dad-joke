package ipc

import (
	"context"
	"io"
	"time"
)

// Handler receives post-handshake messages on the service side.
// Implementations must treat every payload as untrusted.
type Handler interface {
	// OnMessage is called for each decoded message after a successful hello.
	// Unknown types are not delivered (counted by the server). Ping is answered
	// with pong before OnMessage; hello is never delivered here.
	OnMessage(ctx context.Context, sessionID uint32, msg Message) error
}

// NopHandler discards messages.
type NopHandler struct{}

// OnMessage implements Handler.
func (NopHandler) OnMessage(context.Context, uint32, Message) error { return nil }

// ExpectedClient is the OS identity the service requires on a new connection.
type ExpectedClient struct {
	PID       uint32
	SessionID uint32
	UserSID   string // empty on non-Windows fakes
}

// ServerConfig configures the service-side listener for one session.
type ServerConfig struct {
	SessionID       uint32
	Expected        ExpectedClient
	IntervalSeconds int
	Handler         Handler
	Log             LogFunc
	Clock           func() time.Time // injectable; defaults to time.Now
	Stats           *Stats
	// HelloTimeout overrides the default 5s hello deadline (tests).
	HelloTimeout time.Duration
	// OnHandshake is called once after a successful hello/hello_ack exchange.
	OnHandshake func()
}

// ClientConfig configures the session-agent client.
type ClientConfig struct {
	SessionID    uint32
	PID          uint32
	AgentVersion string
	Protocol     int // 0 → ProtocolVersion
	Log          LogFunc
	Clock        func() time.Time
	Dial         DialFunc // optional; defaults to platform Dial
	// OnAck is called once per successful handshake with the server ack.
	OnAck func(ack Message)
	// AfterHello may send extra frames (e2e oversize injection); optional.
	AfterHello func(c *Conn) error
}

// DialFunc opens a client stream to the pipe for sessionID.
type DialFunc func(ctx context.Context, sessionID uint32) (io.ReadWriteCloser, error)

// ListenFunc creates a server-side acceptor for one session.
type ListenFunc func(ctx context.Context, sessionID uint32, userSID string) (Acceptor, error)

// Acceptor waits for one client connection and reports OS client identity.
type Acceptor interface {
	// Accept blocks until a client connects or ctx is done.
	Accept(ctx context.Context) (stream io.ReadWriteCloser, client ExpectedClient, err error)
	// Close abandons the listen handle (e.g. FILE_FLAG_FIRST_PIPE_INSTANCE pipe).
	Close() error
	// Name is the pipe / endpoint name (diagnostics).
	Name() string
}
