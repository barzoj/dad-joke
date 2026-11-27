package apigen_test

import (
	"context"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func mustUUID(s string) openapi_types.UUID {
	var id openapi_types.UUID
	if err := id.UnmarshalText([]byte(s)); err != nil {
		panic(err)
	}
	return id
}

// agentStub implements agentapi.StrictServerInterface for contract round-trips.
// Mode switches exercise ErrorResponse shapes (401 / 429) without product logic.
type agentStub struct {
	mode string // "", "unauthorized", "rate_limited"
}

func (s agentStub) GetHealthz(context.Context, agentapi.GetHealthzRequestObject) (agentapi.GetHealthzResponseObject, error) {
	return agentapi.GetHealthz200JSONResponse{Status: agentapi.Ok}, nil
}

func (s agentStub) PostHeartbeat(_ context.Context, req agentapi.PostHeartbeatRequestObject) (agentapi.PostHeartbeatResponseObject, error) {
	switch s.mode {
	case "unauthorized":
		return agentapi.PostHeartbeat401JSONResponse{
			Code:      "unauthorized",
			Message:   "missing or invalid agent token",
			RequestId: "req-agent-401",
		}, nil
	case "rate_limited":
		return agentapi.PostHeartbeat429JSONResponse{
			Body: agentapi.ErrorResponse{
				Code:      "rate_limited",
				Message:   "too many heartbeats",
				RequestId: "req-agent-429",
			},
			Headers: agentapi.PostHeartbeat429ResponseHeaders{RetryAfter: "30"},
		}, nil
	}
	body := req.Body
	if body == nil {
		return agentapi.PostHeartbeat400JSONResponse{
			Code:      "bad_request",
			Message:   "missing body",
			RequestId: "req-agent-400",
		}, nil
	}
	return agentapi.PostHeartbeat200JSONResponse{
		ServerTime:           time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC),
		DesiredPolicyVersion: body.AppliedPolicyVersion,
		PendingJobIds:        []string{"job-1"},
	}, nil
}

// controlStub implements controlapi.StrictServerInterface for contract round-trips.
type controlStub struct {
	mode string // "", "unauthorized", "rate_limited", "not_found"
}

func (s controlStub) GetHealthz(context.Context, controlapi.GetHealthzRequestObject) (controlapi.GetHealthzResponseObject, error) {
	return controlapi.GetHealthz200JSONResponse{Status: controlapi.Ok}, nil
}

func (s controlStub) CreateAgentToken(_ context.Context, req controlapi.CreateAgentTokenRequestObject) (controlapi.CreateAgentTokenResponseObject, error) {
	if errResp, ok := s.errCreateAgentToken(); ok {
		return errResp, nil
	}
	name := ""
	if req.Body != nil {
		name = req.Body.Name
	}
	id := mustUUID("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return controlapi.CreateAgentToken201JSONResponse{
		Id:         id,
		Name:       name,
		Prefix:     "dj_agt_",
		Token:      "dj_agt_" + strings.Repeat("a", 64),
		CreatedAt:  now,
		LastUsedAt: nil,
		ExpiresAt:  nil,
	}, nil
}

func (s controlStub) errCreateAgentToken() (controlapi.CreateAgentTokenResponseObject, bool) {
	switch s.mode {
	case "unauthorized":
		return controlapi.CreateAgentToken401JSONResponse{
			Code:      "unauthorized",
			Message:   "missing or invalid control token",
			RequestId: "req-ctrl-401",
		}, true
	case "rate_limited":
		return controlapi.CreateAgentToken429JSONResponse{
			Body: controlapi.ErrorResponse{
				Code:      "rate_limited",
				Message:   "too many token creates",
				RequestId: "req-ctrl-429",
			},
			Headers: controlapi.CreateAgentToken429ResponseHeaders{RetryAfter: "60"},
		}, true
	}
	return nil, false
}

func (s controlStub) ListAgentTokens(context.Context, controlapi.ListAgentTokensRequestObject) (controlapi.ListAgentTokensResponseObject, error) {
	id := mustUUID("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return controlapi.ListAgentTokens200JSONResponse{
		{
			Id:         id,
			Name:       "default",
			Prefix:     "dj_agt_",
			CreatedAt:  now,
			LastUsedAt: nil,
			RevokedAt:  nil,
			ExpiresAt:  nil,
		},
	}, nil
}

func (s controlStub) DeleteAgentToken(context.Context, controlapi.DeleteAgentTokenRequestObject) (controlapi.DeleteAgentTokenResponseObject, error) {
	return controlapi.DeleteAgentToken204Response{}, nil
}

func (s controlStub) CreateApiKey(_ context.Context, req controlapi.CreateApiKeyRequestObject) (controlapi.CreateApiKeyResponseObject, error) {
	name := ""
	if req.Body != nil {
		name = req.Body.Name
	}
	id := mustUUID("22222222-2222-4222-8222-222222222222")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return controlapi.CreateApiKey201JSONResponse{
		Id:         id,
		Name:       name,
		Prefix:     "dj_pat_",
		Token:      "dj_pat_" + strings.Repeat("b", 64),
		CreatedAt:  now,
		LastUsedAt: nil,
		ExpiresAt:  nil,
	}, nil
}

