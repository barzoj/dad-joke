package jobs

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/service"
)

// stubUpdater counts ticks while a long job runs — proves Group isolation (AC #4/#11).
type stubUpdater struct {
	ticks atomic.Int32
}

func (s *stubUpdater) Run(ctx context.Context) error {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			s.ticks.Add(1)
		}
	}
}

func TestGroup_UpdaterKeepsRunningDuringLongJob(t *testing.T) {
	t.Parallel()
	cfg := testCfg(t)
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	exec := &fakeExec{started: started, block: block, result: ExecResult{ExitCode: 0}}
	api := &fakeAPI{}
	h := NewHandoff()
	w := &Worker{
		Handoff: h, Executor: exec, Audit: &AuditLog{DataDir: t.TempDir()}, Config: &cfg, Log: NopLogger{},
		NewClient: func(string) (API, error) { return api, nil },
	}
	upd := &stubUpdater{}
	g := service.Group{upd, w}

	cancel := startAgent(t, g.Run)

	h.Enqueue([]string{"88888888-8888-4888-8888-888888888888"})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not start")
	}
	time.Sleep(100 * time.Millisecond)
	if upd.ticks.Load() < 2 {
		t.Fatalf("updater ticks = %d while job blocked; want continued progress", upd.ticks.Load())
	}
	close(block)
	cancel()
}
