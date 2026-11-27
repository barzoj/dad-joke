package updater

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateDownloadURL enforces HTTPS by default. HTTP is allowed only for
// loopback hosts (127.0.0.1 / localhost) so local E2E mock servers work.
func ValidateDownloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Host == "" || u.Scheme == "" {
		return fmt.Errorf("url missing scheme or host: %q", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if isLoopbackHost(host) {
			return nil
		}
		return fmt.Errorf("http download url host must be loopback, got %q", host)
	default:
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
