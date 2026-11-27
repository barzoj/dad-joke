package server

import (
	"context"
	"errors"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

func (c *ControlAPI) CreateMachineJob(ctx context.Context, req controlapi.CreateMachineJobRequestObject) (controlapi.CreateMachineJobResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.CreateMachineJob401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	if req.Body == nil {
		return controlapi.CreateMachineJob400JSONResponse{Code: "bad_request", Message: "missing body", RequestId: uuid.NewString()}, nil
	}
	machineID := req.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return controlapi.CreateMachineJob400JSONResponse{Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString()}, nil
	}
	if req.Body.ExpiresAt.IsZero() {
		return controlapi.CreateMachineJob400JSONResponse{Code: "bad_request", Message: "expires_at is required", RequestId: uuid.NewString()}, nil
	}
	if oversizedJobIO(req.Body.Payload) {
		return controlapi.CreateMachineJob413JSONResponse{Code: "payload_too_large", Message: "payload exceeds 64 KiB limit", RequestId: uuid.NewString()}, nil
	}

	now := c.now()
	job := &store.Job{
		TenantID:  tenantID,
		MachineID: machineID,
		Status:    store.JobPending,
		Payload:   req.Body.Payload,
		CreatedAt: now,
		ExpiresAt: req.Body.ExpiresAt.UTC(),
	}
	if err := c.Store.Jobs().Create(ctx, job); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return controlapi.CreateMachineJob400JSONResponse{Code: "bad_request", Message: "invalid job", RequestId: uuid.NewString()}, nil
		}
		return controlapi.CreateMachineJob500JSONResponse{Code: "internal_error", Message: "job create failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Audit.JobCreate(ctx, tenantID, actorID, job.ID, machineID); err != nil {
		return controlapi.CreateMachineJob500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	out, err := toControlJob(job)
	if err != nil {
		return controlapi.CreateMachineJob500JSONResponse{Code: "internal_error", Message: "job encode failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.CreateMachineJob201JSONResponse(out), nil
}

func (c *ControlAPI) ListMachineJobs(ctx context.Context, req controlapi.ListMachineJobsRequestObject) (controlapi.ListMachineJobsResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.ListMachineJobs401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	machineID := req.MachineId.String()
	jobs, err := c.Store.Jobs().ListByMachine(ctx, tenantID, machineID)
	if err != nil {
		return controlapi.ListMachineJobs500JSONResponse{Code: "internal_error", Message: "list jobs failed", RequestId: uuid.NewString()}, nil
	}
	out := make(controlapi.ListMachineJobs200JSONResponse, 0, len(jobs))
	for i := range jobs {
		j, err := toControlJob(&jobs[i])
		if err != nil {
			return controlapi.ListMachineJobs500JSONResponse{Code: "internal_error", Message: "job encode failed", RequestId: uuid.NewString()}, nil
		}
		out = append(out, j)
	}
	return out, nil
}

func (c *ControlAPI) GetJob(ctx context.Context, req controlapi.GetJobRequestObject) (controlapi.GetJobResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.GetJob401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	job, err := c.Store.Jobs().Get(ctx, tenantID, req.Id.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.GetJob404JSONResponse{Code: "not_found", Message: "job not found", RequestId: uuid.NewString()}, nil
		}
		return controlapi.GetJob500JSONResponse{Code: "internal_error", Message: "job lookup failed", RequestId: uuid.NewString()}, nil
	}
	out, err := toControlJob(job)
	if err != nil {
		return controlapi.GetJob500JSONResponse{Code: "internal_error", Message: "job encode failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.GetJob200JSONResponse(out), nil
}

func (c *ControlAPI) CancelJob(ctx context.Context, req controlapi.CancelJobRequestObject) (controlapi.CancelJobResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.CancelJob401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	jobID := req.Id.String()
	job, err := c.Store.Jobs().Get(ctx, tenantID, jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.CancelJob404JSONResponse{Code: "not_found", Message: "job not found", RequestId: uuid.NewString()}, nil
		}
		return controlapi.CancelJob500JSONResponse{Code: "internal_error", Message: "job lookup failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Store.Jobs().Cancel(ctx, tenantID, jobID, c.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.CancelJob404JSONResponse{Code: "not_found", Message: "job not found", RequestId: uuid.NewString()}, nil
		}
		if errors.Is(err, store.ErrConflict) {
			return controlapi.CancelJob409JSONResponse{Code: "conflict", Message: "job is not cancelable", RequestId: uuid.NewString()}, nil
		}
		return controlapi.CancelJob500JSONResponse{Code: "internal_error", Message: "cancel failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Audit.JobCancel(ctx, tenantID, actorID, jobID, job.MachineID); err != nil {
		return controlapi.CancelJob500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.CancelJob204Response{}, nil
}

func (a *AgentAPI) ClaimJob(ctx context.Context, req agentapi.ClaimJobRequestObject) (agentapi.ClaimJobResponseObject, error) {
	tenantID, actorID, errResp := a.requireAuth(ctx)
	if errResp != nil {
		return agentapi.ClaimJob401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	if req.Body == nil {
		return agentapi.ClaimJob400JSONResponse{Code: "bad_request", Message: "missing body", RequestId: uuid.NewString()}, nil
	}
	machineID := req.Body.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return agentapi.ClaimJob400JSONResponse{Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString()}, nil
	}
	job, err := a.Store.Jobs().Claim(ctx, tenantID, machineID, req.Id.String(), a.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return agentapi.ClaimJob404JSONResponse{Code: "not_found", Message: "job not found", RequestId: uuid.NewString()}, nil
		}
		if errors.Is(err, store.ErrConflict) {
			return agentapi.ClaimJob409JSONResponse{Code: "conflict", Message: "job not claimable", RequestId: uuid.NewString()}, nil
		}
		return agentapi.ClaimJob500JSONResponse{Code: "internal_error", Message: "claim failed", RequestId: uuid.NewString()}, nil
	}
	if err := a.Audit.JobClaim(ctx, tenantID, actorID, job.ID, machineID); err != nil {
		return agentapi.ClaimJob500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	out, err := toAgentJob(job)
	if err != nil {
		return agentapi.ClaimJob500JSONResponse{Code: "internal_error", Message: "job encode failed", RequestId: uuid.NewString()}, nil
	}
	return agentapi.ClaimJob200JSONResponse(out), nil
}

func (a *AgentAPI) PostJobResult(ctx context.Context, req agentapi.PostJobResultRequestObject) (agentapi.PostJobResultResponseObject, error) {
	tenantID, actorID, errResp := a.requireAuth(ctx)
	if errResp != nil {
		return agentapi.PostJobResult401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	if req.Body == nil {
		return agentapi.PostJobResult400JSONResponse{Code: "bad_request", Message: "missing body", RequestId: uuid.NewString()}, nil
	}
	machineID := req.Body.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return agentapi.PostJobResult400JSONResponse{Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString()}, nil
	}

	stdout := ""
	if req.Body.Stdout != nil {
		stdout = *req.Body.Stdout
	}
	stderr := ""
	if req.Body.Stderr != nil {
		stderr = *req.Body.Stderr
	}
	if oversizedJobIO(stdout) || oversizedJobIO(stderr) {
		return agentapi.PostJobResult413JSONResponse{Code: "payload_too_large", Message: "stdout/stderr exceeds 64 KiB limit", RequestId: uuid.NewString()}, nil
	}

	var status store.JobStatus
	switch req.Body.Status {
	case agentapi.DadJokeCommonJobResultStatusSucceeded:
		status = store.JobSucceeded
	case agentapi.DadJokeCommonJobResultStatusFailed:
		status = store.JobFailed
	default:
		return agentapi.PostJobResult400JSONResponse{Code: "bad_request", Message: "status must be succeeded or failed", RequestId: uuid.NewString()}, nil
	}

	result := store.JobResult{
		Status:     status,
		ExitCode:   req.Body.ExitCode,
		Stdout:     stdout,
		Stderr:     stderr,
		DurationMS: req.Body.DurationMs,
		FinishedAt: a.now(),
	}
	if err := a.Store.Jobs().Complete(ctx, tenantID, machineID, req.Id.String(), result); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return agentapi.PostJobResult404JSONResponse{Code: "not_found", Message: "job not found", RequestId: uuid.NewString()}, nil
		}
		if errors.Is(err, store.ErrConflict) {
			return agentapi.PostJobResult409JSONResponse{Code: "conflict", Message: "job not completable", RequestId: uuid.NewString()}, nil
		}
		if errors.Is(err, store.ErrInvalid) {
			return agentapi.PostJobResult400JSONResponse{Code: "bad_request", Message: "invalid result", RequestId: uuid.NewString()}, nil
		}
		return agentapi.PostJobResult500JSONResponse{Code: "internal_error", Message: "complete failed", RequestId: uuid.NewString()}, nil
	}
	if err := a.Audit.JobComplete(ctx, tenantID, actorID, req.Id.String(), machineID, string(status)); err != nil {
		return agentapi.PostJobResult500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	return agentapi.PostJobResult204Response{}, nil
}

type authPair struct {
	Code      string
	Message   string
	RequestId string
}

func (a *AgentAPI) requireAuth(ctx context.Context) (tenantID, actorID string, err *authPair) {
	tenantID, e := authctx.TenantID(ctx)
	if e != nil {
		return "", "", &authPair{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}
	}
	actorID, e = authctx.TokenID(ctx)
	if e != nil {
		return "", "", &authPair{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}
	}
	return tenantID, actorID, nil
}

func oversizedJobIO(s string) bool {
	// Cap is on wire byte length (JSON string UTF-8 bytes).
	return len(s) > store.JobIOMaxBytes
}

func toControlJob(job *store.Job) (controlapi.Job, error) {
	id, err := uuid.Parse(job.ID)
	if err != nil {
		return controlapi.Job{}, err
	}
	machineID, err := uuid.Parse(job.MachineID)
	if err != nil {
		return controlapi.Job{}, err
	}
	return controlapi.Job{
		Id:         id,
		MachineId:  machineID,
		Status:     controlapi.DadJokeCommonJobStatus(job.Status),
		Payload:    job.Payload,
		ExitCode:   job.ExitCode,
		Stdout:     job.Stdout,
		Stderr:     job.Stderr,
		DurationMs: job.DurationMS,
		CreatedAt:  job.CreatedAt,
		ExpiresAt:  job.ExpiresAt,
		ClaimedAt:  job.ClaimedAt,
		FinishedAt: job.FinishedAt,
	}, nil
}

func toAgentJob(job *store.Job) (agentapi.Job, error) {
	id, err := uuid.Parse(job.ID)
	if err != nil {
		return agentapi.Job{}, err
	}
	machineID, err := uuid.Parse(job.MachineID)
	if err != nil {
		return agentapi.Job{}, err
	}
	return agentapi.Job{
		Id:         id,
		MachineId:  machineID,
		Status:     agentapi.DadJokeCommonJobStatus(job.Status),
		Payload:    job.Payload,
		ExitCode:   job.ExitCode,
		Stdout:     job.Stdout,
		Stderr:     job.Stderr,
		DurationMs: job.DurationMS,
		CreatedAt:  job.CreatedAt,
		ExpiresAt:  job.ExpiresAt,
		ClaimedAt:  job.ClaimedAt,
		FinishedAt: job.FinishedAt,
	}, nil
}
