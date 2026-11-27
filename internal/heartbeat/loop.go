package heartbeat

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/installer"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// HeartbeatClient is the subset of the generated AgentAPI client used by Loop.
// Tests inject fakes; production uses *agentapi.ClientWithResponses.
type HeartbeatClient interface {
	PostHeartbeatWithResponse(ctx context.Context, body agentapi.PostHeartbeatJSONRequestBody, reqEditors ...agentapi.RequestEditorFn) (*agentapi.PostHeartbeatResponse, error)
}

// ClientFactory builds a HeartbeatClient for a validated backend URL.
type ClientFactory func(backendURL string) (HeartbeatClient, error)

// Loop is the service.Agent that posts AgentAPI heartbeats independently of
// the updater. Backend outages are logged and never stop the service.
type Loop struct {
	Version    string
	ConfigPath string

	// Optional: override loaded config (tests).
	Config *config.Config

	Log   Logger
	Clock Clock
	Host  HostInfo

	// HTTPClient is used by the default ClientFactory; nil uses newHTTPClient().
	HTTPClient *http.Client

	// NewClient overrides client construction (tests).
	NewClient ClientFactory

	// AppliedPolicyVersion is reported in heartbeats (0 until policy apply exists).
	AppliedPolicyVersion int32
}

// New returns a production Loop wired to default paths.
func New() *Loop {
	paths := installer.DefaultPaths()
	return &Loop{
		Version:    buildinfo.Version,
		ConfigPath: paths.ConfigPath,
		Log:        WriterLogger{W: os.Stderr},
		Clock:      RealClock{},
		Host:       DefaultHost{},
	}
}

// Run implements service.Agent: heartbeat until ctx is cancelled.
// Empty backend_url is a quiet no-op (AC #1). Failures never return except on cancel.
func (l *Loop) Run(ctx context.Context) error {
	if l.Log == nil {
		l.Log = NopLogger{}
	}
	if l.Clock == nil {
		l.Clock = RealClock{}
	}
	if l.Host == nil {
		l.Host = DefaultHost{}
	}
	if l.Version == "" {
		l.Version = buildinfo.Version
	}
	if l.ConfigPath == "" {
		l.ConfigPath = installer.DefaultPaths().ConfigPath
	}
	if l.NewClient == nil {
		l.NewClient = l.defaultNewClient
	}

	cfg, err := l.loadConfig()
	if err != nil {
		// Config load failure: log and idle until cancel so we never take down
		// a co-located updater loop. Missing identity is already fatal for the
		// updater; here we stay fail-soft for heartbeat-only issues.
		l.Log.Log("heartbeat: load config failed (idle until stop): %v", err)
		return l.idle(ctx)
	}

	backend := strings.TrimSpace(cfg.BackendURL)
	if backend == "" {
		l.Log.Log("heartbeat: backend_url unset; heartbeat disabled")
		return l.idle(ctx)
	}

	interval := time.Duration(cfg.HeartbeatIntervalSeconds) * time.Second
	// Never log raw token — Config.String redacts it.
	l.Log.Log("heartbeat: loop starting (url=%s interval=%s version=%s machine_id=%s)",
		backend, interval, l.Version, cfg.MachineID)

	client, err := l.NewClient(backend)
	if err != nil {
		l.Log.Log("heartbeat: client init failed (idle until stop): %s", redactErr(err, cfg.Token))
		return l.idle(ctx)
	}

	l.safeBeat(ctx, client, cfg)

	for {
		timer := l.Clock.After(interval)
		select {
		case <-ctx.Done():
			l.Log.Log("heartbeat: loop stopping")
			return ctx.Err()
		case <-timer:
			next, loadErr := l.loadConfig()
			if loadErr != nil {
				l.Log.Log("heartbeat: reload config failed (continuing with previous): %v", loadErr)
			} else {
				cfg = next
				interval = time.Duration(cfg.HeartbeatIntervalSeconds) * time.Second
				backend = strings.TrimSpace(cfg.BackendURL)
				if backend == "" {
					l.Log.Log("heartbeat: backend_url cleared; pausing heartbeats")
					continue
				}
				rebuilt, clientErr := l.NewClient(backend)
				if clientErr != nil {
					l.Log.Log("heartbeat: client rebuild failed (continuing): %s", redactErr(clientErr, cfg.Token))
					continue
				}
				client = rebuilt
			}
			if strings.TrimSpace(cfg.BackendURL) == "" {
				continue
			}
			l.safeBeat(ctx, client, cfg)
		}
	}
}

func (l *Loop) idle(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (l *Loop) loadConfig() (config.Config, error) {
	if l.Config != nil {
		if err := l.Config.Validate(); err != nil {
			return config.Config{}, err
		}
		return *l.Config, nil
	}
	return config.LoadFile(l.ConfigPath)
}

func (l *Loop) defaultNewClient(backendURL string) (HeartbeatClient, error) {
	if err := config.ValidateBackendURL(backendURL); err != nil {
		return nil, err
	}
	httpClient := l.HTTPClient
	if httpClient == nil {
		httpClient = newHTTPClient()
	}
	return newAgentClient(backendURL, httpClient)
}

func (l *Loop) safeBeat(ctx context.Context, client HeartbeatClient, cfg config.Config) {
	if err := ctx.Err(); err != nil {
		return
	}
	if err := l.beatOnce(ctx, client, cfg); err != nil {
		l.Log.Log("heartbeat: beat failed (continuing): %s", redactErr(err, cfg.Token))
	}
}

func (l *Loop) beatOnce(ctx context.Context, client HeartbeatClient, cfg config.Config) error {
	if strings.TrimSpace(cfg.BackendURL) == "" {
		return nil
	}
	var machineID openapi_types.UUID
	if err := machineID.UnmarshalText([]byte(cfg.MachineID)); err != nil {
		return fmt.Errorf("machine_id: %w", err)
	}
	hostname, err := l.Host.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	body := agentapi.PostHeartbeatJSONRequestBody{
		MachineId:            machineID,
		AgentVersion:         l.Version,
		Hostname:             hostname,
		OsVersion:            l.Host.OSVersion(),
		AppliedPolicyVersion: l.AppliedPolicyVersion,
	}
	resp, err := client.PostHeartbeatWithResponse(ctx, body, bearerEditor(cfg.Token))
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode())
	}
	l.Log.Log("heartbeat: ok (machine_id=%s version=%s)", cfg.MachineID, l.Version)
	return nil
}
