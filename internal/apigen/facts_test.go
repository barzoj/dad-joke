package apigen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func writeBindError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := "req-bind"
	if r != nil && r.Header.Get("X-Request-ID") != "" {
		reqID = r.Header.Get("X-Request-ID")
	}
	w.Header().Set("Content-Type", "application/json")
	status := http.StatusBadRequest
	code := "bad_request"
	msg := "invalid request"
	if err != nil && err.Error() != "" {
		msg = err.Error()
		if strings.Contains(strings.ToLower(msg), "request body too large") || strings.Contains(err.Error(), "http: request body too large") {
			status = http.StatusRequestEntityTooLarge
			code = "payload_too_large"
			msg = "request body too large"
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"code": code, "message": msg, "request_id": reqID,
	})
}

func agentStrictHandler(ssi agentapi.StrictServerInterface) http.Handler {
	return agentapi.HandlerWithOptions(
		agentapi.NewStrictHandlerWithOptions(ssi, nil, agentapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: writeBindError,
		}),
		agentapi.StdHTTPServerOptions{ErrorHandlerFunc: writeBindError},
	)
}

func controlStrictHandler(ssi controlapi.StrictServerInterface) http.Handler {
	return controlapi.HandlerWithOptions(
		controlapi.NewStrictHandlerWithOptions(ssi, nil, controlapi.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: writeBindError,
		}),
		controlapi.StdHTTPServerOptions{ErrorHandlerFunc: writeBindError},
	)
}

// withOpenAPIValidation validates requests against the generated OpenAPI before
// forwarding to next. Contract constraint failures map to 400 ErrorResponse.
func withOpenAPIValidation(t *testing.T, service string, next http.Handler) http.Handler {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(openAPIPath(t, service))
	if err != nil {
		t.Fatalf("load openapi: %v", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("validate openapi: %v", err)
	}
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		t.Fatalf("openapi router: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, pathParams, err := router.FindRoute(r)
		if err != nil {
			writeBindError(w, r, err)
			return
		}
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			writeBindError(w, r, err)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		input := &openapi3filter.RequestValidationInput{
			Request:    r,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
		}
		if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
			writeBindError(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		next.ServeHTTP(w, r)
	})
}

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
		UtcOffsetMinutes: 0,
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
	srv := httptest.NewServer(agentStrictHandler(agentStub{}))
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

	// Empty facts is a no-op success.
	empty := body
	empty.Facts = []agentapi.FactItem{}
	resp, err = client.PostFactsWithResponse(context.Background(), empty)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusOK || resp.JSON200 == nil {
		t.Fatalf("empty facts: %d %s", resp.StatusCode(), resp.Body)
	}

	// Max-size batch (500) round-trips.
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

	// user_sid absent + exes populated.
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
		srv := httptest.NewServer(agentStrictHandler(agentStub{mode: "unauthorized"}))
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
		srv := httptest.NewServer(agentStrictHandler(agentStub{mode: "rate_limited"}))
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
		srv := httptest.NewServer(controlStrictHandler(controlStub{mode: "unauthorized"}))
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
		srv := httptest.NewServer(controlStrictHandler(controlStub{mode: "rate_limited"}))
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

func postRaw(t *testing.T, h http.Handler, method, path string, body []byte) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code, rr.Body.Bytes()
}

func assertJSONError(t *testing.T, raw []byte) {
	t.Helper()
	var er struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &er); err != nil {
		t.Fatalf("decode ErrorResponse: %v body=%s", err, raw)
	}
	if er.Code == "" || er.Message == "" || er.RequestID == "" {
		t.Fatalf("incomplete ErrorResponse: %+v", er)
	}
}

