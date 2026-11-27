package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ToolNames returns the exact seven tool names exposed by this server.
func ToolNames() []string {
	return []string{
		"list_machines",
		"get_machine",
		"set_policy",
		"run_script",
		"get_job",
		"list_events",
		"manage_agent_tokens",
	}
}

func (s *Server) registerTools(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_machines",
		Description: "List enrolled machines for the authenticated tenant",
	}, s.listMachines)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "get_machine",
		Description: "Get one enrolled machine by machine_id",
	}, s.getMachine)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_policy",
		Description: "Create or update the versioned desired policy for a machine",
	}, s.setPolicy)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "run_script",
		Description: "Enqueue a one-shot script/job for a machine",
	}, s.runScript)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "get_job",
		Description: "Get job status/output, or cancel a pending job (action=cancel)",
	}, s.getJob)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_events",
		Description: "List telemetry events for a machine",
	}, s.listEvents)

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "manage_agent_tokens",
		Description: "Create, list, or revoke agent tokens (action=create|list|revoke)",
	}, s.manageAgentTokens)
}

type emptyArgs struct{}

type getMachineArgs struct {
	MachineID string `json:"machine_id" jsonschema:"Machine id (UUIDv4)"`
}

type setPolicyArgs struct {
	MachineID        string         `json:"machine_id" jsonschema:"Machine id (UUIDv4)"`
	ExpectedVersion  int32          `json:"expected_version" jsonschema:"Expected current policy version; 0 creates version 1"`
	Document         map[string]any `json:"document" jsonschema:"Opaque low-level desired-state JSON object"`
}

type runScriptArgs struct {
	MachineID      string `json:"machine_id" jsonschema:"Machine id (UUIDv4)"`
	Type           string `json:"type" jsonschema:"Interpreter: powershell or cmd"`
	Payload        string `json:"payload" jsonschema:"Command/script payload (max 64 KiB)"`
	ExpiresAt      string `json:"expires_at" jsonschema:"RFC 3339 UTC expiry for unclaimed jobs"`
	TimeoutSeconds *int32 `json:"timeout_seconds,omitempty" jsonschema:"Optional hard timeout 1..300 seconds"`
}

type getJobArgs struct {
	JobID  string `json:"job_id" jsonschema:"Job id (UUIDv4)"`
	Action string `json:"action,omitempty" jsonschema:"Optional action: omit to read, or cancel"`
}

type listEventsArgs struct {
	MachineID string `json:"machine_id" jsonschema:"Machine id (UUIDv4)"`
	Type      string `json:"type,omitempty" jsonschema:"Optional exact event type filter"`
	From      string `json:"from,omitempty" jsonschema:"Inclusive lower bound RFC 3339"`
	To        string `json:"to,omitempty" jsonschema:"Exclusive upper bound RFC 3339"`
	Limit     *int32 `json:"limit,omitempty" jsonschema:"Page size 1..100 (default 50)"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Opaque cursor from a previous next_cursor"`
}

type manageAgentTokensArgs struct {
	Action string `json:"action" jsonschema:"One of create, list, revoke"`
	Name   string `json:"name,omitempty" jsonschema:"Token label (required for create)"`
	ID     string `json:"id,omitempty" jsonschema:"Token id (required for revoke)"`
}

func (s *Server) listMachines(ctx context.Context, req *mcpsdk.CallToolRequest, _ emptyArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.client.ListMachinesWithResponse(ctx, editors...)
	if err != nil {
		return nil, nil, mapClientErr(err, secret)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
	}
	// MCP structuredContent must be a JSON object (never a top-level array).
	machines := *resp.JSON200
	if machines == nil {
		machines = controlapi.ListMachines200JSONResponse{}
	}
	return jsonToolResult(map[string]any{"machines": machines})
}

