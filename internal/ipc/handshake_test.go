package ipc

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pair() (server, client net.Conn) {
	a, b := net.Pipe()
	return a, b
}

func closeQuiet(c io.Closer) {
	_ = c.Close()
}

func TestHandshakeSuccess(t *testing.T) {
	srv, cli := pair()
	defer closeQuiet(srv)
	defer closeQuiet(cli)

	stats := &Stats{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := ServeConn(context.Background(), srv, ServerConfig{
			SessionID:       1,
			Expected:        ExpectedClient{PID: 100, SessionID: 1},
			IntervalSeconds: 30,
			Stats:           stats,
		})
		if err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("serve: %v", err)
		}
	}()

	cc := NewConn(cli)
	if err := cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 100)); err != nil {
		t.Fatal(err)
	}
	ack, err := cc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if ack.Type != TypeHelloAck || ack.Protocol != ProtocolVersion || ack.IntervalSeconds != 30 {
		t.Fatalf("%+v", ack)
	}
	closeQuiet(cli)
	wg.Wait()
	if stats.HandshakeOK.Load() != 1 {
		t.Fatalf("handshake_ok=%d", stats.HandshakeOK.Load())
	}
}

func TestHandshakeVersionMismatch(t *testing.T) {
	for _, ver := range []int{0, 2, 99} {
		srv, cli := pair()
		stats := &Stats{}
		done := make(chan error, 1)
		go func() {
			done <- ServeConn(context.Background(), srv, ServerConfig{
				SessionID: 1,
				Expected:  ExpectedClient{PID: 1, SessionID: 1},
				Stats:     stats,
			})
		}()
		cc := NewConn(cli)
		if err := cc.WriteMessage(NewHello(ver, "dev", 1, 1)); err != nil {
			t.Fatalf("ver=%d write: %v", ver, err)
		}
		rej, err := cc.ReadMessage()
		if err != nil {
			t.Fatalf("ver=%d read: %v", ver, err)
		}
		if rej.Type != TypeReject || rej.Code != RejectVersionMismatch {
			t.Fatalf("ver=%d %+v", ver, rej)
		}
		closeQuiet(cli)
		if err := <-done; err == nil {
			t.Fatalf("ver=%d: expected ServeConn error on version mismatch", ver)
		}
		if stats.VersionMismatch.Load() == 0 {
			t.Fatalf("ver=%d no mismatch counter", ver)
		}
		closeQuiet(srv)
	}
}

func TestHandshakeHelloTimeout(t *testing.T) {
	srv, cli := pair()
	defer closeQuiet(cli)
	stats := &Stats{}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := ServeConn(ctx, srv, ServerConfig{
		SessionID:    1,
		HelloTimeout: 50 * time.Millisecond,
		Stats:        stats,
	})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if stats.HelloTimeout.Load() == 0 && stats.BadFrame.Load() == 0 {
		t.Fatalf("expected hello timeout or bad frame counter")
	}
	closeQuiet(srv)
}

func TestDuplicateHello(t *testing.T) {
	srv, cli := pair()
	defer closeQuiet(cli)
	stats := &Stats{}
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), srv, ServerConfig{
			SessionID: 1,
			Expected:  ExpectedClient{PID: 1, SessionID: 1},
			Stats:     stats,
		})
	}()
	cc := NewConn(cli)
	if err := cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1)); err != nil {
		t.Fatal(err)
	}
	rej, err := cc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if rej.Code != RejectDuplicateHello {
		t.Fatalf("%+v", rej)
	}
	if err := <-done; err == nil {
		t.Fatal("expected ServeConn error on duplicate hello")
	}
	if stats.DuplicateHello.Load() != 1 {
		t.Fatalf("dup=%d", stats.DuplicateHello.Load())
	}
	closeQuiet(srv)
}

func TestMessageBeforeHello(t *testing.T) {
	srv, cli := pair()
	defer closeQuiet(cli)
	done := make(chan error, 1)
	go func() { done <- ServeConn(context.Background(), srv, ServerConfig{SessionID: 1}) }()
	cc := NewConn(cli)
	if err := cc.WriteMessage(NewPing()); err != nil {
		t.Fatal(err)
	}
	rej, err := cc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if rej.Type != TypeReject {
		t.Fatalf("%+v", rej)
	}
	if err := <-done; err == nil {
		t.Fatal("expected ServeConn error when message precedes hello")
	}
	closeQuiet(srv)
}

func TestUnknownTypeIgnored(t *testing.T) {
	srv, cli := pair()
	defer closeQuiet(cli)
	stats := &Stats{}
	var got atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), srv, ServerConfig{
			SessionID: 1,
			Expected:  ExpectedClient{PID: 1, SessionID: 1},
			Stats:     stats,
			Handler: HandlerFunc(func(context.Context, uint32, Message) error {
				got.Add(1)
				return nil
			}),
		})
	}()
	cc := NewConn(cli)
	if err := cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := cc.WriteMessage(Message{Type: "not_a_real_type"}); err != nil {
		t.Fatal(err)
	}
	if err := cc.WriteMessage(NewMsg("fg.sample", []byte(`{}`))); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	closeQuiet(cli)
	if err := <-done; err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("serve after unknown type: %v", err)
	}
	if stats.UnknownType.Load() != 1 {
		t.Fatalf("unknown=%d", stats.UnknownType.Load())
	}
	if got.Load() != 1 {
		t.Fatalf("handler calls=%d", got.Load())
	}
	closeQuiet(srv)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, uint32, Message) error

