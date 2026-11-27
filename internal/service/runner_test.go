package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestStubReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	var s Stub
	if err := s.Run(context.Background()); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Run = %v, want ErrNotImplemented", err)
	}
}

type countingAgent struct {
	started atomic.Bool
	ended   atomic.Bool
}

func (a *countingAgent) Run(ctx context.Context) error {
	a.started.Store(true)
	<-ctx.Done()
	a.ended.Store(true)
	return ctx.Err()
}

func TestForegroundRunHonorsCancel(t *testing.T) {
	t.Parallel()
	agent := &countingAgent{}
	r := NewRunner(Options{Agent: agent, ForceForeground: true})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !agent.started.Load() {
		if time.Now().After(deadline) {
			t.Fatal("agent did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
	if !agent.ended.Load() {
		t.Fatal("agent did not end")
	}
}

func TestIdleAgent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- IdleAgent{}.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}
