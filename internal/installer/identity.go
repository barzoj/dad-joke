package installer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/barzoj/dad-joke/internal/config"
)

// Token status values reported in InstallOutcome.
const (
	TokenStatusGenerated = "generated"
	TokenStatusSupplied  = "supplied"
	TokenStatusPreserved = "preserved"
)

// InstallOptions carries optional install-time inputs (CLI flags).
type InstallOptions struct {
	// TokenSet is true when --token was explicitly passed.
	TokenSet bool
	// Token is the raw --token value when TokenSet is true (persisted verbatim).
	Token string

	// BackendURLSet is true when --backend-url was explicitly passed.
	BackendURLSet bool
	// BackendURL is the AgentAPI base URL when BackendURLSet is true (empty clears).
	BackendURL string

	// HeartbeatIntervalSet is true when --heartbeat-interval was explicitly passed.
	HeartbeatIntervalSet bool
	// HeartbeatIntervalSeconds is the heartbeat period when HeartbeatIntervalSet is true.
	HeartbeatIntervalSeconds int

	// SampleIntervalSet is true when --sample-interval was explicitly passed.
	SampleIntervalSet bool
	// SampleIntervalSeconds is the facts sample period when SampleIntervalSet is true.
	SampleIntervalSeconds int
}

// InstallOutcome is returned after a successful Install so the CLI can print
// machine_id (and a newly generated token once).
type InstallOutcome struct {
	MachineID   string
	Token       string // set only when TokenStatus == TokenStatusGenerated
	TokenStatus string
}

// reconcileConfig builds the desired on-disk config from an optional existing
// document and install options. It never silently rotates a present machine_id
// or token unless --token is explicitly supplied (token only). Without
// --backend-url / --heartbeat-interval / --sample-interval, existing backend_url,
// heartbeat_interval_seconds, and sample_interval_seconds are preserved across reinstalls.
func reconcileConfig(existing *config.Config, opts InstallOptions) (config.Config, InstallOutcome, error) {
	cfg := config.Default()
	if existing != nil {
		cfg = *existing
		if cfg.SchemaVersion < 1 {
			cfg.SchemaVersion = config.CurrentSchemaVersion
		}
		if strings.TrimSpace(cfg.ReleaseAPIURL) == "" {
			cfg.ReleaseAPIURL = config.Default().ReleaseAPIURL
		}
		if cfg.PollIntervalSeconds < 1 {
			cfg.PollIntervalSeconds = config.DefaultPollIntervalSeconds
		}
		if cfg.HeartbeatIntervalSeconds < 1 {
			cfg.HeartbeatIntervalSeconds = config.DefaultHeartbeatIntervalSeconds
		}
		if cfg.SampleIntervalSeconds < 1 {
			cfg.SampleIntervalSeconds = config.DefaultSampleIntervalSeconds
		}
	}

	out := InstallOutcome{}

	if strings.TrimSpace(cfg.MachineID) == "" {
		id, err := config.NewMachineID()
		if err != nil {
			return config.Config{}, out, err
		}
		cfg.MachineID = id
	} else if err := config.ValidateMachineID(cfg.MachineID); err != nil {
		return config.Config{}, out, fmt.Errorf("existing machine_id: %w", err)
	}
	out.MachineID = cfg.MachineID

	switch {
	case opts.TokenSet:
		if err := config.ValidateTokenArg(opts.Token); err != nil {
			return config.Config{}, out, err
		}
		cfg.Token = opts.Token // verbatim; do not trim/mutate
		out.TokenStatus = TokenStatusSupplied
	case strings.TrimSpace(cfg.Token) == "":
		tok, err := config.NewToken()
		if err != nil {
			return config.Config{}, out, err
		}
		cfg.Token = tok
		out.Token = tok
		out.TokenStatus = TokenStatusGenerated
	default:
		out.TokenStatus = TokenStatusPreserved
	}

	if opts.BackendURLSet {
		if err := config.ValidateBackendURL(opts.BackendURL); err != nil {
			return config.Config{}, out, err
		}
		cfg.BackendURL = strings.TrimSpace(opts.BackendURL)
	} else if err := config.ValidateBackendURL(cfg.BackendURL); err != nil {
		return config.Config{}, out, fmt.Errorf("existing backend_url: %w", err)
	}

	if opts.HeartbeatIntervalSet {
		if opts.HeartbeatIntervalSeconds < 1 {
			return config.Config{}, out, fmt.Errorf("%w: heartbeat_interval_seconds must be >= 1 (got %d)", config.ErrInvalid, opts.HeartbeatIntervalSeconds)
		}
		cfg.HeartbeatIntervalSeconds = opts.HeartbeatIntervalSeconds
	}

	if opts.SampleIntervalSet {
		if err := config.ValidateSampleInterval(opts.SampleIntervalSeconds); err != nil {
			return config.Config{}, out, err
		}
		cfg.SampleIntervalSeconds = opts.SampleIntervalSeconds
	}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, out, err
	}
	return cfg, out, nil
}

// ensureConfig loads/creates config.json, applying identity rules. previous is
// the prior file bytes when an existing file was modified (for rollback).
// written is true when the file was created or overwritten.
func (s *Service) ensureConfig(opts InstallOptions) (out InstallOutcome, previous []byte, written bool, err error) {
	exists, err := s.host.FileExists(s.paths.ConfigPath)
	if err != nil {
		return out, nil, false, fmt.Errorf("stat config: %w", err)
	}

	var existing *config.Config
	if exists {
		previous, err = s.host.ReadFile(s.paths.ConfigPath)
		if err != nil {
			return out, nil, false, fmt.Errorf("read config: %w", err)
		}
		parsed, perr := config.Parse(bytes.NewReader(previous))
		if perr != nil {
			return out, nil, false, fmt.Errorf("parse existing config: %w", perr)
		}
		existing = &parsed
	}

	cfg, out, err := reconcileConfig(existing, opts)
	if err != nil {
		return out, nil, false, err
	}

	raw, err := config.MarshalJSONBytes(cfg)
	if err != nil {
		return out, nil, false, err
	}

	if !exists {
		if err := s.host.WriteFile(s.paths.ConfigPath, raw); err != nil {
			return out, nil, false, fmt.Errorf("write config: %w", err)
		}
		s.host.Log("install: wrote config %s (token=%s)", s.paths.ConfigPath, out.TokenStatus)
		return out, nil, true, nil
	}

	if bytes.Equal(previous, raw) {
		s.host.Log("install: preserving existing config %s (token=%s)", s.paths.ConfigPath, out.TokenStatus)
		return out, previous, false, nil
	}

	if err := s.host.WriteFile(s.paths.ConfigPath, raw); err != nil {
		return out, previous, false, fmt.Errorf("update config: %w", err)
	}
	s.host.Log("install: updated config %s (token=%s)", s.paths.ConfigPath, out.TokenStatus)
	return out, previous, true, nil
}
