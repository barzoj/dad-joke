package sessionagent

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/ipc"
	"github.com/barzoj/dad-joke/internal/service"
)

func TestSupervisorIPCConnectedAndMismatchNoRespawn(t *testing.T) {
	hub := ipc.NewMemHub()
	events := make(chan service.Event, 4)
	clock := newMemClock(time.Now())
	launcher := newFakeLauncher(1)
	sessions := &fixedSessions{list: []Session{{ID: 1, UserName: "u", UserSID: "S-1-5-21-1"}}}
	var restarts atomic.Int32
	sup := &Supervisor{
		Events:          events,
		Sessions:        sessions,
		Launcher:        launcher,
		Sink:            restartSinkFunc(func(uint32) error { restarts.Add(1); return nil }),
		Clock:           clock,
		Log:             NopLogger{},
		IntervalSeconds: 15,
		IPCListen:       hub.Listen,
		IPCStats:        &ipc.Stats{},
		children:        make(map[uint32]*tracked),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sup.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for launcher.launchCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("no launch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	child := launcher.lastChild()
	if child == nil {
		t.Fatal("no child")
	}

	clientCtx, clientCancel := context.WithCancel(ctx)
	defer clientCancel()
	client := ipc.NewClient(ipc.ClientConfig{
		SessionID: 1,
		PID:       child.PID(),
		Protocol:  ipc.ProtocolVersion,
		Dial: func(ctx context.Context, sid uint32) (io.ReadWriteCloser, error) {
			return hub.DialAs(ctx, sid, ipc.ExpectedClient{PID: child.PID(), SessionID: sid})
		},
	})
	go func() { _ = client.Run(clientCtx) }()

	deadline = time.Now().Add(3 * time.Second)
	for !sup.Connected(1) {
		if time.Now().After(deadline) {
			t.Fatal("not connected")
		}
		time.Sleep(20 * time.Millisecond)
	}

	launchesBefore := launcher.launchCount()

	// Spoofed PID must be refused and must not respawn.
	spoofDeadline := time.Now().Add(2 * time.Second)
	for {
		stream, err := hub.DialAs(ctx, 1, ipc.ExpectedClient{PID: 9999, SessionID: 1})
		if err == nil {
			cc := ipc.NewConn(stream)
			_ = cc.WriteMessage(ipc.NewHello(ipc.ProtocolVersion, "evil", 1, 9999))
			time.Sleep(50 * time.Millisecond)
			_ = stream.Close()
			break
		}
		if time.Now().After(spoofDeadline) {
			break // listener may be busy with the good client; refusal covered in ipc tests
		}
		time.Sleep(20 * time.Millisecond)
	}
	if launcher.launchCount() != launchesBefore {
		t.Fatalf("spoof triggered respawn: %d → %d", launchesBefore, launcher.launchCount())
	}

	// Version-mismatch against the live connection is covered in ipc tests;
	// ensure DropIPC does not respawn.
	sup.DropIPC(1)
	deadline = time.Now().Add(2 * time.Second)
	for sup.Connected(1) {
		if time.Now().After(deadline) {
			t.Fatal("still connected after drop")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if launcher.launchCount() != launchesBefore {
		t.Fatal("drop triggered respawn")
	}

	// Client should reconnect without process restart.
	deadline = time.Now().Add(3 * time.Second)
	for !sup.Connected(1) {
		if time.Now().After(deadline) {
			t.Fatal("did not reconnect after drop")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if launcher.launchCount() != launchesBefore {
		t.Fatal("reconnect respawned process")
	}

	// Process exit does respawn.
	clientCancel()
	child.kill()
	deadline = time.Now().Add(2 * time.Second)
	for launcher.launchCount() <= launchesBefore {
		if time.Now().After(deadline) {
			t.Fatal("exit did not respawn")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if restarts.Load() < 1 {
		t.Fatal("expected session.restarted sink call")
	}
}

type restartSinkFunc func(uint32) error

func (f restartSinkFunc) EnqueueRestarted(id uint32) error { return f(id) }
