package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Config configures the Streamable HTTP MCP handler.
type Config struct {
	// Client is a generated ControlAPI client. Required.
	Client controlapi.ClientWithResponsesInterface
	// Log receives redacted diagnostic lines (optional).
	Log *log.Logger
}

// Server wraps the MCP Streamable HTTP handler and tool registration.
type Server struct {
	client controlapi.ClientWithResponsesInterface
	log    *log.Logger
	http   http.Handler
}

// New builds a Streamable HTTP MCP handler that authorizes every request and
// exposes exactly seven ControlAPI-backed tools.
func New(cfg Config) (*Server, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("mcp: Client is required")
	}
	logger := cfg.Log
	if logger == nil {
		logger = log.Default()
	}
	s := &Server{client: cfg.Client, log: logger}

	impl := &mcpsdk.Implementation{
		Name:    "dad-joke",
		Version: buildinfo.Version,
	}
	mcpServer := mcpsdk.NewServer(impl, nil)
	s.registerTools(mcpServer)

	stream := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return mcpServer
	}, &mcpsdk.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	s.http = s.authMiddleware(stream)
	return s, nil
}

// Handler returns the Streamable HTTP endpoint (mount at /mcp).
func (s *Server) Handler() http.Handler {
	return s.http
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Carry the real MCP peer address into in-process ControlAPI RoundTrips
		// so AuthFailureLimiter keys by client IP (never X-Forwarded-For).
		ctx := WithPeerAddr(r.Context(), r.RemoteAddr)
		r = r.WithContext(ctx)

		// Stateless Streamable HTTP uses POST for all client→server messages.
		// Missing/invalid/revoked credentials are rejected at the HTTP layer.
		if r.Method == http.MethodPost || r.Method == http.MethodGet || r.Method == http.MethodDelete {
			raw, fromCookie := extractCredential(r)
			// Always probe ControlAPI — including empty credentials — so the
			// shared AuthFailureLimiter is charged by peer IP (DRY with ControlAPI).
			if err := s.probeAuth(ctx, raw, fromCookie); err != nil {
				s.logf("mcp auth rejected: %s", redactErr(err, raw))
				s.writeAuthFailure(w, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// probeAuth validates credentials via ControlAPI (ListMachines works for both
// dj_pat_ and dj_ses_ / cookie sessions; GetMe is session-only). Empty
// credentials still hit ControlAPI so missing-auth burns the IP failure budget.
func (s *Server) probeAuth(ctx context.Context, raw string, fromCookie bool) error {
	var editors []controlapi.RequestEditorFn
	if raw != "" {
		editors = append(editors, forwardAuth(raw, fromCookie))
	}
	resp, err := s.client.ListMachinesWithResponse(ctx, editors...)
	if err != nil {
		return mapClientErr(err, raw)
	}
	if resp.StatusCode() == http.StatusOK {
		return nil
	}
	var hdr http.Header
	if resp.HTTPResponse != nil {
		hdr = resp.HTTPResponse.Header
	}
	return mapHTTPStatus(resp.StatusCode(), resp.Body, hdr, raw)
}

func (s *Server) writeAuthFailure(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	code := "unauthorized"
	message := "missing or invalid credentials"
	retryAfter := ""

	var te *ToolError
	if errors.As(err, &te) && te != nil {
		switch {
		case te.Status == http.StatusTooManyRequests:
			status = http.StatusTooManyRequests
			code = "rate_limited"
			message = firstNonEmpty(te.Message, "too many requests")
			retryAfter = te.RetryAfter
		case te.Status >= 500 || te.Status == http.StatusServiceUnavailable:
			status = http.StatusServiceUnavailable
			code = firstNonEmpty(te.Code, "unavailable")
			message = firstNonEmpty(te.Message, "authentication backend unavailable")
		case te.Status == http.StatusForbidden, te.Status == http.StatusUnauthorized, te.Status == 0:
			// keep 401
		default:
			// Auth gate: fail closed as 401 for unexpected 4xx.
		}
		message = RedactSecrets(message)
	}

	if retryAfter != "" {
		w.Header().Set("Retry-After", retryAfter)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(controlapi.ErrorResponse{
		Code:      code,
		Message:   message,
		RequestId: uuid.NewString(),
	})
}

func (s *Server) logf(format string, args ...any) {
	if s.log == nil {
		return
	}
	s.log.Printf(format, args...)
}

func (s *Server) toolEditors(req *mcpsdk.CallToolRequest) ([]controlapi.RequestEditorFn, string, error) {
	var h http.Header
	if req != nil && req.Extra != nil {
		h = req.Extra.Header
	}
	raw, fromCookie := headersAuth(h)
	if raw == "" {
		return nil, "", &ToolError{Code: "unauthorized", Message: "missing or invalid credentials", Status: http.StatusUnauthorized}
	}
	return []controlapi.RequestEditorFn{forwardAuth(raw, fromCookie)}, raw, nil
}
