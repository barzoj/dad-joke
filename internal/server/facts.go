package server

import (
	"context"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

// PostFacts is a temporary stub until T-19 implements facts ingest.
// Auth and body size limits are enforced by middleware / MaxBytesReader.
func (a *AgentAPI) PostFacts(context.Context, agentapi.PostFactsRequestObject) (agentapi.PostFactsResponseObject, error) {
	return nil, ErrNotImplemented
}

// GetMachineUsage is a temporary stub until T-19 implements usage aggregation.
func (c *ControlAPI) GetMachineUsage(context.Context, controlapi.GetMachineUsageRequestObject) (controlapi.GetMachineUsageResponseObject, error) {
	return nil, ErrNotImplemented
}
