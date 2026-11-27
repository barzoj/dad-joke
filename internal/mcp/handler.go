package mcp

import (
	"context"
	"encoding/json"
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
		Stateless:     true,
		JSONResponse:  true,
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
		// Stateless Streamable HTTP uses POST for all client→server messages.
		// Missing auth on POST must be 401 at the HTTP transport layer.
		if r.Method == http.MethodPost || r.Method == http.MethodGet || r.Method == http.MethodDelete {
			raw, fromCookie := extractCredential(r)
			if raw == "" {
				s.writeUnauthorized(w)
				return
			}
			if err := s.probeAuth(r.Context(), raw, fromCookie); err != nil {
				s.logf("mcp auth rejected: %s", redactErr(err, raw))
				s.writeUnauthorized(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// probeAuth validates credentials via ControlAPI (ListMachines works for both
// dj_pat_ and dj_ses_ / cookie sessions; GetMe is session-only).
func (s *Server) probeAuth(ctx context.Context, raw string, fromCookie bool) error {
	resp, err := s.client.ListMachinesWithResponse(ctx, forwardAuth(raw, fromCookie))
	if err != nil {
		return mapClientErr(err, raw)
	}
	switch resp.StatusCode() {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return mapHTTPStatus(resp.StatusCode(), resp.Body, raw)
	default:
		// Unexpected upstream status still blocks MCP (fail closed on auth path).
		return mapHTTPStatus(resp.StatusCode(), resp.Body, raw)
	}
}

func (s *Server) writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(controlapi.ErrorResponse{
		Code:      "unauthorized",
		Message:   "missing or invalid credentials",
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
