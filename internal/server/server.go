// Package server implements the Dad Joke HTTP control-plane server.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/audit"
	djmcp "github.com/barzoj/dad-joke/internal/mcp"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/store"
)

const (
	defaultAddr     = ":8080"
	maxRequestBytes = 1 << 20 // 1 MiB

	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	defaultAuthFailMax  = 30
	defaultMutationMax  = 120
	defaultLoginFailMax = 10
	defaultLimitWindow  = time.Minute
)

// Config configures the HTTP server.
type Config struct {
	Addr  string
	Store store.Store
	Log   *log.Logger

	// Optional rate-limit overrides (nil → defaults). Useful in tests.
	AuthFailureLimiter  *ratelimit.Limiter
	MutationLimiter     *ratelimit.Limiter
	LoginFailureLimiter *ratelimit.Limiter
	Clock               func() time.Time

	// AllowSignup controls POST /v1/auth/register (default true when unset via pointer).
	// Use AllowSignupPtr for explicit false; when nil, signup is allowed.
	AllowSignup *bool
	// CookieSecure sets the Secure flag on session cookies (nil → true).
	CookieSecure *bool

	// FactsRetentionDays bounds how old a fact ts may be (1..365; 0 → default 30).
	// Must match the Mongo facts TTL configured at store open.
	FactsRetentionDays int
}

// Server is a hardened HTTP listener.
type Server struct {
	cfg    Config
	http   *http.Server
	ln     net.Listener
	logger *log.Logger
}

// New builds a Server. Call ListenAndServe or Serve.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("server: store is required")
	}
	addr := cfg.Addr
	if addr == "" {
		addr = defaultAddr
	}
	logger := cfg.Log
	if logger == nil {
		logger = log.Default()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	authFail := cfg.AuthFailureLimiter
	if authFail == nil {
		authFail = ratelimit.New(ratelimit.Config{
			Window:  defaultLimitWindow,
			MaxHits: defaultAuthFailMax,
			Clock:   clock,
		})
	}
	mutations := cfg.MutationLimiter
	if mutations == nil {
		mutations = ratelimit.New(ratelimit.Config{
			Window:  defaultLimitWindow,
			MaxHits: defaultMutationMax,
			Clock:   clock,
		})
	}
	loginFail := cfg.LoginFailureLimiter
	if loginFail == nil {
		loginFail = ratelimit.New(ratelimit.Config{
			Window:  defaultLimitWindow,
			MaxHits: defaultLoginFailMax,
			Clock:   clock,
		})
	}

	allowSignup := true
	if cfg.AllowSignup != nil {
		allowSignup = *cfg.AllowSignup
	}
	cookieSecure := true
	if cfg.CookieSecure != nil {
		cookieSecure = *cfg.CookieSecure
	}

	controlAuthn := NewAuthenticator(AuthConfig{
		Store:          cfg.Store,
		AuthFailures:   authFail,
		Mutations:      mutations,
		Clock:          clock,
		AcceptSessions: true,
	})
	agentAuthn := NewAuthenticator(AuthConfig{
		Store:        cfg.Store,
		AuthFailures: authFail,
		Mutations:    mutations,
		Clock:        clock,
	})

	auditLogger := audit.Logger{Store: cfg.Store.Audits(), Clock: clock}
	control := &ControlAPI{
		Store:         cfg.Store,
		Audit:         auditLogger,
		Clock:         clock,
		AllowSignup:   allowSignup,
		CookieSecure:  cookieSecure,
		LoginFailures: loginFail,
	}
	agent := &AgentAPI{
		Store:              cfg.Store,
		Audit:              auditLogger,
		Clock:              clock,
		FactsRetentionDays: cfg.FactsRetentionDays,
	}

	root := http.NewServeMux()
	s := &Server{cfg: cfg, logger: logger}
	root.HandleFunc("GET /healthz", s.handleHealthz)

	controlHandler := controlapi.HandlerWithOptions(
		controlapi.NewStrictHandlerWithOptions(control, nil, controlapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  writeRequestBindError,
			ResponseErrorHandlerFunc: writeResponseError,
		}),
		controlapi.StdHTTPServerOptions{
			Middlewares: []controlapi.MiddlewareFunc{
				injectClientIP,
				controlAuthn.Middleware(store.TokenKindPAT),
			},
			ErrorHandlerFunc: writeRequestBindError,
		},
	)
	agentHandler := agentapi.HandlerWithOptions(
		agentapi.NewStrictHandlerWithOptions(agent, nil, agentapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  writeRequestBindError,
			ResponseErrorHandlerFunc: writeResponseError,
		}),
		agentapi.StdHTTPServerOptions{
			Middlewares:      []agentapi.MiddlewareFunc{agentAuthn.Middleware(store.TokenKindAgent)},
			ErrorHandlerFunc: writeRequestBindError,
		},
	)

	root.Handle("/v1/heartbeat", agentHandler)
	root.Handle("/v1/policy", agentHandler)
	// Agent event ingest: tighter 256 KiB body limit (413 when exceeded).
	root.Handle("POST /v1/events", limitBody(agentHandler, int64(store.EventBatchMaxBytes)))
	// Agent facts ingest: same 256 KiB body limit (413 when exceeded).
	root.Handle("POST /v1/facts", limitBody(agentHandler, int64(store.FactBatchMaxBytes)))
	// Agent claim/result share the /v1/jobs prefix with ControlAPI get/cancel;
	// method+path patterns keep agent auth on these mutating agent endpoints.
	root.Handle("POST /v1/jobs/{id}/claim", agentHandler)
	root.Handle("POST /v1/jobs/{id}/result", agentHandler)
	root.Handle("/v1/", controlHandler)

	// MCP Streamable HTTP talks only through the generated ControlAPI client
	// via an in-process RoundTripper (no DAL/DB imports in internal/mcp).
	mcpHTTP := &http.Client{Transport: djmcp.HandlerRoundTripper{Handler: controlHandler}}
	mcpClient, err := controlapi.NewClientWithResponses("http://control.invalid", controlapi.WithHTTPClient(mcpHTTP))
	if err != nil {
		return nil, fmt.Errorf("server: mcp control client: %w", err)
	}
	mcpSrv, err := djmcp.New(djmcp.Config{Client: mcpClient, Log: logger})
	if err != nil {
		return nil, fmt.Errorf("server: mcp: %w", err)
	}
	root.Handle("/mcp", mcpSrv.Handler())
	root.Handle("/mcp/", mcpSrv.Handler())

	handler := securityHeaders(limitBody(root, maxRequestBytes))
	s.http = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          logger,
	}
	return s, nil
}

// Handler returns the root HTTP handler. Intended for in-process tests that
// need to set RemoteAddr (e.g. MCP auth rate-limit isolation).
func (s *Server) Handler() http.Handler {
	return s.http.Handler
}

// Addr returns the bound address after Serve/ListenAndServe starts (best-effort).
func (s *Server) Addr() string {
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.http.Addr
}

// ListenAndServe listens on Config.Addr until the context is cancelled or Shutdown.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve serves HTTP on ln until ctx is cancelled, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.ln = ln
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
		err := <-errCh
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Shutdown stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func limitBody(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

type healthOK struct {
	Status string `json:"status"`
}

type healthUnavailable struct {
	Status string `json:"status"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.cfg.Store.Ping(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthUnavailable{Status: "unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(healthOK{Status: "ok"})
}
