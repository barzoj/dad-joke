// Package sensor defines the agent sampling contract: pure data producers that
// emit facts for the durable queue. Sensors must not talk to the network or
// apply policy.
package sensor

import (
	"context"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
)

// Fact is one sample/marker produced by a Sensor (AgentAPI FactItem shape).
type Fact = agentapi.FactItem

// Sensor collects facts at a point in time. Implementations are pure producers.
type Sensor interface {
	// Collect returns zero or more facts for now. An empty slice with a nil
	// error is normal (e.g. no interactive sessions). Errors are logged by the
	// sampler; the loop continues.
	Collect(ctx context.Context, now time.Time) ([]Fact, error)
}

// Registry is a plain slice of sensors (YAGNI — no plugin framework).
type Registry []Sensor

// CollectAll runs every sensor and concatenates facts. Per-sensor errors are
// returned via onErr (may be nil); collection continues.
func (r Registry) CollectAll(ctx context.Context, now time.Time, onErr func(Sensor, error)) []Fact {
	var out []Fact
	for _, s := range r {
		if s == nil {
			continue
		}
		facts, err := s.Collect(ctx, now)
		if err != nil {
			if onErr != nil {
				onErr(s, err)
			}
			continue
		}
		out = append(out, facts...)
	}
	return out
}
