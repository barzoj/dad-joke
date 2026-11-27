package apigen_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

func TestAgentAPI_GetPolicy_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := agentapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.GetPolicyWithResponse(context.Background(), &agentapi.GetPolicyParams{
		MachineId: mustUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
	}
	if resp.JSON200.Version != 1 {
		t.Fatalf("version=%d", resp.JSON200.Version)
	}
}

func TestControlAPI_PutMachinePolicy_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := controlapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	machineID := mustUUID("33333333-3333-4333-8333-333333333333")
	resp, err := client.PutMachinePolicyWithResponse(context.Background(), machineID, controlapi.PutMachinePolicyJSONRequestBody{
		ExpectedVersion: 0,
		Document:        map[string]any{"firewall": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
	}
	if resp.JSON200.Version != 1 || resp.JSON200.Document["firewall"] != true {
		t.Fatalf("unexpected: %+v", resp.JSON200)
	}

	get, err := client.GetMachinePolicyWithResponse(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	if get.StatusCode() != http.StatusOK || get.JSON200 == nil {
		t.Fatalf("get status=%d body=%s", get.StatusCode(), get.Body)
	}
}