func (s *Server) getMachine(ctx context.Context, req *mcpsdk.CallToolRequest, in getMachineArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	id, err := parseUUID("machine_id", in.MachineID)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.client.GetMachineWithResponse(ctx, id, editors...)
	if err != nil {
		return nil, nil, mapClientErr(err, secret)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
	}
	return jsonToolResult(*resp.JSON200)
}

func (s *Server) setPolicy(ctx context.Context, req *mcpsdk.CallToolRequest, in setPolicyArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	id, err := parseUUID("machine_id", in.MachineID)
	if err != nil {
		return nil, nil, err
	}
	if in.Document == nil {
		return nil, nil, validationError("document is required")
	}
	body := controlapi.PutMachinePolicyJSONRequestBody{
		ExpectedVersion: in.ExpectedVersion,
		Document:        in.Document,
	}
	resp, err := s.client.PutMachinePolicyWithResponse(ctx, id, body, editors...)
	if err != nil {
		return nil, nil, mapClientErr(err, secret)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
	}
	return jsonToolResult(*resp.JSON200)
}

func (s *Server) runScript(ctx context.Context, req *mcpsdk.CallToolRequest, in runScriptArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	id, err := parseUUID("machine_id", in.MachineID)
	if err != nil {
		return nil, nil, err
	}
	if err := requireNonEmpty("payload", in.Payload); err != nil {
		return nil, nil, err
	}
	jobType, err := parseJobType(in.Type)
	if err != nil {
		return nil, nil, err
	}
	expires, err := parseTime("expires_at", in.ExpiresAt)
	if err != nil {
		return nil, nil, err
	}
	body := controlapi.CreateMachineJobJSONRequestBody{
		Type:           jobType,
		Payload:        in.Payload,
		ExpiresAt:      expires,
		TimeoutSeconds: in.TimeoutSeconds,
	}
	resp, err := s.client.CreateMachineJobWithResponse(ctx, id, body, editors...)
	if err != nil {
		return nil, nil, mapClientErr(err, secret)
	}
	if resp.StatusCode() != http.StatusCreated || resp.JSON201 == nil {
		return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
	}
	return jsonToolResult(*resp.JSON201)
}

func (s *Server) getJob(ctx context.Context, req *mcpsdk.CallToolRequest, in getJobArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	id, err := parseUUID("job_id", in.JobID)
	if err != nil {
		return nil, nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "", "get", "read", "status":
		resp, err := s.client.GetJobWithResponse(ctx, id, editors...)
		if err != nil {
			return nil, nil, mapClientErr(err, secret)
		}
		if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
			return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
		}
		return jsonToolResult(*resp.JSON200)
	case "cancel":
		resp, err := s.client.CancelJobWithResponse(ctx, id, editors...)
		if err != nil {
			return nil, nil, mapClientErr(err, secret)
		}
		if resp.StatusCode() != http.StatusNoContent {
			return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
		}
		return jsonToolResult(map[string]any{"id": id.String(), "canceled": true})
	default:
		return nil, nil, validationError("action must be empty or cancel")
	}
}

func (s *Server) listEvents(ctx context.Context, req *mcpsdk.CallToolRequest, in listEventsArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	id, err := parseUUID("machine_id", in.MachineID)
	if err != nil {
		return nil, nil, err
	}
	params := &controlapi.ListMachineEventsParams{}
	if strings.TrimSpace(in.Type) != "" {
		t := strings.TrimSpace(in.Type)
		params.Type = &t
	}
	if strings.TrimSpace(in.From) != "" {
		t, err := parseTime("from", in.From)
		if err != nil {
			return nil, nil, err
		}
		params.From = &t
	}
	if strings.TrimSpace(in.To) != "" {
		t, err := parseTime("to", in.To)
		if err != nil {
			return nil, nil, err
		}
		params.To = &t
	}
	if in.Limit != nil {
		params.Limit = in.Limit
	}
	if strings.TrimSpace(in.Cursor) != "" {
		c := strings.TrimSpace(in.Cursor)
		params.Cursor = &c
	}
	resp, err := s.client.ListMachineEventsWithResponse(ctx, id, params, editors...)
	if err != nil {
		return nil, nil, mapClientErr(err, secret)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
	}
	page := *resp.JSON200
	if page.Events == nil {
		page.Events = []controlapi.MachineEvent{}
	}
	return jsonToolResult(page)
}

