package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type trackingAgent struct {
	started atomic.Bool
	ended   atomic.Bool
	err     error
}

func (a *trackingAgent) Run(ctx context.Context) error {
	a.started.Store(true)
	<-ctx.Done()
	a.ended.Store(true)
	if a.err != nil {
		return a.err
	}
	return ctx.Err()
}

func TestGroupRunsIndependentlyAndCancels(t *testing.T) {
	t.Parallel()
	a1 := &trackingAgent{}
	a2 := &trackingAgent{}
	g := Group{a1, nil, a2}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !a1.started.Load() || !a2.started.Load() {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("agents did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if !a1.ended.Load() || !a2.ended.Load() {
		t.Fatal("agents did not end")
	}
}

type failAgent struct {
	err error
}

func (a failAgent) Run(context.Context) error { return a.err }

func TestGroupPropagatesHardFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("hard failure")
	idle := &trackingAgent{}
	g := Group{failAgent{err: want}, idle}

	done := make(chan error, 1)
	go func() { done <- g.Run(context.Background()) }()

	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestGroupEmptyWaitsForCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Group{}.Run(ctx) }()
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

func TestNewDefaultRunner(t *testing.T) {
	t.Parallel()
	r := New()
	if r == nil {
		t.Fatal("nil runner")
	}
}
