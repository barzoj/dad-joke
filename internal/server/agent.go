package server

import (
	"context"
	"errors"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

// AgentAPI implements agentapi.StrictServerInterface.
type AgentAPI struct {
	Store store.Store
	Clock func() time.Time
}

func (a *AgentAPI) now() time.Time {
	if a.Clock != nil {
		return a.Clock().UTC()
	}
	return time.Now().UTC()
}

func (a *AgentAPI) GetHealthz(context.Context, agentapi.GetHealthzRequestObject) (agentapi.GetHealthzResponseObject, error) {
	return agentapi.GetHealthz200JSONResponse{Status: agentapi.Ok}, nil
}

func (a *AgentAPI) PostHeartbeat(ctx context.Context, req agentapi.PostHeartbeatRequestObject) (agentapi.PostHeartbeatResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return agentapi.PostHeartbeat401JSONResponse{
			Code:      "unauthorized",
			Message:   "missing auth context",
			RequestId: uuid.NewString(),
		}, nil
	}
	if req.Body == nil {
		return agentapi.PostHeartbeat400JSONResponse{
			Code:      "bad_request",
			Message:   "missing body",
			RequestId: uuid.NewString(),
		}, nil
	}
	body := req.Body
	now := a.now()
	machineID := body.MachineId.String()

	existing, err := a.Store.Machines().Get(ctx, tenantID, machineID)
	desired := int32(0)
	if err == nil {
		desired = existing.DesiredPolicyVersion
	} else if !errors.Is(err, store.ErrNotFound) {
		return agentapi.PostHeartbeat500JSONResponse{
			Code:      "internal_error",
			Message:   "machine lookup failed",
			RequestId: uuid.NewString(),
		}, nil
	}

	mach := &store.Machine{
		ID:                   machineID,
		TenantID:             tenantID,
		Hostname:             body.Hostname,
		OSVersion:            body.OsVersion,
		AgentVersion:         body.AgentVersion,
		LastSeenAt:           now,
		AppliedPolicyVersion: body.AppliedPolicyVersion,
		DesiredPolicyVersion: desired,
	}
	if err := a.Store.Machines().Upsert(ctx, mach); err != nil {
		return agentapi.PostHeartbeat500JSONResponse{
			Code:      "internal_error",
			Message:   "machine upsert failed",
			RequestId: uuid.NewString(),
		}, nil
	}

	pending, err := a.Store.Jobs().ListPendingIDs(ctx, tenantID, machineID, now)
	if err != nil {
		return agentapi.PostHeartbeat500JSONResponse{
			Code:      "internal_error",
			Message:   "pending jobs failed",
			RequestId: uuid.NewString(),
		}, nil
	}
	if pending == nil {
		pending = []string{}
	}

	return agentapi.PostHeartbeat200JSONResponse{
		ServerTime:           now,
		DesiredPolicyVersion: desired,
		PendingJobIds:        pending,
	}, nil
}

var _ agentapi.StrictServerInterface = (*AgentAPI)(nil)