func (s *Server) manageAgentTokens(ctx context.Context, req *mcpsdk.CallToolRequest, in manageAgentTokensArgs) (*mcpsdk.CallToolResult, any, error) {
	editors, secret, err := s.toolEditors(req)
	if err != nil {
		return nil, nil, err
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "list":
		resp, err := s.client.ListAgentTokensWithResponse(ctx, editors...)
		if err != nil {
			return nil, nil, mapClientErr(err, secret)
		}
		if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
			return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
		}
		// MCP structuredContent must be a JSON object (never a top-level array).
		tokens := *resp.JSON200
		if tokens == nil {
			tokens = controlapi.ListAgentTokens200JSONResponse{}
		}
		return jsonToolResult(map[string]any{"tokens": tokens})
	case "create":
		if err := requireNonEmpty("name", in.Name); err != nil {
			return nil, nil, err
		}
		body := controlapi.CreateAgentTokenJSONRequestBody{Name: strings.TrimSpace(in.Name)}
		resp, err := s.client.CreateAgentTokenWithResponse(ctx, body, editors...)
		if err != nil {
			return nil, nil, mapClientErr(err, secret)
		}
		if resp.StatusCode() != http.StatusCreated || resp.JSON201 == nil {
			return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
		}
		return jsonToolResult(*resp.JSON201)
	case "revoke":
		id, err := parseUUID("id", in.ID)
		if err != nil {
			return nil, nil, err
		}
		resp, err := s.client.DeleteAgentTokenWithResponse(ctx, id, editors...)
		if err != nil {
			return nil, nil, mapClientErr(err, secret)
		}
		if resp.StatusCode() != http.StatusNoContent {
			return nil, nil, mapResponseStatus(resp.StatusCode(), resp.Body, resp.HTTPResponse, secret)
		}
		return jsonToolResult(map[string]any{"id": id.String(), "revoked": true})
	default:
		return nil, nil, validationError("action must be create, list, or revoke")
	}
}

func jsonToolResult(v any) (*mcpsdk.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, nil, &ToolError{Code: "internal_error", Message: "encode failed"}
	}
	// CallToolResult.structuredContent MUST be a JSON object (MCP spec / SEP-1860).
	if !jsonObjectPayload(b) {
		return nil, nil, &ToolError{Code: "internal_error", Message: "structured content must be a JSON object"}
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(b)}},
	}, v, nil
}

// jsonObjectPayload reports whether raw is a JSON object (leading '{'),
// skipping insignificant whitespace. Arrays/primitives/null are rejected.
func jsonObjectPayload(raw []byte) bool {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

func mapResponseStatus(status int, body []byte, httpResp *http.Response, knownSecrets ...string) error {
	var hdr http.Header
	if httpResp != nil {
		hdr = httpResp.Header
	}
	return mapHTTPStatus(status, body, hdr, knownSecrets...)
}

func parseUUID(field, raw string) (openapi_types.UUID, error) {
	var zero openapi_types.UUID
	if err := requireNonEmpty(field, raw); err != nil {
		return zero, err
	}
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return zero, validationError(field + " must be a UUID")
	}
	return id, nil
}

func parseTime(field, raw string) (time.Time, error) {
	if err := requireNonEmpty(field, raw); err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	}
	if err != nil {
		return time.Time{}, validationError(field + " must be RFC 3339")
	}
	return t.UTC(), nil
}

func parseJobType(raw string) (controlapi.DadJokeCommonJobType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "powershell":
		return controlapi.Powershell, nil
	case "cmd":
		return controlapi.Cmd, nil
	case "":
		return "", validationError("type is required (powershell or cmd)")
	default:
		return "", validationError("type must be powershell or cmd")
	}
}