func (s controlStub) ListApiKeys(context.Context, controlapi.ListApiKeysRequestObject) (controlapi.ListApiKeysResponseObject, error) {
	id := mustUUID("22222222-2222-4222-8222-222222222222")
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return controlapi.ListApiKeys200JSONResponse{
		{
			Id:         id,
			Name:       "mcp",
			Prefix:     "dj_pat_",
			CreatedAt:  now,
			LastUsedAt: nil,
			RevokedAt:  nil,
			ExpiresAt:  nil,
		},
	}, nil
}

func (s controlStub) DeleteApiKey(context.Context, controlapi.DeleteApiKeyRequestObject) (controlapi.DeleteApiKeyResponseObject, error) {
	return controlapi.DeleteApiKey204Response{}, nil
}

func (s controlStub) ListMachines(context.Context, controlapi.ListMachinesRequestObject) (controlapi.ListMachinesResponseObject, error) {
	return controlapi.ListMachines200JSONResponse{sampleMachine()}, nil
}

func (s controlStub) GetMachine(_ context.Context, req controlapi.GetMachineRequestObject) (controlapi.GetMachineResponseObject, error) {
	if s.mode == "not_found" {
		return controlapi.GetMachine404JSONResponse{
			Code:      "not_found",
			Message:   "machine not found",
			RequestId: "req-machine-404",
		}, nil
	}
	m := sampleMachine()
	m.MachineId = req.MachineId
	return controlapi.GetMachine200JSONResponse(m), nil
}

func (s controlStub) Register(_ context.Context, req controlapi.RegisterRequestObject) (controlapi.RegisterResponseObject, error) {
	email := openapi_types.Email("parent@example.com")
	if req.Body != nil {
		email = req.Body.Email
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return controlapi.Register201JSONResponse{
		Body: controlapi.AuthSession{
			AccessToken: "dj_ses_" + strings.Repeat("c", 64),
			TokenType:   controlapi.Bearer,
			ExpiresAt:   now.Add(24 * time.Hour),
			User: controlapi.UserMe{
				Id:       mustUUID("44444444-4444-4444-8444-444444444444"),
				Email:    email,
				TenantId: mustUUID("55555555-5555-4555-8555-555555555555"),
			},
		},
		Headers: controlapi.Register201ResponseHeaders{
			SetCookie: "dj_session=dj_ses_stub; Path=/; HttpOnly; SameSite=Lax; Secure",
		},
	}, nil
}

func (s controlStub) Login(_ context.Context, req controlapi.LoginRequestObject) (controlapi.LoginResponseObject, error) {
	email := openapi_types.Email("parent@example.com")
	if req.Body != nil {
		email = req.Body.Email
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	return controlapi.Login200JSONResponse{
		Body: controlapi.AuthSession{
			AccessToken: "dj_ses_" + strings.Repeat("d", 64),
			TokenType:   controlapi.Bearer,
			ExpiresAt:   now.Add(24 * time.Hour),
			User: controlapi.UserMe{
				Id:       mustUUID("44444444-4444-4444-8444-444444444444"),
				Email:    email,
				TenantId: mustUUID("55555555-5555-4555-8555-555555555555"),
			},
		},
		Headers: controlapi.Login200ResponseHeaders{
			SetCookie: "dj_session=dj_ses_stub; Path=/; HttpOnly; SameSite=Lax; Secure",
		},
	}, nil
}

func (s controlStub) Logout(context.Context, controlapi.LogoutRequestObject) (controlapi.LogoutResponseObject, error) {
	return controlapi.Logout204Response{
		Headers: controlapi.Logout204ResponseHeaders{
			SetCookie: "dj_session=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0; Secure",
		},
	}, nil
}

func (s controlStub) GetMe(context.Context, controlapi.GetMeRequestObject) (controlapi.GetMeResponseObject, error) {
	return controlapi.GetMe200JSONResponse{
		Id:       mustUUID("44444444-4444-4444-8444-444444444444"),
		Email:    "parent@example.com",
		TenantId: mustUUID("55555555-5555-4555-8555-555555555555"),
	}, nil
}

func sampleMachine() controlapi.Machine {
	return controlapi.Machine{
		MachineId:            mustUUID("33333333-3333-4333-8333-333333333333"),
		Hostname:             "kid-pc",
		OsVersion:            "Windows 11 Home",
		LastSeenAt:           time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC),
		AgentVersion:         "0.0.1-dev",
		AppliedPolicyVersion: 3,
		DesiredPolicyVersion: 4,
		Status:               controlapi.Online,
	}
}

// Compile-time assertions that stubs satisfy the generated strict-server interfaces.
var (
	_ agentapi.StrictServerInterface   = agentStub{}
	_ controlapi.StrictServerInterface = controlStub{}
)
