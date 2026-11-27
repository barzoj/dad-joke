package heartbeat

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
)

func TestNopAndWriterLogger(t *testing.T) {
	t.Parallel()
	NopLogger{}.Log("ignored %s", "x")
	var buf bytes.Buffer
	WriterLogger{W: &buf}.Log("hello %s", "world")
	if !strings.Contains(buf.String(), "hello world") {
		t.Fatalf("buf = %q", buf.String())
	}
	WriterLogger{}.Log("nil writer")
}

func TestRealClockAndDefaultHost(t *testing.T) {
	t.Parallel()
	c := RealClock{}
	now := c.Now()
	if now.IsZero() {
		t.Fatal("zero time")
	}
	select {
	case <-c.After(1 * time.Millisecond):
	case <-time.After(2 * time.Second):
		t.Fatal("After timeout")
	}
	h := DefaultHost{}
	if _, err := h.Hostname(); err != nil {
		t.Fatal(err)
	}
	if h.OSVersion() == "" {
		t.Fatal("empty OSVersion")
	}
}

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	loop := New()
	if loop.ConfigPath == "" || loop.Version == "" {
		t.Fatalf("%+v", loop)
	}
}

func TestRedactErrNilAndEmpty(t *testing.T) {
	t.Parallel()
	if redactErr(nil, "x") != "" {
		t.Fatal("expected empty")
	}
	if redactSecret("", "x") != "" {
		t.Fatal("expected empty")
	}
	if redactSecret("ok", "") != "ok" {
		t.Fatal("expected passthrough")
	}
}

func TestBearerEditorEmptyToken(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/v1/heartbeat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bearerEditor("")(context.Background(), req); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckRedirectMaxAndMissingURL(t *testing.T) {
	t.Parallel()
	via := make([]*http.Request, maxHTTPRedirects)
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err := checkRedirect(req, via); err == nil {
		t.Fatal("expected max redirects error")
	}
	bad := &http.Request{}
	if err := checkRedirect(bad, nil); err == nil {
		t.Fatal("expected missing url error")
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := testCfg("http://127.0.0.1:9", 12)
	raw, err := config.MarshalJSONBytes(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loop := &Loop{ConfigPath: path}
	got, err := loop.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.BackendURL != cfg.BackendURL || got.HeartbeatIntervalSeconds != 12 {
		t.Fatalf("got %+v", got)
	}
}
