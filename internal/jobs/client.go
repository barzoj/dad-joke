package jobs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// API is the subset of the generated AgentAPI client used by Worker.
type API interface {
	ClaimJobWithResponse(ctx context.Context, id openapi_types.UUID, body agentapi.ClaimJobJSONRequestBody, reqEditors ...agentapi.RequestEditorFn) (*agentapi.ClaimJobResponse, error)
	PostJobResultWithResponse(ctx context.Context, id openapi_types.UUID, body agentapi.PostJobResultJSONRequestBody, reqEditors ...agentapi.RequestEditorFn) (*agentapi.PostJobResultResponse, error)
	PostEventsWithResponse(ctx context.Context, body agentapi.PostEventsJSONRequestBody, reqEditors ...agentapi.RequestEditorFn) (*agentapi.PostEventsResponse, error)
}

// ClaimOutcome classifies a claim attempt.
type ClaimOutcome int

const (
	ClaimOK ClaimOutcome = iota
	ClaimSkip // 404 / 409 — do not execute
	ClaimErr
)

// ClaimResult wraps a successful claim.
type ClaimResult struct {
	Outcome ClaimOutcome
	Job     *agentapi.Job
	Err     error
}

func claimJob(ctx context.Context, api API, jobID, machineID string, editor agentapi.RequestEditorFn) ClaimResult {
	id, err := parseUUID(jobID)
	if err != nil {
		return ClaimResult{Outcome: ClaimSkip, Err: err}
	}
	mid, err := parseUUID(machineID)
	if err != nil {
		return ClaimResult{Outcome: ClaimErr, Err: err}
	}
	resp, err := api.ClaimJobWithResponse(ctx, id, agentapi.ClaimJobJSONRequestBody{MachineId: mid}, editor)
	if err != nil {
		return ClaimResult{Outcome: ClaimErr, Err: err}
	}
	switch resp.StatusCode() {
	case http.StatusOK:
		if resp.JSON200 == nil {
			return ClaimResult{Outcome: ClaimErr, Err: fmt.Errorf("jobs: claim 200 with empty body")}
		}
		return ClaimResult{Outcome: ClaimOK, Job: resp.JSON200}
	case http.StatusConflict, http.StatusNotFound:
		return ClaimResult{Outcome: ClaimSkip}
	default:
		return ClaimResult{Outcome: ClaimErr, Err: fmt.Errorf("jobs: claim HTTP %d", resp.StatusCode())}
	}
}

func postResult(ctx context.Context, api API, editor agentapi.RequestEditorFn, jobID, machineID string, status agentapi.DadJokeCommonJobResultStatus, exitCode *int32, stdout, stderr string, durationMS int64) error {
	id, err := parseUUID(jobID)
	if err != nil {
		return err
	}
	mid, err := parseUUID(machineID)
	if err != nil {
		return err
	}
	dur := durationMS
	body := agentapi.PostJobResultJSONRequestBody{
		MachineId:  mid,
		Status:     status,
		ExitCode:   exitCode,
		Stdout:     &stdout,
		Stderr:     &stderr,
		DurationMs: &dur,
	}
	resp, err := api.PostJobResultWithResponse(ctx, id, body, editor)
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusNoContent && resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("jobs: result HTTP %d", resp.StatusCode())
	}
	return nil
}

func postJobEvent(ctx context.Context, api API, editor agentapi.RequestEditorFn, machineID, jobID, status string, durationMS int64) error {
	mid, err := parseUUID(machineID)
	if err != nil {
		return err
	}
	eventID := "job-" + jobID + "-" + status
	if len(eventID) > 128 {
		eventID = eventID[:128]
	}
	body := agentapi.PostEventsJSONRequestBody{
		MachineId: mid,
		Events: []agentapi.EventIngestItem{
			{
				EventId: eventID,
				Ts:      time.Now().UTC(),
				Type:    "job.result",
				Payload: map[string]any{
					"job_id":      jobID,
					"status":      status,
					"duration_ms": durationMS,
				},
			},
		},
	}
	resp, err := api.PostEventsWithResponse(ctx, body, editor)
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusNoContent && resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("jobs: events HTTP %d", resp.StatusCode())
	}
	return nil
}

func parseUUID(s string) (openapi_types.UUID, error) {
	var id openapi_types.UUID
	s = strings.TrimSpace(s)
	if err := id.UnmarshalText([]byte(s)); err != nil {
		// Prefer google/uuid for clearer errors on invalid ids.
		if _, e2 := uuid.Parse(s); e2 != nil {
			return id, fmt.Errorf("jobs: invalid uuid %q", s)
		}
		return id, err
	}
	return id, nil
}

func bearerEditor(token string) agentapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if token == "" {
			return fmt.Errorf("agent token is empty")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}
