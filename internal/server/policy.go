package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

func (c *ControlAPI) GetMachinePolicy(ctx context.Context, req controlapi.GetMachinePolicyRequestObject) (controlapi.GetMachinePolicyResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.GetMachinePolicy401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	machineID := req.MachineId.String()
	pol, err := c.Store.Policies().Get(ctx, tenantID, machineID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.GetMachinePolicy404JSONResponse{Code: "not_found", Message: "policy not found", RequestId: uuid.NewString()}, nil
		}
		return controlapi.GetMachinePolicy500JSONResponse{Code: "internal_error", Message: "policy lookup failed", RequestId: uuid.NewString()}, nil
	}
	out, err := toControlMachinePolicy(pol)
	if err != nil {
		return controlapi.GetMachinePolicy500JSONResponse{Code: "internal_error", Message: "policy decode failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.GetMachinePolicy200JSONResponse(out), nil
}

func (c *ControlAPI) PutMachinePolicy(ctx context.Context, req controlapi.PutMachinePolicyRequestObject) (controlapi.PutMachinePolicyResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.PutMachinePolicy401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	if req.Body == nil {
		return controlapi.PutMachinePolicy400JSONResponse{Code: "bad_request", Message: "missing body", RequestId: uuid.NewString()}, nil
	}
	machineID := req.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return controlapi.PutMachinePolicy400JSONResponse{Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString()}, nil
	}
	if req.Body.Document == nil {
		return controlapi.PutMachinePolicy400JSONResponse{Code: "bad_request", Message: "document is required", RequestId: uuid.NewString()}, nil
	}
	raw, err := json.Marshal(req.Body.Document)
	if err != nil {
		return controlapi.PutMachinePolicy400JSONResponse{Code: "bad_request", Message: "invalid document", RequestId: uuid.NewString()}, nil
	}

	pol := &store.Policy{
		TenantID:  tenantID,
		MachineID: machineID,
		Document:  raw,
		UpdatedAt: c.now(),
	}
	if err := c.Store.Policies().Put(ctx, pol, req.Body.ExpectedVersion); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return controlapi.PutMachinePolicy409JSONResponse{Code: "conflict", Message: "expected_version mismatch", RequestId: uuid.NewString()}, nil
		}
		if errors.Is(err, store.ErrInvalid) {
			return controlapi.PutMachinePolicy400JSONResponse{Code: "bad_request", Message: "invalid policy", RequestId: uuid.NewString()}, nil
		}
		return controlapi.PutMachinePolicy500JSONResponse{Code: "internal_error", Message: "policy write failed", RequestId: uuid.NewString()}, nil
	}

	if err := c.Audit.PolicyCreate(ctx, tenantID, actorID, machineID, pol.Version); err != nil {
		return controlapi.PutMachinePolicy500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}

	// Best-effort: keep machines.desired_policy_version aligned when enrolled.
	c.syncMachineDesiredVersion(ctx, tenantID, machineID, pol.Version)

	out, err := toControlMachinePolicy(pol)
	if err != nil {
		return controlapi.PutMachinePolicy500JSONResponse{Code: "internal_error", Message: "policy decode failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.PutMachinePolicy200JSONResponse(out), nil
}

func (c *ControlAPI) syncMachineDesiredVersion(ctx context.Context, tenantID, machineID string, version int32) {
	m, err := c.Store.Machines().Get(ctx, tenantID, machineID)
	if err != nil {
		return
	}
	m.DesiredPolicyVersion = version
	_ = c.Store.Machines().Upsert(ctx, m)
}

func (a *AgentAPI) GetPolicy(ctx context.Context, req agentapi.GetPolicyRequestObject) (agentapi.GetPolicyResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return agentapi.GetPolicy401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	machineID := req.Params.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return agentapi.GetPolicy400JSONResponse{Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString()}, nil
	}
	pol, err := a.Store.Policies().Get(ctx, tenantID, machineID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return agentapi.GetPolicy404JSONResponse{Code: "not_found", Message: "policy not found", RequestId: uuid.NewString()}, nil
		}
		return agentapi.GetPolicy500JSONResponse{Code: "internal_error", Message: "policy lookup failed", RequestId: uuid.NewString()}, nil
	}
	out, err := toAgentMachinePolicy(pol)
	if err != nil {
		return agentapi.GetPolicy500JSONResponse{Code: "internal_error", Message: "policy decode failed", RequestId: uuid.NewString()}, nil
	}
	return agentapi.GetPolicy200JSONResponse(out), nil
}

func toControlMachinePolicy(pol *store.Policy) (controlapi.MachinePolicy, error) {
	doc, err := decodePolicyDocument(pol.Document)
	if err != nil {
		return controlapi.MachinePolicy{}, err
	}
	return controlapi.MachinePolicy{
		Version:   pol.Version,
		Document:  doc,
		UpdatedAt: pol.UpdatedAt,
	}, nil
}

func toAgentMachinePolicy(pol *store.Policy) (agentapi.MachinePolicy, error) {
	doc, err := decodePolicyDocument(pol.Document)
	if err != nil {
		return agentapi.MachinePolicy{}, err
	}
	return agentapi.MachinePolicy{
		Version:   pol.Version,
		Document:  doc,
		UpdatedAt: pol.UpdatedAt,
	}, nil
}

func decodePolicyDocument(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode policy document: %w", err)
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	return doc, nil
}
