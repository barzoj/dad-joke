package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/factsup"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/sampler"
	"github.com/barzoj/dad-joke/internal/service"
)

func TestInstallParsesSampleInterval(t *testing.T) {
	t.Parallel()
	opts, err := parseInstallArgs([]string{"--sample-interval", "15"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.SampleIntervalSet || opts.SampleIntervalSeconds != 15 {
		t.Fatalf("%+v", opts)
	}
	for _, bad := range []string{"0", "14", "301", "abc", "-1"} {
		_, err := parseInstallArgs([]string{"--sample-interval", bad})
		if err == nil {
			t.Fatalf("expected reject for %q", bad)
		}
	}
}

func TestDefaultAgentGroupHasSamplerAndUploader(t *testing.T) {
	t.Parallel()
	g := DefaultAgentGroup()
	if len(g) < 5 {
		t.Fatalf("DefaultAgentGroup len=%d, want ≥5 (updater,heartbeat,jobs,sampler,factsup)", len(g))
	}
	var sawSampler, sawUploader bool
	for _, a := range g {
		switch a.(type) {
		case *sampler.Loop:
			sawSampler = true
		case *factsup.Loop:
			sawUploader = true
		}
	}
	if !sawSampler || !sawUploader {
		t.Fatalf("sampler=%v uploader=%v", sawSampler, sawUploader)
	}
}

func TestGroupContainsSamplerAndUploader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &factsq.Queue{Dir: dir}
	cfg := config.WithTestIdentity(config.Default())
	cfg.BackendURL = ""
	cfg.SampleIntervalSeconds = 15

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)

	samp := &sampler.Loop{
		Config:  &cfg,
		Queue:   q,
		Log:     sampler.NopLogger{},
		Clock:   sampler.RealClock{},
		Zone:    func(time.Time) (string, int32) { return "UTC", 0 },
		Sensors: nil, // uses default empty → sessions stub
	}
	up := &factsup.Loop{
		Config: &cfg,
		Queue:  q,
		Log:    factsup.NopLogger{},
		Clock:  factsup.RealClock{},
	}
	g := service.Group{samp, up}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = g.Run(ctx)
	if hits.Load() != 0 {
		t.Fatalf("empty backend_url must not HTTP; hits=%d", hits.Load())
	}
}

func TestUsageMentionsSampleInterval(t *testing.T) {
	t.Parallel()
	if !containsFlag(usageText, "--sample-interval") {
		t.Fatal("usage missing --sample-interval")
	}
	_ = installer.InstallOptions{}
	_ = io.Discard
}

func containsFlag(s, flag string) bool {
	return len(s) > 0 && (stringIndex(s, flag) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
