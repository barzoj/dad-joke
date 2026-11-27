package apigen_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

func TestAgentAPI_Healthz_EndToEnd(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := agentapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	t.Run("GET_healthz_200_json", func(t *testing.T) {
		t.Parallel()
		resp, err := client.GetHealthzWithResponse(context.Background())
		if err != nil {
			t.Fatalf("GetHealthzWithResponse: %v", err)
		}
		if resp.StatusCode() != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body)
		}
		if ct := resp.HTTPResponse.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		if resp.JSON200 == nil {
			t.Fatalf("JSON200 is nil; body=%s", resp.Body)
		}
		if resp.JSON200.Status != agentapi.Ok {
			t.Fatalf("status field = %q, want %q", resp.JSON200.Status, agentapi.Ok)
		}
		var raw map[string]string
		if err := json.Unmarshal(resp.Body, &raw); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if raw["status"] != "ok" {
			t.Fatalf("body status = %q, want ok", raw["status"])
		}
	})

	t.Run("unsupported_method", func(t *testing.T) {
		t.Parallel()
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/healthz", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("POST /healthz status = %d, want 405; body=%s", res.StatusCode, body)
		}
	})

	t.Run("unknown_path", func(t *testing.T) {
		t.Parallel()
		res, err := srv.Client().Get(srv.URL + "/not-a-route")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("GET /not-a-route status = %d, want 404; body=%s", res.StatusCode, body)
		}
	})
}

func TestControlAPI_Healthz_EndToEnd(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := controlapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	t.Run("GET_healthz_200_json", func(t *testing.T) {
		t.Parallel()
		resp, err := client.GetHealthzWithResponse(context.Background())
		if err != nil {
			t.Fatalf("GetHealthzWithResponse: %v", err)
		}
		if resp.StatusCode() != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body)
		}
		if ct := resp.HTTPResponse.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		if resp.JSON200 == nil {
			t.Fatalf("JSON200 is nil; body=%s", resp.Body)
		}
		if resp.JSON200.Status != controlapi.Ok {
			t.Fatalf("status field = %q, want %q", resp.JSON200.Status, controlapi.Ok)
		}
		var raw map[string]string
		if err := json.Unmarshal(resp.Body, &raw); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if raw["status"] != "ok" {
			t.Fatalf("body status = %q, want ok", raw["status"])
		}
	})

	t.Run("unsupported_method", func(t *testing.T) {
		t.Parallel()
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/healthz", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("POST /healthz status = %d, want 405; body=%s", res.StatusCode, body)
		}
	})

	t.Run("unknown_path", func(t *testing.T) {
		t.Parallel()
		res, err := srv.Client().Get(srv.URL + "/not-a-route")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(res.Body)
			t.Fatalf("GET /not-a-route status = %d, want 404; body=%s", res.StatusCode, body)
		}
	})
}
