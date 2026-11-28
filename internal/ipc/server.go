package ipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ServeConn runs the service-side handshake and message loop on an already
// accepted and PID/session-verified stream. Returns when the connection ends.
func ServeConn(ctx context.Context, stream io.ReadWriteCloser, cfg ServerConfig) error {
	if stream == nil {
		return fmt.Errorf("ipc: nil stream")
	}
	defer stream.Close()

	if cfg.Handler == nil {
		cfg.Handler = NopHandler{}
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Stats == nil {
		cfg.Stats = &Stats{}
	}
	helloTO := cfg.HelloTimeout
	if helloTO <= 0 {
		helloTO = HelloTimeout
	}
	interval := cfg.IntervalSeconds
	if interval <= 0 {
		interval = 60
	}

	conn := NewConn(stream)
	rate := NewRateLimiter(time.Second)
	rate.Clock = cfg.Clock

	helloCtx, cancel := context.WithTimeout(ctx, helloTO)
	defer cancel()

	hello, err := readMessageContext(helloCtx, conn)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(helloCtx.Err(), context.DeadlineExceeded) {
			cfg.Stats.HelloTimeout.Add(1)
			cfg.Log("ipc: session %d hello timeout", cfg.SessionID)
		} else {
			cfg.Stats.BadFrame.Add(1)
			logRate(cfg.Log, rate, "ipc: session %d hello read: %v", cfg.SessionID, err)
		}
		return err
	}
	if hello.Type != TypeHello {
		cfg.Stats.BadFrame.Add(1)
		_ = conn.WriteMessage(NewReject(RejectBadHello, "expected hello"))
		return fmt.Errorf("ipc: expected hello, got %q", hello.Type)
	}
	if hello.Protocol != ProtocolVersion {
		cfg.Stats.VersionMismatch.Add(1)
		_ = conn.WriteMessage(NewReject(RejectVersionMismatch, fmt.Sprintf("want protocol %d", ProtocolVersion)))
		cfg.Log("ipc: session %d version mismatch (client=%d)", cfg.SessionID, hello.Protocol)
		return fmt.Errorf("ipc: version mismatch")
	}
	// Cross-check claimed identity against verified OS values (OS values win).
	if cfg.Expected.PID != 0 && hello.PID != 0 && hello.PID != cfg.Expected.PID {
		cfg.Stats.RefusedClient.Add(1)
		_ = conn.WriteMessage(NewReject(RejectBadHello, "pid mismatch"))
		return fmt.Errorf("ipc: hello pid mismatch")
	}
	if hello.SessionID != 0 && hello.SessionID != cfg.SessionID {
		cfg.Stats.RefusedClient.Add(1)
		_ = conn.WriteMessage(NewReject(RejectBadHello, "session_id mismatch"))
		return fmt.Errorf("ipc: hello session_id mismatch")
	}

	ack := NewHelloAck(ProtocolVersion, interval, cfg.Clock())
	if err := conn.WriteMessage(ack); err != nil {
		return err
	}
	cfg.Stats.HandshakeOK.Add(1)
	if cfg.OnHandshake != nil {
		cfg.OnHandshake()
	}

	helloSeen := true
	for {
		if err := ctx.Err(); err != nil {
			_ = conn.Close()
			return err
		}
		msg, err := readMessageContext(ctx, conn)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				cfg.Stats.Disconnects.Add(1)
				return err
			}
			if errors.Is(err, ErrFrameTooLarge) {
				cfg.Stats.Oversize.Add(1)
				logRate(cfg.Log, rate, "ipc: session %d oversize frame", cfg.SessionID)
				return err
			}
			cfg.Stats.BadFrame.Add(1)
			logRate(cfg.Log, rate, "ipc: session %d bad frame: %v", cfg.SessionID, err)
			return err
		}
		switch msg.Type {
		case TypeHello:
			if helloSeen {
				cfg.Stats.DuplicateHello.Add(1)
				_ = conn.WriteMessage(NewReject(RejectDuplicateHello, "duplicate hello"))
				return fmt.Errorf("ipc: duplicate hello")
			}
		case TypePing:
			if err := conn.WriteMessage(NewPong()); err != nil {
				return err
			}
		case TypePong, TypeHelloAck, TypeReject:
			// Ignore unexpected server-role messages from client.
		case TypeMsg:
			if err := cfg.Handler.OnMessage(ctx, cfg.SessionID, msg); err != nil {
				logRate(cfg.Log, rate, "ipc: session %d handler: %v", cfg.SessionID, err)
			}
		default:
			cfg.Stats.UnknownType.Add(1)
			// Ignore unknown types (never crash).
		}
	}
}

func readMessageContext(ctx context.Context, conn *Conn) (Message, error) {
	type result struct {
		m   Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := conn.ReadMessage()
		ch <- result{m, err}
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		return Message{}, ctx.Err()
	case r := <-ch:
		return r.m, r.err
	}
}

func logRate(log LogFunc, rate *RateLimiter, format string, args ...any) {
	ok, suppressed := rate.Allow()
	if !ok {
		return
	}
	if suppressed > 0 {
		log(format+" (suppressed %d similar)", append(args, suppressed)...)
		return
	}
	log(format, args...)
}

// VerifyClient reports whether the OS-reported client matches expected.
func VerifyClient(got, expected ExpectedClient) bool {
	if expected.PID != 0 && got.PID != expected.PID {
		return false
	}
	if expected.SessionID != 0 && got.SessionID != expected.SessionID {
		return false
	}
	return true
}
