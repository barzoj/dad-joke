// Package config defines the versioned on-disk agent configuration contract
// stored at C:\ProgramData\DadJoke\config.json.
//
// Schema fields include release polling settings (Issue #3), persistent
// machine identity (Issue #8), and optional AgentAPI heartbeat settings
// (Issue #10):
//
//   - machine_id: RFC 4122 UUIDv4, generated once on first install and
//     preserved across reinstalls and self-updates.
//   - token: secret credential. Auto-generated values are 32 cryptographically
//     random bytes encoded as 64-char lowercase hex. Explicit install --token
//     values are stored verbatim (no truncation or re-encoding).
//   - backend_url: optional AgentAPI base URL (empty = local-only, no heartbeat).
//   - heartbeat_interval_seconds: heartbeat period when backend_url is set
//     (default 60); preserved across reinstalls/self-updates.
//
// The raw token must never appear in service logs, debug output, or outbound
// HTTP headers (e.g. GitHub Releases polls). Config.String redacts it.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/barzoj/dad-joke/internal/buildinfo"
)

// CurrentSchemaVersion is the config schema version written by this release.
const CurrentSchemaVersion = 1

// DefaultPollIntervalSeconds is the default GitHub Releases poll interval (Issue #3).
const DefaultPollIntervalSeconds = 60

// DefaultHeartbeatIntervalSeconds is the default AgentAPI heartbeat interval (Issue #10).
const DefaultHeartbeatIntervalSeconds = 60

// ErrInvalid is returned when config fails validation.
var ErrInvalid = errors.New("invalid config")

// Config is the versioned JSON schema for ProgramData\DadJoke\config.json.
type Config struct {
	SchemaVersion            int    `json:"schema_version"`
	ReleaseAPIURL            string `json:"release_api_url"`
	PollIntervalSeconds      int    `json:"poll_interval_seconds"`
	MachineID                string `json:"machine_id"`
	Token                    string `json:"token"`
	BackendURL               string `json:"backend_url"`
	HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds"`
}

// Default returns a new Config populated with compile-time / product defaults.
// MachineID and Token are left empty; callers that persist config must populate
// them (see NewMachineID / NewToken) before Validate / MarshalJSONBytes.
// BackendURL defaults to empty (no heartbeat).
func Default() Config {
	return Config{
		SchemaVersion:            CurrentSchemaVersion,
		ReleaseAPIURL:            buildinfo.ReleaseAPIURL,
		PollIntervalSeconds:      DefaultPollIntervalSeconds,
		HeartbeatIntervalSeconds: DefaultHeartbeatIntervalSeconds,
	}
}

// applyDefaults fills zero optional fields so older on-disk documents remain valid.
func (c *Config) applyDefaults() {
	if c.HeartbeatIntervalSeconds < 1 {
		c.HeartbeatIntervalSeconds = DefaultHeartbeatIntervalSeconds
	}
}

// String renders Config for logs without leaking Token.
func (c Config) String() string {
	tok := ""
	if c.Token != "" {
		tok = RedactedToken
	}
	return fmt.Sprintf("Config{SchemaVersion:%d ReleaseAPIURL:%q PollIntervalSeconds:%d MachineID:%q Token:%s BackendURL:%q HeartbeatIntervalSeconds:%d}",
		c.SchemaVersion, c.ReleaseAPIURL, c.PollIntervalSeconds, c.MachineID, tok, c.BackendURL, c.HeartbeatIntervalSeconds)
}

// Validate checks required fields and ranges, including machine identity and backend_url.
func (c Config) Validate() error {
	if c.SchemaVersion < 1 {
		return fmt.Errorf("%w: schema_version must be >= 1 (got %d)", ErrInvalid, c.SchemaVersion)
	}
	if strings.TrimSpace(c.ReleaseAPIURL) == "" {
		return fmt.Errorf("%w: release_api_url is required", ErrInvalid)
	}
	if c.PollIntervalSeconds < 1 {
		return fmt.Errorf("%w: poll_interval_seconds must be >= 1 (got %d)", ErrInvalid, c.PollIntervalSeconds)
	}
	if c.HeartbeatIntervalSeconds < 1 {
		return fmt.Errorf("%w: heartbeat_interval_seconds must be >= 1 (got %d)", ErrInvalid, c.HeartbeatIntervalSeconds)
	}
	if err := ValidateBackendURL(c.BackendURL); err != nil {
		return err
	}
	if err := ValidateMachineID(c.MachineID); err != nil {
		return err
	}
	if err := ValidateTokenPresence(c.Token); err != nil {
		return err
	}
	return nil
}

// Encode writes pretty-printed JSON suitable for config.json.
func Encode(w io.Writer, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

// Decode reads JSON into Config and validates it.
func Decode(r io.Reader) (Config, error) {
	c, err := Parse(r)
	if err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Parse reads JSON into Config without validating required fields.
// Used by install reconcile to backfill missing machine_id / token.
// Applies defaults for newly introduced optional fields (e.g. heartbeat interval).
func Parse(r io.Reader) (Config, error) {
	var c Config
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	c.applyDefaults()
	return c, nil
}

// MarshalJSONBytes returns the canonical on-disk JSON bytes for c.
func MarshalJSONBytes(c Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// LoadFile reads and validates config.json from path.
func LoadFile(path string) (Config, error) {
	path = filepath.Clean(path)
	if path == "." || path == "" {
		return Config{}, fmt.Errorf("%w: empty config path", ErrInvalid)
	}
	// #nosec G304 -- path is filepath.Clean'd; production callers use ProgramData\DadJoke\config.json
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	return Decode(f)
}

// WithTestIdentity returns c with a fixed valid machine_id and token for tests.
// Not for production use. Also backfills heartbeat_interval_seconds when unset.
func WithTestIdentity(c Config) Config {
	c.MachineID = "550e8400-e29b-41d4-a716-446655440000"
	c.Token = strings.Repeat("ab", TokenByteLen) // 64 hex chars
	c.applyDefaults()
	return c
}
