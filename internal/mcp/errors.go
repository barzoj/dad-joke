package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

// ToolError is a clean, redacted error returned to MCP clients (no stacks).
type ToolError struct {
	Code       string
	Message    string
	Status     int
	RetryAfter string // optional; set when upstream returned Retry-After
}

func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func validationError(msg string) error {
	return &ToolError{Code: "bad_request", Message: RedactSecrets(msg), Status: http.StatusBadRequest}
}

func mapHTTPStatus(status int, body []byte, hdr http.Header, knownSecrets ...string) error {
	var er controlapi.ErrorResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &er)
	}

	fallbackCode := "upstream_error"
	fallbackMsg := "upstream request failed"
	switch status {
	case http.StatusBadRequest:
		fallbackCode = "bad_request"
	case http.StatusUnauthorized:
		fallbackCode = "unauthorized"
		fallbackMsg = "missing or invalid credentials"
	case http.StatusForbidden:
		fallbackCode = "forbidden"
	case http.StatusNotFound:
		fallbackCode = "not_found"
	case http.StatusConflict:
		fallbackCode = "conflict"
	case http.StatusRequestEntityTooLarge:
		fallbackCode = "payload_too_large"
	case http.StatusTooManyRequests:
		fallbackCode = "rate_limited"
		fallbackMsg = "too many requests"
	case http.StatusServiceUnavailable:
		fallbackCode = "unavailable"
	}

	code := firstNonEmpty(er.Code, fallbackCode)
	message := firstNonEmpty(er.Message, fallbackMsg)
	if status == http.StatusUnauthorized {
		// Never echo upstream details that might mention credential material.
		code = "unauthorized"
		message = "missing or invalid credentials"
	}
	if status == http.StatusTooManyRequests {
		code = "rate_limited"
		message = firstNonEmpty(er.Message, "too many requests")
	}

	te := &ToolError{
		Code:    code,
		Message: RedactSecrets(message, knownSecrets...),
		Status:  status,
	}
	if status == http.StatusTooManyRequests && hdr != nil {
		te.RetryAfter = hdr.Get("Retry-After")
	}
	return te
}

func mapClientErr(err error, knownSecrets ...string) error {
	if err == nil {
		return nil
	}
	var te *ToolError
	if errors.As(err, &te) {
		te.Message = RedactSecrets(te.Message, knownSecrets...)
		return te
	}
	msg := RedactSecrets(err.Error(), knownSecrets...)
	// Never leak raw transport URLs that might embed credentials.
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "request failed"
	}
	return &ToolError{Code: "upstream_error", Message: msg, Status: 0}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func requireNonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return validationError(fmt.Sprintf("%s is required", field))
	}
	return nil
}
