package apigen_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

func TestControlAPI_AgentTokens_CreateListDelete(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := controlapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	created, err := client.CreateAgentTokenWithResponse(context.Background(), controlapi.CreateAgentTokenJSONRequestBody{
		Name: "default",
	})
	if err != nil {
		t.Fatalf("CreateAgentTokenWithResponse: %v", err)
	}
	if created.StatusCode() != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", created.StatusCode(), created.Body)
	}
	if created.JSON201 == nil {
		t.Fatalf("JSON201 is nil; body=%s", created.Body)
	}
	if !strings.HasPrefix(created.JSON201.Token, "dj_agt_") {
		t.Fatalf("created token prefix = %q, want dj_agt_", created.JSON201.Token)
	}
	if created.JSON201.Prefix != "dj_agt_" {
		t.Fatalf("prefix = %q", created.JSON201.Prefix)
	}

	listed, err := client.ListAgentTokensWithResponse(context.Background())
	if err != nil {
		t.Fatalf("ListAgentTokensWithResponse: %v", err)
	}
	if listed.StatusCode() != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listed.StatusCode(), listed.Body)
	}
	if listed.JSON200 == nil || len(*listed.JSON200) != 1 {
		t.Fatalf("list body unexpected: %s", listed.Body)
	}
	var listRaw []map[string]any
	if err := json.Unmarshal(listed.Body, &listRaw); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if _, ok := listRaw[0]["token"]; ok {
		t.Fatal("GET list JSON must not contain raw token secret")
	}

	del, err := client.DeleteAgentTokenWithResponse(context.Background(), created.JSON201.Id)
	if err != nil {
		t.Fatalf("DeleteAgentTokenWithResponse: %v", err)
	}
	if del.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", del.StatusCode(), del.Body)
	}
}

func TestControlAPI_ApiKeys_CreateListDelete(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := controlapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	created, err := client.CreateApiKeyWithResponse(context.Background(), controlapi.CreateApiKeyJSONRequestBody{
		Name: "mcp",
	})
	if err != nil {
		t.Fatalf("CreateApiKeyWithResponse: %v", err)
	}
	if created.StatusCode() != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", created.StatusCode(), created.Body)
	}
	if created.JSON201 == nil || !strings.HasPrefix(created.JSON201.Token, "dj_pat_") {
		t.Fatalf("created key unexpected: %s", created.Body)
	}

	listed, err := client.ListApiKeysWithResponse(context.Background())
	if err != nil {
		t.Fatalf("ListApiKeysWithResponse: %v", err)
	}
	if listed.StatusCode() != http.StatusOK || listed.JSON200 == nil {
		t.Fatalf("list status/body unexpected: %d %s", listed.StatusCode(), listed.Body)
	}
	var listRaw []map[string]any
	if err := json.Unmarshal(listed.Body, &listRaw); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if _, ok := listRaw[0]["token"]; ok {
		t.Fatal("GET api-keys list JSON must not contain raw token secret")
	}

	del, err := client.DeleteApiKeyWithResponse(context.Background(), created.JSON201.Id)
	if err != nil {
		t.Fatalf("DeleteApiKeyWithResponse: %v", err)
	}
	if del.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", del.StatusCode(), del.Body)
	}
}

func TestControlAPI_Machines_ListAndGet(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := controlapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	listed, err := client.ListMachinesWithResponse(context.Background())
	if err != nil {
		t.Fatalf("ListMachinesWithResponse: %v", err)
	}
	if listed.StatusCode() != http.StatusOK || listed.JSON200 == nil || len(*listed.JSON200) != 1 {
		t.Fatalf("list unexpected: %d %s", listed.StatusCode(), listed.Body)
	}
	m := (*listed.JSON200)[0]
	if m.Status != controlapi.Online {
		t.Fatalf("status = %q, want online", m.Status)
	}
	if m.Hostname == "" || m.AgentVersion == "" || m.OsVersion == "" {
		t.Fatalf("machine metadata incomplete: %+v", m)
	}

	got, err := client.GetMachineWithResponse(context.Background(), m.MachineId)
	if err != nil {
		t.Fatalf("GetMachineWithResponse: %v", err)
	}
	if got.StatusCode() != http.StatusOK || got.JSON200 == nil {
		t.Fatalf("get unexpected: %d %s", got.StatusCode(), got.Body)
	}
	if got.JSON200.MachineId != m.MachineId {
		t.Fatalf("machine_id mismatch: %v vs %v", got.JSON200.MachineId, m.MachineId)
	}
}

func TestControlAPI_CreateAgentToken_ErrorResponse401And429(t *testing.T) {
	t.Parallel()

	t.Run("401", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{mode: "unauthorized"}, nil)))
		t.Cleanup(srv.Close)
		client, err := controlapi.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatalf("NewClientWithResponses: %v", err)
		}
		resp, err := client.CreateAgentTokenWithResponse(context.Background(), controlapi.CreateAgentTokenJSONRequestBody{Name: "x"})
		if err != nil {
			t.Fatalf("CreateAgentTokenWithResponse: %v", err)
		}
		if resp.StatusCode() != http.StatusUnauthorized || resp.JSON401 == nil {
			t.Fatalf("want 401 ErrorResponse, got %d %s", resp.StatusCode(), resp.Body)
		}
		if resp.JSON401.Code == "" || resp.JSON401.Message == "" || resp.JSON401.RequestId == "" {
			t.Fatalf("incomplete ErrorResponse: %+v", resp.JSON401)
		}
	})

	t.Run("429_retry_after", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{mode: "rate_limited"}, nil)))
		t.Cleanup(srv.Close)
		client, err := controlapi.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatalf("NewClientWithResponses: %v", err)
		}
		resp, err := client.CreateAgentTokenWithResponse(context.Background(), controlapi.CreateAgentTokenJSONRequestBody{Name: "x"})
		if err != nil {
			t.Fatalf("CreateAgentTokenWithResponse: %v", err)
		}
		if resp.StatusCode() != http.StatusTooManyRequests || resp.JSON429 == nil {
			t.Fatalf("want 429 ErrorResponse, got %d %s", resp.StatusCode(), resp.Body)
		}
		if resp.HTTPResponse.Header.Get("Retry-After") != "60" {
			t.Fatalf("Retry-After = %q, want 60", resp.HTTPResponse.Header.Get("Retry-After"))
		}
		if resp.Headers429 == nil || resp.Headers429.RetryAfter != "60" {
			t.Fatalf("Headers429 = %+v", resp.Headers429)
		}
	})
}
