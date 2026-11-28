package apigen_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
)

func TestAgentAPI_Heartbeat_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{}, nil)))
	t.Cleanup(srv.Close)

	client, err := agentapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	machineID := mustUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	resp, err := client.PostHeartbeatWithResponse(context.Background(), agentapi.PostHeartbeatJSONRequestBody{
		MachineId:            machineID,
		AgentVersion:         "0.0.1-dev",
		Hostname:             "kid-pc",
		OsVersion:            "Windows 11 Home",
		AppliedPolicyVersion: 2,
		TimeZone:             "UTC",
		UtcOffsetMinutes:     0,
	})
	if err != nil {
		t.Fatalf("PostHeartbeatWithResponse: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode(), resp.Body)
	}
	if resp.JSON200 == nil {
		t.Fatalf("JSON200 is nil; body=%s", resp.Body)
	}
	if resp.JSON200.DesiredPolicyVersion != 2 {
		t.Fatalf("desired_policy_version = %d, want 2", resp.JSON200.DesiredPolicyVersion)
	}
	if len(resp.JSON200.PendingJobIds) != 1 || resp.JSON200.PendingJobIds[0] != "job-1" {
		t.Fatalf("pending_job_ids = %#v", resp.JSON200.PendingJobIds)
	}
	if !resp.JSON200.ServerTime.Equal(time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("server_time = %v", resp.JSON200.ServerTime)
	}
}

func TestAgentAPI_Heartbeat_ErrorResponse401(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{mode: "unauthorized"}, nil)))
	t.Cleanup(srv.Close)

	client, err := agentapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	resp, err := client.PostHeartbeatWithResponse(context.Background(), agentapi.PostHeartbeatJSONRequestBody{
		MachineId:            mustUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		AgentVersion:         "0.0.1-dev",
		Hostname:             "kid-pc",
		OsVersion:            "Windows 11 Home",
		AppliedPolicyVersion: 0,
		TimeZone:             "UTC",
		UtcOffsetMinutes:     0,
	})
	if err != nil {
		t.Fatalf("PostHeartbeatWithResponse: %v", err)
	}
	if resp.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", resp.StatusCode(), resp.Body)
	}
	if resp.JSON401 == nil {
		t.Fatalf("JSON401 is nil; body=%s", resp.Body)
	}
	if resp.JSON401.Code != "unauthorized" || resp.JSON401.RequestId == "" || resp.JSON401.Message == "" {
		t.Fatalf("ErrorResponse incomplete: %+v", resp.JSON401)
	}
}

func TestAgentAPI_Heartbeat_ErrorResponse429RetryAfter(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{mode: "rate_limited"}, nil)))
	t.Cleanup(srv.Close)

	client, err := agentapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses: %v", err)
	}

	resp, err := client.PostHeartbeatWithResponse(context.Background(), agentapi.PostHeartbeatJSONRequestBody{
		MachineId:            mustUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		AgentVersion:         "0.0.1-dev",
		Hostname:             "kid-pc",
		OsVersion:            "Windows 11 Home",
		AppliedPolicyVersion: 0,
		TimeZone:             "UTC",
		UtcOffsetMinutes:     0,
	})
	if err != nil {
		t.Fatalf("PostHeartbeatWithResponse: %v", err)
	}
	if resp.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", resp.StatusCode(), resp.Body)
	}
	if resp.JSON429 == nil {
		t.Fatalf("JSON429 is nil; body=%s", resp.Body)
	}
	if resp.JSON429.Code != "rate_limited" || resp.JSON429.RequestId == "" {
		t.Fatalf("ErrorResponse incomplete: %+v", resp.JSON429)
	}
	if resp.HTTPResponse.Header.Get("Retry-After") != "30" {
		t.Fatalf("Retry-After = %q, want 30", resp.HTTPResponse.Header.Get("Retry-After"))
	}
	if resp.Headers429 == nil || resp.Headers429.RetryAfter != "30" {
		t.Fatalf("Headers429 = %+v, want RetryAfter=30", resp.Headers429)
	}
}
