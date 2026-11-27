package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateBackendURL enforces Zero-Trust transport rules for AgentAPI base URLs.
//
// Empty is valid (local-only / no heartbeat). Non-empty values must:
//   - parse as an absolute URL with scheme and host
//   - not include userinfo (credentials in the URL)
//   - use https for any non-loopback host; http is allowed only for loopback
//     (127.0.0.1, ::1, localhost)
func ValidateBackendURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: backend_url: parse: %v", ErrInvalid, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%w: backend_url missing scheme or host", ErrInvalid)
	}
	if u.User != nil {
		return fmt.Errorf("%w: backend_url must not contain userinfo", ErrInvalid)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if isLoopbackHost(host) {
			return nil
		}
		return fmt.Errorf("%w: backend_url http host must be loopback, got %q", ErrInvalid, host)
	default:
		return fmt.Errorf("%w: backend_url unsupported scheme %q", ErrInvalid, u.Scheme)
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
