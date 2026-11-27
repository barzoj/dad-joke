package updater

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func attachTestTransport(c *HTTPReleaseClient, srv *httptest.Server) {
	tc := srv.Client()
	c.HTTPClient.Transport = tc.Transport
	if c.HTTPClient.CheckRedirect == nil {
		c.HTTPClient.CheckRedirect = CheckRedirect
	}
}

func TestRedirectHTTPSToHTTPSAllowed(t *testing.T) {
	t.Parallel()
	final := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.0.2","assets":[]}`))
	}))
	defer final.Close()

	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/releases/latest", http.StatusFound)
	}))
	defer redir.Close()

	c := NewHTTPReleaseClient()
	attachTestTransport(c, redir)
	// Trust both TLS test certs.
	c.HTTPClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only
		Proxy:           http.ProxyFromEnvironment,
	}
	// Still need to reach redir/final hosts — use default transport with insecure TLS
	// and CheckRedirect intact.
	c.HTTPClient.CheckRedirect = CheckRedirect

	meta, err := c.FetchLatest(context.Background(), redir.URL+"/start", "0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.TagName != "v0.0.2" {
		t.Fatalf("tag = %q", meta.TagName)
	}
}

func TestRedirectHTTPSToNonLoopbackHTTPRejected(t *testing.T) {
	t.Parallel()
	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/evil", http.StatusFound)
	}))
	defer redir.Close()

	c := NewHTTPReleaseClient()
	c.HTTPClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only
	}
	c.HTTPClient.CheckRedirect = CheckRedirect

	_, err := c.FetchLatest(context.Background(), redir.URL+"/start", "0.0.1")
	if err == nil {
		t.Fatal("expected redirect rejection")
	}
	if !strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectExcessiveRejected(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Infinite loopback redirect chain.
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	c := NewHTTPReleaseClient()
	attachTestTransport(c, srv)
	c.HTTPClient.CheckRedirect = CheckRedirect

	_, err := c.FetchLatest(context.Background(), srv.URL+"/start", "0.0.1")
	if err == nil {
		t.Fatal("expected too-many-redirects error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d redirects", MaxHTTPRedirects)) &&
		!strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRedirectDirect(t *testing.T) {
	t.Parallel()
	via := make([]*http.Request, MaxHTTPRedirects)
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err := CheckRedirect(req, via); err == nil {
		t.Fatal("expected max redirects error")
	}
	req2, _ := http.NewRequest(http.MethodGet, "http://example.com/x", nil)
	if err := CheckRedirect(req2, nil); err == nil {
		t.Fatal("expected non-loopback http rejection")
	}
	req3, _ := http.NewRequest(http.MethodGet, "https://github.com/x", nil)
	if err := CheckRedirect(req3, nil); err != nil {
		t.Fatal(err)
	}
}
