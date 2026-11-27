package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// apiError is the generated ErrorResponse JSON shape (code/message/request_id).
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func requestID(r *http.Request) string {
	if r == nil {
		return uuid.NewString()
	}
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return uuid.NewString()
}

func writeAPIError(w http.ResponseWriter, status int, code, message, reqID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{
		Code:      code,
		Message:   message,
		RequestID: reqID,
	})
}

func writeUnauthorized(w http.ResponseWriter, reqID string) {
	writeAPIError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token", reqID)
}

func writeServiceUnavailable(w http.ResponseWriter, reqID string) {
	writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "authentication backend unavailable", reqID)
}

func writeTooManyRequests(w http.ResponseWriter, reqID string, retryAfter time.Duration) {
	secs := int(retryAfter / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "too many requests", reqID)
}

// writeRequestBindError maps oapi-codegen request/path bind failures to ErrorResponse JSON.
func writeRequestBindError(w http.ResponseWriter, r *http.Request, err error) {
	msg := "invalid request"
	if err != nil && err.Error() != "" {
		msg = err.Error()
	}
	writeAPIError(w, http.StatusBadRequest, "bad_request", msg, requestID(r))
}
