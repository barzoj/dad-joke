package service

import (
	"context"
	"errors"
	"sync"
)

// Group runs multiple Agents concurrently until ctx is cancelled.
// Each agent is independent: one agent's error is logged by that agent and
// must not stop siblings (agents should only return on cancel). If an agent
// returns a non-cancel error, Group still waits for the others after canceling.
type Group []Agent

// Run starts every agent and returns when all have exited. The first non-nil
// non-cancel error is returned (if any); otherwise ctx.Err() / nil.
func (g Group) Run(ctx context.Context) error {
	if len(g) == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for _, a := range g {
		if a == nil {
			continue
		}
		wg.Add(1)
		go func(agent Agent) {
			defer wg.Done()
			err := agent.Run(ctx)
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			cancel() // stop siblings if one fails hard
			mu.Lock()
			if first == nil {
				first = err
			}
			mu.Unlock()
		}(a)
	}
	wg.Wait()
	if first != nil {
		return first
	}
	if err := ctx.Err(); errors.Is(err, context.Canceled) {
		return err
	}
	return first
}
