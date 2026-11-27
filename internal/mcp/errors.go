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
	Code    string
	Message string
	Status  int
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

func mapHTTPStatus(status int, body []byte, knownSecrets ...string) error {
	code, message := "upstream_error", "upstream request failed"
	var er controlapi.ErrorResponse
	if len(body) > 0 && json.Unmarshal(body, &er) == nil {
		if er.Code != "" {
			code = er.Code
		}
		if er.Message != "" {
			message = er.Message
		}
	}
	switch status {
	case http.StatusBadRequest:
		code = firstNonEmpty(code, "bad_request")
	case http.StatusUnauthorized:
		code = "unauthorized"
		message = "missing or invalid credentials"
	case http.StatusForbidden:
		code = firstNonEmpty(code, "forbidden")
	case http.StatusNotFound:
		code = firstNonEmpty(code, "not_found")
	case http.StatusConflict:
		code = firstNonEmpty(code, "conflict")
	case http.StatusRequestEntityTooLarge:
		code = firstNonEmpty(code, "payload_too_large")
	case http.StatusTooManyRequests:
		code = "rate_limited"
		message = firstNonEmpty(er.Message, "too many requests")
	case http.StatusServiceUnavailable:
		code = firstNonEmpty(code, "unavailable")
	}
	return &ToolError{
		Code:    code,
		Message: RedactSecrets(message, knownSecrets...),
		Status:  status,
	}
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
