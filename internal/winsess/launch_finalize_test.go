package winsess

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFinalizeChild struct {
	term   atomic.Int32
	closed atomic.Int32
	exit   chan struct{}
}

func (c *fakeFinalizeChild) PID() uint32 { return 1 }

func (c *fakeFinalizeChild) Wait(ctx context.Context) error {
	select {
	case <-c.exit:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeFinalizeChild) Terminate() error {
	c.term.Add(1)
	select {
	case <-c.exit:
	default:
		close(c.exit)
	}
	return nil
}

func (c *fakeFinalizeChild) Close() error {
	c.closed.Add(1)
	return nil
}

func TestFinalizeLaunchPassesThroughOnOK(t *testing.T) {
	t.Parallel()
	ch := &fakeFinalizeChild{exit: make(chan struct{})}
	got, err := FinalizeLaunch(context.Background(), ch, nil)
	if err != nil || got != ch {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if ch.term.Load() != 0 || ch.closed.Load() != 0 {
		t.Fatal("must not touch child when ctx is live")
	}
}

func TestFinalizeLaunchPropagatesCreateError(t *testing.T) {
	t.Parallel()
	want := errors.New("create failed")
	got, err := FinalizeLaunch(context.Background(), nil, want)
	if !errors.Is(err, want) || got != nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestFinalizeLaunchReapsChildAfterCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := &fakeFinalizeChild{exit: make(chan struct{})}
	got, err := FinalizeLaunch(ctx, ch, nil)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got=%v err=%v", got, err)
	}
	deadline := time.Now().Add(time.Second)
	for ch.term.Load() == 0 || ch.closed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("term=%d closed=%d", ch.term.Load(), ch.closed.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
