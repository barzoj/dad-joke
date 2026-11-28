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

func TestHandshakeSuccess(t *testing.T) {
	srv, cli := pair()
	defer srv.Close()
	defer cli.Close()

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
	_ = cli.Close()
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
		_ = cc.WriteMessage(NewHello(ver, "dev", 1, 1))
		rej, err := cc.ReadMessage()
		if err != nil {
			t.Fatalf("ver=%d read: %v", ver, err)
		}
		if rej.Type != TypeReject || rej.Code != RejectVersionMismatch {
			t.Fatalf("ver=%d %+v", ver, rej)
		}
		_ = cli.Close()
		<-done
		if stats.VersionMismatch.Load() == 0 {
			t.Fatalf("ver=%d no mismatch counter", ver)
		}
		_ = srv.Close()
	}
}

func TestHandshakeHelloTimeout(t *testing.T) {
	srv, cli := pair()
	defer cli.Close()
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
		// either timeout or close-induced bad frame is acceptable
		t.Fatalf("expected hello timeout or bad frame counter")
	}
}

func TestDuplicateHello(t *testing.T) {
	srv, cli := pair()
	defer cli.Close()
	stats := &Stats{}
	done := make(chan error, 1)
	go func() { done <- ServeConn(context.Background(), srv, ServerConfig{SessionID: 1, Expected: ExpectedClient{PID: 1, SessionID: 1}, Stats: stats}) }()
	cc := NewConn(cli)
	_ = cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1))
	_, _ = cc.ReadMessage()
	_ = cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1))
	rej, err := cc.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if rej.Code != RejectDuplicateHello {
		t.Fatalf("%+v", rej)
	}
	<-done
	if stats.DuplicateHello.Load() != 1 {
		t.Fatalf("dup=%d", stats.DuplicateHello.Load())
	}
}

func TestMessageBeforeHello(t *testing.T) {
	srv, cli := pair()
	defer cli.Close()
	done := make(chan error, 1)
	go func() { done <- ServeConn(context.Background(), srv, ServerConfig{SessionID: 1}) }()
	cc := NewConn(cli)
	_ = cc.WriteMessage(NewPing())
	rej, _ := cc.ReadMessage()
	if rej.Type != TypeReject {
		t.Fatalf("%+v", rej)
	}
	<-done
}

func TestUnknownTypeIgnored(t *testing.T) {
	srv, cli := pair()
	defer cli.Close()
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
	_ = cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1))
	_, _ = cc.ReadMessage()
	_ = cc.WriteMessage(Message{Type: "not_a_real_type"})
	_ = cc.WriteMessage(NewMsg("fg.sample", []byte(`{}`)))
	time.Sleep(50 * time.Millisecond)
	_ = cli.Close()
	<-done
	if stats.UnknownType.Load() != 1 {
		t.Fatalf("unknown=%d", stats.UnknownType.Load())
	}
	if got.Load() != 1 {
		t.Fatalf("handler calls=%d", got.Load())
	}
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, uint32, Message) error

func (f HandlerFunc) OnMessage(ctx context.Context, sid uint32, m Message) error {
	return f(ctx, sid, m)
}

func TestPingPong(t *testing.T) {
	srv, cli := pair()
	defer cli.Close()
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), srv, ServerConfig{SessionID: 1, Expected: ExpectedClient{PID: 1, SessionID: 1}})
	}()
	cc := NewConn(cli)
	_ = cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1))
	_, _ = cc.ReadMessage()
	_ = cc.WriteMessage(NewPing())
	pong, err := cc.ReadMessage()
	if err != nil || pong.Type != TypePong {
		t.Fatalf("%+v %v", pong, err)
	}
	_ = cli.Close()
	<-done
}

func TestOversizeCloses(t *testing.T) {
	srv, cli := pair()
	stats := &Stats{}
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), srv, ServerConfig{SessionID: 1, Expected: ExpectedClient{PID: 1, SessionID: 1}, Stats: stats})
	}()
	cc := NewConn(cli)
	_ = cc.WriteMessage(NewHello(ProtocolVersion, "dev", 1, 1))
	_, _ = cc.ReadMessage()
	var hdr [4]byte
	hdr[0] = 0x00
	hdr[1] = 0x01
	hdr[2] = 0x00
	hdr[3] = 0x01 // 65537
	_, _ = cli.Write(hdr[:])
	err := <-done
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if stats.Oversize.Load() != 1 {
		t.Fatalf("oversize=%d", stats.Oversize.Load())
	}
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
	_ = stream.Close()
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
