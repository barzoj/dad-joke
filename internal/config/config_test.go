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
	base := WithTestIdentity(Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1})
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "schema_zero", cfg: Config{SchemaVersion: 0, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, MachineID: base.MachineID, Token: base.Token}},
		{name: "empty_url", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "  ", PollIntervalSeconds: 1, MachineID: base.MachineID, Token: base.Token}},
		{name: "poll_zero", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 0, MachineID: base.MachineID, Token: base.Token}},
		{name: "poll_neg", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: -5, MachineID: base.MachineID, Token: base.Token}},
		{name: "missing_machine_id", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, Token: base.Token}},
		{name: "missing_token", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, MachineID: base.MachineID}},
		{name: "bad_machine_id", cfg: Config{SchemaVersion: 1, ReleaseAPIURL: "http://x", PollIntervalSeconds: 1, MachineID: "nope", Token: base.Token}},
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

	raw, err := MarshalJSONBytes(c)
	if err != nil {
		t.Fatalf("MarshalJSONBytes: %v", err)
	}
	for _, key := range []string{`"schema_version"`, `"machine_id"`, `"token"`} {
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
		SchemaVersion:       1,
		ReleaseAPIURL:       "http://127.0.0.1:9/mock",
		PollIntervalSeconds: 2,
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
