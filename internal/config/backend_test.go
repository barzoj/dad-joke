package config

import (
	"errors"
	"testing"
)

func TestValidateBackendURL(t *testing.T) {
	t.Parallel()
	ok := []string{
		"",
		"   ",
		"https://api.example.com",
		"https://api.example.com/v1",
		"http://127.0.0.1:8080",
		"http://localhost:9/",
		"http://[::1]:8443",
	}
	for _, u := range ok {
		if err := ValidateBackendURL(u); err != nil {
			t.Fatalf("ValidateBackendURL(%q) = %v", u, err)
		}
	}
	bad := []string{
		"http://example.com",
		"http://192.168.1.1/",
		"ftp://127.0.0.1/",
		"file:///C:/x",
		"://nope",
		"https://",
		"https://user:pass@example.com/",
		"http://user@127.0.0.1/",
		"not-a-url",
	}
	for _, u := range bad {
		err := ValidateBackendURL(u)
		if err == nil {
			t.Fatalf("ValidateBackendURL(%q) expected error", u)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("ValidateBackendURL(%q) = %v, want ErrInvalid", u, err)
		}
	}
}