func TestAgentAPI_PostFacts_NegativeDecode(t *testing.T) {
	t.Parallel()
	h := withOpenAPIValidation(t, "agent", agentStrictHandler(agentStub{}))

	base := validPostFactsBody()
	baseRaw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var baseMap map[string]any
	if err := json.Unmarshal(baseRaw, &baseMap); err != nil {
		t.Fatal(err)
	}

	mutate := func(fn func(map[string]any)) []byte {
		cp := map[string]any{}
		raw, _ := json.Marshal(baseMap)
		_ = json.Unmarshal(raw, &cp)
		fn(cp)
		out, err := json.Marshal(cp)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	cases := []struct {
		name string
		body []byte
	}{
		{"missing_time_zone", mutate(func(m map[string]any) { delete(m, "time_zone") })},
		{"seq_zero", mutate(func(m map[string]any) { m["seq"] = 0 })},
		{"seq_negative", mutate(func(m map[string]any) { m["seq"] = -1 })},
		{"non_uuid_stream_id", mutate(func(m map[string]any) { m["stream_id"] = "not-a-uuid" })},
		{"unknown_kind", mutate(func(m map[string]any) {
			facts := m["facts"].([]any)
			f0 := facts[0].(map[string]any)
			f0["kind"] = "nope.kind"
		})},
		{"dur_s_301", mutate(func(m map[string]any) {
			facts := m["facts"].([]any)
			f0 := facts[0].(map[string]any)
			f0["dur_s"] = 301
		})},
		{"dur_s_negative", mutate(func(m map[string]any) {
			facts := m["facts"].([]any)
			f0 := facts[0].(map[string]any)
			f0["dur_s"] = -1
		})},
		{"501_facts", func() []byte {
			m := mutate(func(map[string]any) {})
			var obj map[string]any
			_ = json.Unmarshal(m, &obj)
			facts := make([]any, 501)
			item := obj["facts"].([]any)[0]
			for i := range facts {
				facts[i] = item
			}
			obj["facts"] = facts
			out, _ := json.Marshal(obj)
			return out
		}()},
		{"title_257_runes", mutate(func(m map[string]any) {
			facts := m["facts"].([]any)
			// use fg.sample item (index 2 in validPostFactsBody)
			f := facts[2].(map[string]any)
			data := f["data"].(map[string]any)
			title := strings.Repeat("あ", 257)
			if utf8.RuneCountInString(title) != 257 {
				t.Fatal("title rune count")
			}
			data["title"] = title
		})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, raw := postRaw(t, h, http.MethodPost, "/v1/facts", tc.body)
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", code, raw)
			}
			assertJSONError(t, raw)
		})
	}
}

func TestAgentAPI_Heartbeat_OldShapeMissingTimeZone400(t *testing.T) {
	t.Parallel()
	h := withOpenAPIValidation(t, "agent", agentStrictHandler(agentStub{}))
	body := []byte(`{
		"machine_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"agent_version":"0.0.1",
		"hostname":"pc",
		"os_version":"Win",
		"applied_policy_version":0
	}`)
	code, raw := postRaw(t, h, http.MethodPost, "/v1/heartbeat", body)
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	assertJSONError(t, raw)
}

func TestControlAPI_GetMachineUsage_QueryValidation(t *testing.T) {
	t.Parallel()
	h := withOpenAPIValidation(t, "control", controlStrictHandler(controlStub{}))
	machine := "33333333-3333-4333-8333-333333333333"

	t.Run("missing_date", func(t *testing.T) {
		t.Parallel()
		code, raw := postRaw(t, h, http.MethodGet, "/v1/machines/"+machine+"/usage", nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertJSONError(t, raw)
	})

	t.Run("invalid_calendar_date", func(t *testing.T) {
		t.Parallel()
		code, raw := postRaw(t, h, http.MethodGet, "/v1/machines/"+machine+"/usage?date=2026-02-30", nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertJSONError(t, raw)
	})

	t.Run("wrong_format_date", func(t *testing.T) {
		t.Parallel()
		code, raw := postRaw(t, h, http.MethodGet, "/v1/machines/"+machine+"/usage?date=28-11-2026", nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertJSONError(t, raw)
	})

	t.Run("empty_date", func(t *testing.T) {
		t.Parallel()
		code, raw := postRaw(t, h, http.MethodGet, "/v1/machines/"+machine+"/usage?date=", nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertJSONError(t, raw)
	})

	t.Run("unknown_tz_param_ignored", func(t *testing.T) {
		t.Parallel()
		// OpenAPI does not declare tz; generators ignore unknown query params.
		code, raw := postRaw(t, h, http.MethodGet, "/v1/machines/"+machine+"/usage?date=2026-11-28&tz=UTC", nil)
		if code != http.StatusOK {
			t.Fatalf("status=%d body=%s (tz should be ignored)", code, raw)
		}
		var usage controlapi.MachineUsage
		if err := json.Unmarshal(raw, &usage); err != nil {
			t.Fatal(err)
		}
		if usage.TimeZone != "UTC" || len(usage.Apps) != 1 {
			t.Fatalf("usage=%+v", usage)
		}
	})
}

func TestAgentAPI_PostFacts_BindNonUUIDWithoutOpenAPIFilter(t *testing.T) {
	t.Parallel()
	// BindJSONBody alone rejects non-uuid stream_id via openapi_types.UUID.
	h := agentStrictHandler(agentStub{})
	body := fmt.Sprintf(`{
		"machine_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"stream_id":"not-a-uuid",
		"seq":1,
		"sent_at":"2026-11-28T12:00:00Z",
		"time_zone":"UTC",
		"utc_offset_minutes":0,
		"facts":[]
	}`)
	code, raw := postRaw(t, h, http.MethodPost, "/v1/facts", []byte(body))
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", code, raw)
	}
	assertJSONError(t, raw)
}
