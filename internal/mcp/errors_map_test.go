package mcp

import (
	"net/http"
	"testing"
)

func TestMapHTTPStatus_UsesStatusFallbackWhenBodyLacksCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusNotFound, `{"message":"missing","request_id":"r1"}`, "not_found"},
		{http.StatusConflict, `{"message":"busy","request_id":"r1"}`, "conflict"},
		{http.StatusTooManyRequests, `{"message":"slow down","request_id":"r1"}`, "rate_limited"},
		{http.StatusBadRequest, `{"message":"nope","request_id":"r1"}`, "bad_request"},
		{http.StatusRequestEntityTooLarge, `{"message":"big","request_id":"r1"}`, "payload_too_large"},
		{http.StatusNotFound, `{"code":"custom_missing","message":"x","request_id":"r1"}`, "custom_missing"},
	}
	for _, tc := range cases {
		err := mapHTTPStatus(tc.status, []byte(tc.body), nil)
		te, ok := err.(*ToolError)
		if !ok {
			t.Fatalf("status %d: type %T", tc.status, err)
		}
		if te.Code != tc.want {
			t.Fatalf("status %d: code=%q want %q", tc.status, te.Code, tc.want)
		}
		if te.Status != tc.status {
			t.Fatalf("status field=%d want %d", te.Status, tc.status)
		}
	}
}

func TestMapHTTPStatus_RetryAfterPreserved(t *testing.T) {
	t.Parallel()
	hdr := http.Header{}
	hdr.Set("Retry-After", "42")
	err := mapHTTPStatus(http.StatusTooManyRequests, []byte(`{"message":"too many requests","request_id":"r"}`), hdr)
	te := err.(*ToolError)
	if te.RetryAfter != "42" {
		t.Fatalf("RetryAfter=%q", te.RetryAfter)
	}
	if te.Code != "rate_limited" {
		t.Fatalf("code=%q", te.Code)
	}
}
