package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/audit"
	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/tokenhash"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ControlAPI implements controlapi.StrictServerInterface.
type ControlAPI struct {
	Store  store.Store
	Audit  audit.Logger
	Clock  func() time.Time
	Online time.Duration // machine considered online if last_seen within this window

	AllowSignup   bool               // when false, Register returns 403
	CookieSecure  bool               // Secure flag on session cookies (default true)
	LoginFailures *ratelimit.Limiter // per-IP and per-email failed logins
}

func (c *ControlAPI) now() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

func (c *ControlAPI) onlineWindow() time.Duration {
	if c.Online > 0 {
		return c.Online
	}
	return 3 * time.Minute
}

func (c *ControlAPI) GetHealthz(context.Context, controlapi.GetHealthzRequestObject) (controlapi.GetHealthzResponseObject, error) {
	// Authoritative healthz is mounted on the root mux; this satisfies the interface.
	return controlapi.GetHealthz200JSONResponse{Status: controlapi.Ok}, nil
}

func (c *ControlAPI) CreateAgentToken(ctx context.Context, req controlapi.CreateAgentTokenRequestObject) (controlapi.CreateAgentTokenResponseObject, error) {
	return c.createToken(ctx, req.Body, store.TokenKindAgent)
}

func (c *ControlAPI) ListAgentTokens(ctx context.Context, _ controlapi.ListAgentTokensRequestObject) (controlapi.ListAgentTokensResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.ListAgentTokens401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	tokens, err := c.Store.Tokens().ListByTenant(ctx, tenantID, store.TokenKindAgent)
	if err != nil {
		return controlapi.ListAgentTokens500JSONResponse{Code: "internal_error", Message: "list failed", RequestId: uuid.NewString()}, nil
	}
	out := make(controlapi.ListAgentTokens200JSONResponse, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, toAgentTokenSummary(tok))
	}
	return out, nil
}

func (c *ControlAPI) DeleteAgentToken(ctx context.Context, req controlapi.DeleteAgentTokenRequestObject) (controlapi.DeleteAgentTokenResponseObject, error) {
	return c.deleteToken(ctx, req.Id, store.TokenKindAgent)
}

func (c *ControlAPI) CreateApiKey(ctx context.Context, req controlapi.CreateApiKeyRequestObject) (controlapi.CreateApiKeyResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.CreateApiKey401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	if req.Body == nil || strings.TrimSpace(req.Body.Name) == "" {
		return controlapi.CreateApiKey400JSONResponse{Code: "bad_request", Message: "name is required", RequestId: uuid.NewString()}, nil
	}
	raw, prefix, hash, err := tokenhash.Generate(store.TokenKindPAT)
	if err != nil {
		return controlapi.CreateApiKey500JSONResponse{Code: "internal_error", Message: "token generate failed", RequestId: uuid.NewString()}, nil
	}
	now := c.now()
	tok := &store.Token{
		TenantID:  tenantID,
		Kind:      store.TokenKindPAT,
		Name:      strings.TrimSpace(req.Body.Name),
		Prefix:    prefix,
		Hash:      hash,
		CreatedAt: now,
		ExpiresAt: req.Body.ExpiresAt,
	}
	if err := c.Store.Tokens().Create(ctx, tok); err != nil {
		return controlapi.CreateApiKey500JSONResponse{Code: "internal_error", Message: "token create failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Audit.TokenIssue(ctx, tenantID, actorID, tok.ID, store.TokenKindPAT, prefix, tok.Name); err != nil {
		return controlapi.CreateApiKey500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	id, err := uuid.Parse(tok.ID)
	if err != nil {
		return controlapi.CreateApiKey500JSONResponse{Code: "internal_error", Message: "invalid token id", RequestId: uuid.NewString()}, nil
	}
	return controlapi.CreateApiKey201JSONResponse{
		Id:         id,
		Name:       tok.Name,
		Prefix:     tok.Prefix,
		Token:      raw,
		CreatedAt:  tok.CreatedAt,
		LastUsedAt: nil,
		ExpiresAt:  tok.ExpiresAt,
	}, nil
}

func (c *ControlAPI) ListApiKeys(ctx context.Context, _ controlapi.ListApiKeysRequestObject) (controlapi.ListApiKeysResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.ListApiKeys401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	tokens, err := c.Store.Tokens().ListByTenant(ctx, tenantID, store.TokenKindPAT)
	if err != nil {
		return controlapi.ListApiKeys500JSONResponse{Code: "internal_error", Message: "list failed", RequestId: uuid.NewString()}, nil
	}
	out := make(controlapi.ListApiKeys200JSONResponse, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, toApiKeySummary(tok))
	}
	return out, nil
}

func (c *ControlAPI) DeleteApiKey(ctx context.Context, req controlapi.DeleteApiKeyRequestObject) (controlapi.DeleteApiKeyResponseObject, error) {
	return c.deletePAT(ctx, req.Id)
}

func (c *ControlAPI) ListMachines(ctx context.Context, _ controlapi.ListMachinesRequestObject) (controlapi.ListMachinesResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.ListMachines401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	machines, err := c.Store.Machines().ListByTenant(ctx, tenantID)
	if err != nil {
		return controlapi.ListMachines500JSONResponse{Code: "internal_error", Message: "list failed", RequestId: uuid.NewString()}, nil
	}
	out := make(controlapi.ListMachines200JSONResponse, 0, len(machines))
	now := c.now()
	for _, m := range machines {
		out = append(out, toMachine(m, now, c.onlineWindow()))
	}
	return out, nil
}

func (c *ControlAPI) GetMachine(ctx context.Context, req controlapi.GetMachineRequestObject) (controlapi.GetMachineResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.GetMachine401JSONResponse{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}, nil
	}
	m, err := c.Store.Machines().Get(ctx, tenantID, req.MachineId.String())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.GetMachine404JSONResponse{Code: "not_found", Message: "machine not found", RequestId: uuid.NewString()}, nil
		}
		return controlapi.GetMachine500JSONResponse{Code: "internal_error", Message: "get failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.GetMachine200JSONResponse(toMachine(*m, c.now(), c.onlineWindow())), nil
}

