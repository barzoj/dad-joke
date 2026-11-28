package server

import (
	"context"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/google/uuid"
)

// PostFacts is a temporary stub until T-19 implements facts ingest.
// Auth, body size limits, and contract validation run before the stub.
func (a *AgentAPI) PostFacts(_ context.Context, req agentapi.PostFactsRequestObject) (agentapi.PostFactsResponseObject, error) {
	if err := validatePostFactsRequest(req.Body); err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	return nil, ErrNotImplemented
}

// GetMachineUsage is a temporary stub until T-19 implements usage aggregation.
func (c *ControlAPI) GetMachineUsage(_ context.Context, req controlapi.GetMachineUsageRequestObject) (controlapi.GetMachineUsageResponseObject, error) {
	if err := validateUsageDate(req.Params.Date); err != nil {
		return controlapi.GetMachineUsage400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	return nil, ErrNotImplemented
}
