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

func TestSupervisorIPCConnectedMismatchDropNoRespawn(t *testing.T) {
	hub := ipc.NewMemHub()
	events := make(chan service.Event, 4)
	clock := newMemClock(time.Now())
	launcher := newFakeLauncher(1)
	sessions := &fixedSessions{list: []Session{{ID: 1, UserName: "u", UserSID: "S-1-5-21-1"}}}
	var restarts atomic.Int32
	stats := &ipc.Stats{}
	sup := &Supervisor{
		Events:          events,
		Sessions:        sessions,
		Launcher:        launcher,
		Sink:            restartSinkFunc(func(uint32) error { restarts.Add(1); return nil }),
		Clock:           clock,
		Log:             NopLogger{},
		IntervalSeconds: 15,
		IPCListen:       hub.Listen,
		IPCStats:        stats,
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
	mismatchBefore := stats.VersionMismatch.Load()

	// Stop the good client and drop so we can present a version-mismatched peer
	// with the same verified PID (no respawn).
	clientCancel()
	sup.DropIPC(1)
	deadline = time.Now().Add(2 * time.Second)
	for sup.Connected(1) {
		if time.Now().After(deadline) {
			t.Fatal("still connected after drop")
		}
		time.Sleep(10 * time.Millisecond)
	}

	mismatchCtx, mismatchCancel := context.WithCancel(ctx)
	defer mismatchCancel()
	bad := ipc.NewClient(ipc.ClientConfig{
		SessionID: 1,
		PID:       child.PID(),
		Protocol:  99,
		Dial: func(ctx context.Context, sid uint32) (io.ReadWriteCloser, error) {
			return hub.DialAs(ctx, sid, ipc.ExpectedClient{PID: child.PID(), SessionID: sid})
		},
	})
	go func() { _ = bad.Run(mismatchCtx) }()

	deadline = time.Now().Add(3 * time.Second)
	for stats.VersionMismatch.Load() <= mismatchBefore {
		if time.Now().After(deadline) {
			t.Fatal("version mismatch not counted")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sup.Connected(1) {
		t.Fatal("connected after version mismatch")
	}
	if launcher.launchCount() != launchesBefore {
		t.Fatalf("mismatch triggered respawn: %d → %d", launchesBefore, launcher.launchCount())
	}
	mismatchCancel()

	// Good client reconnects without process restart.
	clientCtx2, clientCancel2 := context.WithCancel(ctx)
	defer clientCancel2()
	good := ipc.NewClient(ipc.ClientConfig{
		SessionID: 1,
		PID:       child.PID(),
		Protocol:  ipc.ProtocolVersion,
		Dial: func(ctx context.Context, sid uint32) (io.ReadWriteCloser, error) {
			return hub.DialAs(ctx, sid, ipc.ExpectedClient{PID: child.PID(), SessionID: sid})
		},
	})
	go func() { _ = good.Run(clientCtx2) }()
	deadline = time.Now().Add(3 * time.Second)
	for !sup.Connected(1) {
		if time.Now().After(deadline) {
			t.Fatal("did not reconnect after mismatch")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if launcher.launchCount() != launchesBefore {
		t.Fatal("reconnect respawned process")
	}

	// Process exit does respawn.
	clientCancel2()
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
