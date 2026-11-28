package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultHasReleaseDefaults(t *testing.T) {
	t.Parallel()
	c := Default()
	if c.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", c.SchemaVersion, CurrentSchemaVersion)
	}
	if c.PollIntervalSeconds != DefaultPollIntervalSeconds {
		t.Fatalf("PollIntervalSeconds = %d, want %d", c.PollIntervalSeconds, DefaultPollIntervalSeconds)
	}
	if c.HeartbeatIntervalSeconds != DefaultHeartbeatIntervalSeconds {
		t.Fatalf("HeartbeatIntervalSeconds = %d, want %d", c.HeartbeatIntervalSeconds, DefaultHeartbeatIntervalSeconds)
	}
	if c.SampleIntervalSeconds != DefaultSampleIntervalSeconds {
		t.Fatalf("SampleIntervalSeconds = %d, want %d", c.SampleIntervalSeconds, DefaultSampleIntervalSeconds)
	}
	if c.BackendURL != "" {
		t.Fatalf("BackendURL = %q, want empty", c.BackendURL)
	}
	if c.ReleaseAPIURL == "" {
		t.Fatal("ReleaseAPIURL empty")
	}
	// Identity is filled at install time, not by Default().
	if c.MachineID != "" || c.Token != "" {
		t.Fatalf("Default must leave identity empty, got %+v", c)
	}
	if err := c.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Default().Validate() = %v, want ErrInvalid (missing identity)", err)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	t.Parallel()
	base := WithTestIdentity(Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, SampleIntervalSeconds: 60})
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "schema_zero", cfg: Config{SchemaVersion: 0, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, MachineID: base.MachineID, Token: base.Token, SampleIntervalSeconds: 60}},
		{name: "empty_url", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "  ", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, MachineID: base.MachineID, Token: base.Token, SampleIntervalSeconds: 60}},
		{name: "poll_zero", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 0, HeartbeatIntervalSeconds: 1, MachineID: base.MachineID, Token: base.Token, SampleIntervalSeconds: 60}},
		{name: "poll_neg", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: -5, HeartbeatIntervalSeconds: 1, MachineID: base.MachineID, Token: base.Token, SampleIntervalSeconds: 60}},
		{name: "heartbeat_zero", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 0, SampleIntervalSeconds: 60, MachineID: base.MachineID, Token: base.Token}},
		{name: "sample_low", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, SampleIntervalSeconds: 14, MachineID: base.MachineID, Token: base.Token}},
		{name: "sample_high", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, SampleIntervalSeconds: 301, MachineID: base.MachineID, Token: base.Token}},
		{name: "bad_backend_url", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, BackendURL: "http://example.com", MachineID: base.MachineID, Token: base.Token, SampleIntervalSeconds: 60}},
		{name: "missing_machine_id", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, Token: base.Token, SampleIntervalSeconds: 60}},
		{name: "missing_token", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, MachineID: base.MachineID, SampleIntervalSeconds: 60}},
		{name: "bad_machine_id", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, HeartbeatIntervalSeconds: 1, MachineID: "nope", Token: base.Token, SampleIntervalSeconds: 60}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.Validate()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRoundTripJSON(t *testing.T) {
	t.Parallel()
	c := WithTestIdentity(Default())
	c.ReleaseAPIURL = "http://127.0.0.1:9/mock"
	c.PollIntervalSeconds = 2
	c.BackendURL = "https://api.example.com"
	c.HeartbeatIntervalSeconds = 30

	raw, err := MarshalJSONBytes(c)
	if err != nil {
		t.Fatalf("MarshalJSONBytes: %v", err)
	}
	for _, key := range []string{`"schema_version"`, `"machine_id"`, `"token"`, `"backend_url"`, `"heartbeat_interval_seconds"`, `"sample_interval_seconds"`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}

	got, err := Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != c {
		t.Fatalf("got %+v, want %+v", got, c)
	}
}

func TestParseBackfillsHeartbeatInterval(t *testing.T) {
	t.Parallel()
	got, err := Parse(strings.NewReader(`{"schema_version":1,"release_api_url":"http://x","poll_interval_seconds":2,"machine_id":"550e8400-e29b-41d4-a716-446655440000","token":"ab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.HeartbeatIntervalSeconds != DefaultHeartbeatIntervalSeconds {
		t.Fatalf("HeartbeatIntervalSeconds = %d, want default %d", got.HeartbeatIntervalSeconds, DefaultHeartbeatIntervalSeconds)
	}
	if got.BackendURL != "" {
		t.Fatalf("BackendURL = %q", got.BackendURL)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`{"schema_version":1,"release_api_url":"http://x","poll_interval_seconds":1,"machine_id":"550e8400-e29b-41d4-a716-446655440000","token":"ab","extra":true}`))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestEncodeWritesValidatedJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := Encode(&buf, WithTestIdentity(Default())); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "{") {
		t.Fatalf("unexpected output: %q", buf.String())
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := MarshalJSONBytes(WithTestIdentity(Config{
		SchemaVersion:         1,
		ReleaseAPIURL:         "http://127.0.0.1:9/mock",
		PollIntervalSeconds:   2,
		SampleIntervalSeconds: 60,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.PollIntervalSeconds != 2 || got.ReleaseAPIURL != "http://127.0.0.1:9/mock" {
		t.Fatalf("got %+v", got)
	}
	if got.MachineID == "" || got.Token == "" {
		t.Fatalf("identity missing: %+v", got)
	}
	if _, err := LoadFile(dir + "/missing.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestConfigStringRedactsToken(t *testing.T) {
	t.Parallel()
	c := WithTestIdentity(Default())
	c.Token = "super-secret-token-value-do-not-leak"
	s := c.String()
	if strings.Contains(s, "super-secret") {
		t.Fatalf("token leaked in String(): %s", s)
	}
	if !strings.Contains(s, RedactedToken) {
		t.Fatalf("expected redaction marker in %s", s)
	}
	// fmt verbs that use Stringer must also redact.
	for _, format := range []string{"%s", "%v", "%+v"} {
		out := fmt.Sprintf(format, c)
		if strings.Contains(out, "super-secret") {
			t.Fatalf("token leaked via %s: %s", format, out)
		}
	}
}

func TestParseAllowsMissingIdentity(t *testing.T) {
	t.Parallel()
	got, err := Parse(strings.NewReader(`{"schema_version":1,"release_api_url":"http://x","poll_interval_seconds":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReleaseAPIURL != "http://x" || got.MachineID != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseBackfillsSampleInterval(t *testing.T) {
	t.Parallel()
	got, err := Parse(strings.NewReader(`{"schema_version":1,"release_api_url":"http://x","poll_interval_seconds":2,"machine_id":"550e8400-e29b-41d4-a716-446655440000","token":"ab"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.SampleIntervalSeconds != DefaultSampleIntervalSeconds {
		t.Fatalf("SampleIntervalSeconds = %d, want default %d", got.SampleIntervalSeconds, DefaultSampleIntervalSeconds)
	}
}

func TestValidateSampleIntervalBounds(t *testing.T) {
	t.Parallel()
	for _, n := range []int{15, 60, 300} {
		if err := ValidateSampleInterval(n); err != nil {
			t.Fatalf("%d: %v", n, err)
		}
	}
	for _, n := range []int{0, 14, 301, -1} {
		if err := ValidateSampleInterval(n); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%d: %v", n, err)
		}
	}
}