type authErr struct {
	Code, Message, RequestId string
}

func (c *ControlAPI) requireAuth(ctx context.Context) (tenantID, actorID string, err *authErr) {
	tenantID, e := authctx.TenantID(ctx)
	if e != nil {
		return "", "", &authErr{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}
	}
	actorID, e = authctx.TokenID(ctx)
	if e != nil {
		return "", "", &authErr{Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString()}
	}
	return tenantID, actorID, nil
}

func (c *ControlAPI) createToken(ctx context.Context, body *controlapi.CreateAgentTokenJSONRequestBody, kind store.TokenKind) (controlapi.CreateAgentTokenResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.CreateAgentToken401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	if body == nil || strings.TrimSpace(body.Name) == "" {
		return controlapi.CreateAgentToken400JSONResponse{Code: "bad_request", Message: "name is required", RequestId: uuid.NewString()}, nil
	}
	raw, prefix, hash, err := tokenhash.Generate(kind)
	if err != nil {
		return controlapi.CreateAgentToken500JSONResponse{Code: "internal_error", Message: "token generate failed", RequestId: uuid.NewString()}, nil
	}
	now := c.now()
	tok := &store.Token{
		TenantID:  tenantID,
		Kind:      kind,
		Name:      strings.TrimSpace(body.Name),
		Prefix:    prefix,
		Hash:      hash,
		CreatedAt: now,
		ExpiresAt: body.ExpiresAt,
	}
	if err := c.Store.Tokens().Create(ctx, tok); err != nil {
		return controlapi.CreateAgentToken500JSONResponse{Code: "internal_error", Message: "token create failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Audit.TokenIssue(ctx, tenantID, actorID, tok.ID, kind, prefix, tok.Name); err != nil {
		return controlapi.CreateAgentToken500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	id, err := uuid.Parse(tok.ID)
	if err != nil {
		return controlapi.CreateAgentToken500JSONResponse{Code: "internal_error", Message: "invalid token id", RequestId: uuid.NewString()}, nil
	}
	return controlapi.CreateAgentToken201JSONResponse{
		Id:         id,
		Name:       tok.Name,
		Prefix:     tok.Prefix,
		Token:      raw,
		CreatedAt:  tok.CreatedAt,
		LastUsedAt: nil,
		ExpiresAt:  tok.ExpiresAt,
	}, nil
}

func (c *ControlAPI) deleteToken(ctx context.Context, id openapi_types.UUID, kind store.TokenKind) (controlapi.DeleteAgentTokenResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.DeleteAgentToken401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	tok, err := c.Store.Tokens().GetByID(ctx, tenantID, id.String())
	if err != nil || tok.Kind != kind {
		return controlapi.DeleteAgentToken404JSONResponse{Code: "not_found", Message: "token not found", RequestId: uuid.NewString()}, nil
	}
	now := c.now()
	if err := c.Store.Tokens().Revoke(ctx, tenantID, id.String(), now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.DeleteAgentToken404JSONResponse{Code: "not_found", Message: "token not found", RequestId: uuid.NewString()}, nil
		}
		return controlapi.DeleteAgentToken500JSONResponse{Code: "internal_error", Message: "revoke failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Audit.TokenRevoke(ctx, tenantID, actorID, id.String(), kind); err != nil {
		return controlapi.DeleteAgentToken500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.DeleteAgentToken204Response{}, nil
}

func (c *ControlAPI) deletePAT(ctx context.Context, id openapi_types.UUID) (controlapi.DeleteApiKeyResponseObject, error) {
	tenantID, actorID, errResp := c.requireAuth(ctx)
	if errResp != nil {
		return controlapi.DeleteApiKey401JSONResponse{Code: errResp.Code, Message: errResp.Message, RequestId: errResp.RequestId}, nil
	}
	tok, err := c.Store.Tokens().GetByID(ctx, tenantID, id.String())
	if err != nil || tok.Kind != store.TokenKindPAT {
		return controlapi.DeleteApiKey404JSONResponse{Code: "not_found", Message: "api key not found", RequestId: uuid.NewString()}, nil
	}
	now := c.now()
	if err := c.Store.Tokens().Revoke(ctx, tenantID, id.String(), now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.DeleteApiKey404JSONResponse{Code: "not_found", Message: "api key not found", RequestId: uuid.NewString()}, nil
		}
		return controlapi.DeleteApiKey500JSONResponse{Code: "internal_error", Message: "revoke failed", RequestId: uuid.NewString()}, nil
	}
	if err := c.Audit.TokenRevoke(ctx, tenantID, actorID, id.String(), store.TokenKindPAT); err != nil {
		return controlapi.DeleteApiKey500JSONResponse{Code: "internal_error", Message: "audit failed", RequestId: uuid.NewString()}, nil
	}
	return controlapi.DeleteApiKey204Response{}, nil
}

func toAgentTokenSummary(tok store.Token) controlapi.AgentTokenSummary {
	id, _ := uuid.Parse(tok.ID)
	return controlapi.AgentTokenSummary{
		Id:         id,
		Name:       tok.Name,
		Prefix:     tok.Prefix,
		CreatedAt:  tok.CreatedAt,
		LastUsedAt: tok.LastUsedAt,
		RevokedAt:  tok.RevokedAt,
		ExpiresAt:  tok.ExpiresAt,
	}
}

func toApiKeySummary(tok store.Token) controlapi.ApiKeySummary {
	id, _ := uuid.Parse(tok.ID)
	return controlapi.ApiKeySummary{
		Id:         id,
		Name:       tok.Name,
		Prefix:     tok.Prefix,
		CreatedAt:  tok.CreatedAt,
		LastUsedAt: tok.LastUsedAt,
		RevokedAt:  tok.RevokedAt,
		ExpiresAt:  tok.ExpiresAt,
	}
}

func toMachine(m store.Machine, now time.Time, onlineWindow time.Duration) controlapi.Machine {
	id, _ := uuid.Parse(m.ID)
	status := controlapi.Offline
	if !m.LastSeenAt.IsZero() && now.Sub(m.LastSeenAt) <= onlineWindow {
		status = controlapi.Online
	}
	return controlapi.Machine{
		MachineId:            id,
		Hostname:             m.Hostname,
		OsVersion:            m.OSVersion,
		LastSeenAt:           m.LastSeenAt,
		AgentVersion:         m.AgentVersion,
		AppliedPolicyVersion: m.AppliedPolicyVersion,
		DesiredPolicyVersion: m.DesiredPolicyVersion,
		Status:               status,
	}
}

// Compile-time check.
var _ controlapi.StrictServerInterface = (*ControlAPI)(nil)