func (f HandlerFunc) OnMessage(ctx context.Context, sid uint32, m Message) error {
	return f(ctx, sid, m)
}

func TestPingPong(t *testing.T) {
	srv, cli := pair()
	defer closeQuiet(cli)
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), srv, ServerConfig{
			SessionID: 1,
			Expected:  ExpectedClient{PID: 1, SessionID: 1},
		})
	}()
	cc := NewConn(cli)
	if err := cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := cc.WriteMessage(NewPing()); err != nil {
		t.Fatal(err)
	}
	pong, err := cc.ReadMessage()
	if err != nil || pong.Type != TypePong {
		t.Fatalf("%+v %v", pong, err)
	}
	closeQuiet(cli)
	if err := <-done; err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("serve after ping/pong: %v", err)
	}
	closeQuiet(srv)
}

func TestOversizeCloses(t *testing.T) {
	srv, cli := pair()
	stats := &Stats{}
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), srv, ServerConfig{
			SessionID: 1,
			Expected:  ExpectedClient{PID: 1, SessionID: 1},
			Stats:     stats,
		})
	}()
	cc := NewConn(cli)
	if err := cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cc.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	var hdr [4]byte
	hdr[0] = 0x00
	hdr[1] = 0x01
	hdr[2] = 0x00
	hdr[3] = 0x01 // 65537
	if _, err := cli.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}
	err := <-done
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if stats.Oversize.Load() != 1 {
		t.Fatalf("oversize=%d", stats.Oversize.Load())
	}
	closeQuiet(cli)
	closeQuiet(srv)
}

func TestClientReconnectAfterDrop(t *testing.T) {
	hub := NewMemHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := &SessionServer{
		SessionID:       5,
		UserSID:         "S-1-5-21-1",
		ExpectedPID:     42,
		IntervalSeconds: 15,
		Listen:          hub.Listen,
		Stats:           &Stats{},
	}
	go func() { _ = srv.Run(ctx) }()

	client := NewClient(ClientConfig{
		SessionID: 5,
		PID:       42,
		Protocol:  ProtocolVersion,
		Dial: func(ctx context.Context, sid uint32) (io.ReadWriteCloser, error) {
			return hub.DialAs(ctx, sid, ExpectedClient{PID: 42, SessionID: 5})
		},
	})
	go func() { _ = client.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !srv.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("not connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	handshakes := srv.Stats.HandshakeOK.Load()
	srv.Drop()
	deadline = time.Now().Add(5 * time.Second)
	for srv.Stats.HandshakeOK.Load() <= handshakes || !srv.Connected() {
		if time.Now().After(deadline) {
			t.Fatalf("handshakes=%d→%d connected=%v after reconnect",
				handshakes, srv.Stats.HandshakeOK.Load(), srv.Connected())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSpoofedPIDRefused(t *testing.T) {
	hub := NewMemHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stats := &Stats{}
	srv := &SessionServer{
		SessionID:   9,
		UserSID:     "S-1-5-21-9",
		ExpectedPID: 100,
		Listen:      hub.Listen,
		Stats:       stats,
	}
	go func() { _ = srv.Run(ctx) }()

	var stream io.ReadWriteCloser
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		stream, err = hub.DialAs(ctx, 9, ExpectedClient{PID: 999, SessionID: 9})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// PID/session check runs before any frame is read; the stream may already
	// be closed by the time we try to write.
	cc := NewConn(stream)
	_ = cc.WriteMessage(NewHello(ProtocolVersion, "evil", 9, 999))
	deadline = time.Now().Add(2 * time.Second)
	for stats.RefusedClient.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("expected refused counter")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Connected() {
		t.Fatal("spoofed client marked connected")
	}
	closeQuiet(stream)
}

func TestSquatterListenRejected(t *testing.T) {
	hub := NewMemHub()
	ctx := context.Background()
	stats := &Stats{}

	first, err := hub.Listen(ctx, 11, "S-1-5-21-11")
	if err != nil {
		t.Fatal(err)
	}
	defer closeQuiet(first)

	srv := &SessionServer{
		SessionID:   11,
		UserSID:     "S-1-5-21-11",
		ExpectedPID: 55,
		Listen:      hub.Listen,
		Stats:       stats,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(runCtx) }()

	deadline := time.Now().Add(2 * time.Second)
	for stats.Squatter.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("expected squatter counter while first listen is held")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if srv.Connected() {
		t.Fatal("Connected() true while pipe name is squatted")
	}
	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionServer.Run after cancel: %v", err)
	}
}

func TestVerifyClient(t *testing.T) {
	if !VerifyClient(ExpectedClient{PID: 1, SessionID: 2}, ExpectedClient{PID: 1, SessionID: 2}) {
		t.Fatal("equal")
	}
	if VerifyClient(ExpectedClient{PID: 9, SessionID: 2}, ExpectedClient{PID: 1, SessionID: 2}) {
		t.Fatal("pid")
	}
	if VerifyClient(ExpectedClient{PID: 1, SessionID: 9}, ExpectedClient{PID: 1, SessionID: 2}) {
		t.Fatal("sess")
	}
}
