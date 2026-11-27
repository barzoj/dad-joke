package jobs

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/config"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	maxHTTPRedirects   = 10
)

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
