package jobs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAwaitWaitGroup_FinishesCleanly(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
	}()
	var forced atomic.Bool
	ok := awaitWaitGroup(&wg, time.Second, func() { forced.Store(true) })
	if !ok {
		t.Fatal("expected clean finish")
	}
	if forced.Load() {
		t.Fatal("onTimeout must not run when wg finishes in time")
	}
}

func TestAwaitWaitGroup_ForcesOnTimeout(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	release := make(chan struct{})
	go func() {
		defer wg.Done()
		<-release
	}()
	var forced atomic.Bool
	start := time.Now()
	ok := awaitWaitGroup(&wg, 30*time.Millisecond, func() {
		forced.Store(true)
		close(release)
	})
	elapsed := time.Since(start)
	if ok {
		t.Fatal("expected timeout path")
	}
	if !forced.Load() {
		t.Fatal("onTimeout was not called")
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("returned too quickly: %s", elapsed)
	}
}

func TestAwaitWaitGroup_AbandonsIfStillStuck(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	// Never releases — models Close failing to unblock a pipe reader.
	var forced atomic.Bool
	start := time.Now()
	done := make(chan bool, 1)
	go func() {
		done <- awaitWaitGroup(&wg, 25*time.Millisecond, func() { forced.Store(true) })
	}()
	select {
	case ok := <-done:
		elapsed := time.Since(start)
		if ok {
			t.Fatal("expected timeout path")
		}
		if !forced.Load() {
			t.Fatal("onTimeout was not called")
		}
		// First grace + second grace; must not hang forever.
		if elapsed > 500*time.Millisecond {
			t.Fatalf("await hung too long: %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("awaitWaitGroup blocked forever despite dual grace period")
	}
}
