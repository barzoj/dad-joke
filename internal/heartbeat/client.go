package heartbeat

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	maxHTTPRedirects   = 10
)

// newHTTPClient returns a Zero-Trust HTTP client for AgentAPI:
// system CA verification, TLS 1.2+, and redirects that never leak Authorization
// to a different host.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultHTTPTimeout,
		CheckRedirect: checkRedirect,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
	}
}

// checkRedirect re-validates every redirect target and strips Authorization
// when the host or scheme changes so Bearer credentials never follow a
// cross-origin hop (including http→https on the same host).
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxHTTPRedirects {
		return fmt.Errorf("stopped after %d redirects", maxHTTPRedirects)
	}
	if req.URL == nil {
		return fmt.Errorf("redirect missing url")
	}
	if err := config.ValidateBackendURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect blocked: %w", err)
	}
	if len(via) > 0 && via[0].URL != nil {
		if !sameOrigin(via[0].URL, req.URL) {
			req.Header.Del("Authorization")
		}
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.Scheme), strings.TrimSpace(b.Scheme)) &&
		strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host))
}

// bearerEditor attaches Authorization: Bearer <token> without logging the secret.
func bearerEditor(token string) agentapi.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if token == "" {
			return fmt.Errorf("agent token is empty")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// newAgentClient builds a generated AgentAPI client against backendURL.
func newAgentClient(backendURL string, httpClient *http.Client) (*agentapi.ClientWithResponses, error) {
	if httpClient == nil {
		httpClient = newHTTPClient()
	}
	return agentapi.NewClientWithResponses(backendURL, agentapi.WithHTTPClient(httpClient))
}
