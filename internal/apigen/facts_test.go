package apigen_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func sampleFactItem(kind agentapi.DadJokeCommonFactKind, title string) agentapi.FactItem {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	data := agentapi.FactData{}
	switch kind {
	case agentapi.SessionState:
		sid, user, state := "sess-1", "user", agentapi.Active
		data.SessionId = &sid
		data.Username = &user
		data.State = &state
	case agentapi.ProcSample:
		exes := []string{}
		data.Exes = &exes
	case agentapi.FgSample:
		exe, pid, idle := "notepad.exe", int32(42), int32(0)
		data.Exe = &exe
		data.Pid = &pid
		data.Title = &title
		data.IdleS = &idle
	case agentapi.QueueDropped:
		batches, dropped := int32(1), int64(128)
		data.DroppedBatches = &batches
		data.DroppedBytes = &dropped
	case agentapi.SessionRestarted:
		// no required data members
	}
	return agentapi.FactItem{
		Ts:     now,
		Kind:   kind,
		Plugin: "sensor",
		DurS:   1,
		Data:   data,
	}
}

func validPostFactsBody() agentapi.PostFactsRequest {
	return agentapi.PostFactsRequest{
		MachineId:        mustUUID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		StreamId:         mustUUID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
		Seq:              1,
		SentAt:           time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC),
		TimeZone:         "UTC",
		UtcOffsetMinutes: ptrInt32(0),
		Facts: []agentapi.FactItem{
			sampleFactItem(agentapi.SessionState, ""),
			sampleFactItem(agentapi.ProcSample, ""),
			sampleFactItem(agentapi.FgSample, "экран 🎮"),
			sampleFactItem(agentapi.QueueDropped, ""),
			sampleFactItem(agentapi.SessionRestarted, ""),
		},
	}
}

func TestAgentAPI_PostFacts_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{}, nil)))
	t.Cleanup(srv.Close)
	client, err := agentapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	body := validPostFactsBody()
	resp, err := client.PostFactsWithResponse(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
	}
	if resp.JSON200.AcceptedSeq != 1 || resp.JSON200.Duplicate {
		t.Fatalf("ack=%+v", resp.JSON200)
	}

	empty := body
	empty.Facts = []agentapi.FactItem{}
	resp, err = client.PostFactsWithResponse(context.Background(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		t.Fatalf("empty facts: %d %s", resp.StatusCode(), resp.Body)
	}

	maxBody := body
	maxBody.Facts = make([]agentapi.FactItem, 500)
	for i := range maxBody.Facts {
		maxBody.Facts[i] = sampleFactItem(agentapi.ProcSample, "")
	}
	resp, err = client.PostFactsWithResponse(context.Background(), maxBody)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("500 facts: %d %s", resp.StatusCode(), resp.Body)
	}

	populated := body
	exes := []string{"a.exe", "b.exe"}
	item := sampleFactItem(agentapi.ProcSample, "")
	item.Data.Exes = &exes
	populated.Facts = []agentapi.FactItem{item}
	resp, err = client.PostFactsWithResponse(context.Background(), populated)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("populated exes: %d %s", resp.StatusCode(), resp.Body)
	}
}

func TestAgentAPI_PostFacts_ErrorShapes401429(t *testing.T) {
	t.Parallel()

	t.Run("401", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{mode: "unauthorized"}, nil)))
		t.Cleanup(srv.Close)
		client, err := agentapi.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.PostFactsWithResponse(context.Background(), validPostFactsBody())
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode() != http.StatusUnauthorized || resp.JSON401 == nil {
			t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
		}
		if resp.JSON401.Code != "unauthorized" || resp.JSON401.RequestId == "" || resp.JSON401.Message == "" {
			t.Fatalf("ErrorResponse incomplete: %+v", resp.JSON401)
		}
	})

	t.Run("429", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(agentapi.Handler(agentapi.NewStrictHandler(agentStub{mode: "rate_limited"}, nil)))
		t.Cleanup(srv.Close)
		client, err := agentapi.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.PostFactsWithResponse(context.Background(), validPostFactsBody())
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode() != http.StatusTooManyRequests || resp.JSON429 == nil {
			t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
		}
		if resp.JSON429.Code != "rate_limited" || resp.JSON429.RequestId == "" {
			t.Fatalf("ErrorResponse incomplete: %+v", resp.JSON429)
		}
		if resp.HTTPResponse.Header.Get("Retry-After") != "15" {
			t.Fatalf("Retry-After=%q", resp.HTTPResponse.Header.Get("Retry-After"))
		}
	})
}

func TestControlAPI_GetMachineUsage_ErrorShapes401429(t *testing.T) {
	t.Parallel()
	machineID := mustUUID("33333333-3333-4333-8333-333333333333")
	date := openapi_types.Date{Time: time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)}

	t.Run("401", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{mode: "unauthorized"}, nil)))
		t.Cleanup(srv.Close)
		client, err := controlapi.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.GetMachineUsageWithResponse(context.Background(), machineID, &controlapi.GetMachineUsageParams{Date: date})
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode() != http.StatusUnauthorized || resp.JSON401 == nil {
			t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
		}
		if resp.JSON401.Code != "unauthorized" || resp.JSON401.RequestId == "" {
			t.Fatalf("ErrorResponse incomplete: %+v", resp.JSON401)
		}
	})

	t.Run("429", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(controlapi.Handler(controlapi.NewStrictHandler(controlStub{mode: "rate_limited"}, nil)))
		t.Cleanup(srv.Close)
		client, err := controlapi.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.GetMachineUsageWithResponse(context.Background(), machineID, &controlapi.GetMachineUsageParams{Date: date})
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode() != http.StatusTooManyRequests || resp.JSON429 == nil {
			t.Fatalf("status=%d body=%s", resp.StatusCode(), resp.Body)
		}
		if resp.HTTPResponse.Header.Get("Retry-After") != "20" {
			t.Fatalf("Retry-After=%q", resp.HTTPResponse.Header.Get("Retry-After"))
		}
	})
}
