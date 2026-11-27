package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchLatestHeadersAndParse(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") != "DadJoke/0.0.1-dev" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		if r.Header.Get("X-Agent-Version") != "0.0.1-dev" {
			t.Errorf("X-Agent-Version = %q", r.Header.Get("X-Agent-Version"))
		}
		// AC #3: raw agent token must never be sent to GitHub Releases.
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("Authorization must be absent on release polls, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name":"v0.0.2",
			"assets":[
				{"name":"dad-joke-windows-amd64.exe","browser_download_url":"http://127.0.0.1/a.exe"},
				{"name":"dad-joke-windows-amd64.exe.sha256","browser_download_url":"http://127.0.0.1/a.sha256"}
			]
		}`))
	}))
	defer srv.Close()

	c := NewHTTPReleaseClient()
	c.HTTPClient = srv.Client()
	meta, err := c.FetchLatest(context.Background(), srv.URL, "v0.0.1-dev")
	if err != nil {
		t.Fatal(err)
	}
	if meta.TagName != "v0.0.2" {
		t.Fatalf("tag = %q", meta.TagName)
	}
	if len(meta.Assets) != 2 {
		t.Fatalf("assets = %d", len(meta.Assets))
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d", hits.Load())
	}
}

func TestFetchLatestErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewHTTPReleaseClient()
	c.HTTPClient = srv.Client()
	_, err := c.FetchLatest(context.Background(), srv.URL, "0.0.1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("err = %v", err)
	}

	_, err = c.FetchLatest(context.Background(), "http://example.com/x", "0.0.1")
	if err == nil {
		t.Fatal("expected non-loopback http rejection")
	}
}

func TestFetchLatestRejectsOversizedJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, MaxReleaseJSONBytes+10))
	}))
	defer srv.Close()

	c := NewHTTPReleaseClient()
	c.HTTPClient = srv.Client()
	_, err := c.FetchLatest(context.Background(), srv.URL, "0.0.1")
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadAndFindAsset(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agent-Version") != "0.0.2" {
			t.Errorf("version header = %q", r.Header.Get("X-Agent-Version"))
		}
		_, _ = w.Write([]byte("bin"))
	}))
	defer srv.Close()

	c := NewHTTPReleaseClient()
	c.HTTPClient = srv.Client()
	b, err := c.Download(context.Background(), srv.URL, "0.0.2", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "bin" {
		t.Fatalf("body = %q", b)
	}

	meta := ReleaseMeta{TagName: "v1", Assets: []AssetMeta{{Name: AssetEXE, BrowserDownloadURL: srv.URL}}}
	a, err := FindAsset(meta, AssetEXE)
	if err != nil || a.BrowserDownloadURL != srv.URL {
		t.Fatalf("FindAsset = %v %v", a, err)
	}
	if _, err := FindAsset(meta, AssetSHA256); err == nil {
		t.Fatal("expected missing asset")
	}
}

func TestDownloadContextCancel(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := NewHTTPReleaseClient()
	c.HTTPClient = srv.Client()
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Download(ctx, srv.URL, "0.0.1", 1024)
		errCh <- err
	}()
	<-started
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected cancel error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download did not return after cancel")
	}
}
