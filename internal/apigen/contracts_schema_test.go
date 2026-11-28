package apigen_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/gen/go/controlapi"
)

func openAPIPath(t *testing.T, service string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	return filepath.Join(root, "gen", "openapi", service, "openapi.yaml")
}

func TestOpenAPI_SeparateSecuritySchemes(t *testing.T) {
	t.Parallel()

	agentSpec, err := os.ReadFile(openAPIPath(t, "agent"))
	if err != nil {
		t.Fatalf("read agent openapi: %v", err)
	}
	controlSpec, err := os.ReadFile(openAPIPath(t, "control"))
	if err != nil {
		t.Fatalf("read control openapi: %v", err)
	}

	agentText := string(agentSpec)
	controlText := string(controlSpec)

	if !strings.Contains(agentText, "AgentBearerAuth:") {
		t.Fatal("agent OpenAPI missing securitySchemes.AgentBearerAuth")
	}
	if strings.Contains(agentText, "ControlBearerAuth:") {
		t.Fatal("agent OpenAPI must not define ControlBearerAuth")
	}
	if !strings.Contains(controlText, "ControlBearerAuth:") {
		t.Fatal("control OpenAPI missing securitySchemes.ControlBearerAuth")
	}
	if strings.Contains(controlText, "AgentBearerAuth:") {
		t.Fatal("control OpenAPI must not define AgentBearerAuth")
	}

	// healthz is explicitly unauthenticated (empty security requirement).
	if !strings.Contains(agentText, "- {}") {
		t.Fatal("agent OpenAPI /healthz should allow unauthenticated access (security: [{}])")
	}
	if !strings.Contains(controlText, "- {}") {
		t.Fatal("control OpenAPI /healthz should allow unauthenticated access (security: [{}])")
	}

	for _, want := range []string{"code:", "message:", "request_id:"} {
		if !strings.Contains(agentText, want) {
			t.Fatalf("agent OpenAPI ErrorResponse missing %q", want)
		}
		if !strings.Contains(controlText, want) {
			t.Fatalf("control OpenAPI ErrorResponse missing %q", want)
		}
	}
	if !strings.Contains(agentText, "Retry-After:") {
		t.Fatal("agent OpenAPI missing Retry-After on 429")
	}
	if !strings.Contains(controlText, "Retry-After:") {
		t.Fatal("control OpenAPI missing Retry-After on 429")
	}

	if !strings.Contains(agentText, "/v1/policy:") {
		t.Fatal("agent OpenAPI missing /v1/policy")
	}
	if !strings.Contains(controlText, "/v1/machines/{machine_id}/policy:") {
		t.Fatal("control OpenAPI missing /v1/machines/{machine_id}/policy")
	}
	if !strings.Contains(controlText, "MachinePolicy:") || !strings.Contains(controlText, "PutMachinePolicyRequest:") {
		t.Fatal("control OpenAPI missing MachinePolicy / PutMachinePolicyRequest schemas")
	}
	if !strings.Contains(agentText, "/v1/jobs/{id}/claim:") || !strings.Contains(agentText, "/v1/jobs/{id}/result:") {
		t.Fatal("agent OpenAPI missing job claim/result routes")
	}
	if !strings.Contains(controlText, "/v1/machines/{machine_id}/jobs:") {
		t.Fatal("control OpenAPI missing /v1/machines/{machine_id}/jobs")
	}
	if !strings.Contains(controlText, "/v1/jobs/{id}:") || !strings.Contains(controlText, "/v1/jobs/{id}/cancel:") {
		t.Fatal("control OpenAPI missing job get/cancel routes")
	}
	if !strings.Contains(controlText, "CreateJobRequest:") || !strings.Contains(controlText, "Job:") {
		t.Fatal("control OpenAPI missing Job / CreateJobRequest schemas")
	}
	if !strings.Contains(agentText, "/v1/events:") {
		t.Fatal("agent OpenAPI missing /v1/events")
	}
	if !strings.Contains(agentText, "PostEventsRequest:") || !strings.Contains(agentText, "EventIngestItem:") {
		t.Fatal("agent OpenAPI missing PostEventsRequest / EventIngestItem schemas")
	}
	if !strings.Contains(controlText, "/v1/machines/{machine_id}/events:") {
		t.Fatal("control OpenAPI missing /v1/machines/{machine_id}/events")
	}
	if !strings.Contains(controlText, "EventListResponse:") || !strings.Contains(controlText, "MachineEvent:") {
		t.Fatal("control OpenAPI missing EventListResponse / MachineEvent schemas")
	}
	if !strings.Contains(agentText, "/v1/facts:") {
		t.Fatal("agent OpenAPI missing /v1/facts")
	}
	if !strings.Contains(agentText, "PostFactsRequest:") || !strings.Contains(agentText, "FactItem:") || !strings.Contains(agentText, "FactsAck:") {
		t.Fatal("agent OpenAPI missing PostFactsRequest / FactItem / FactsAck schemas")
	}
	if !strings.Contains(controlText, "/v1/machines/{machine_id}/usage:") {
		t.Fatal("control OpenAPI missing /v1/machines/{machine_id}/usage")
	}
	if !strings.Contains(controlText, "MachineUsage:") || !strings.Contains(controlText, "AppUsage:") {
		t.Fatal("control OpenAPI missing MachineUsage / AppUsage schemas")
	}
}

func TestOpenAPI_FactsAndUsageContractShapes(t *testing.T) {
	t.Parallel()

	agentSpec, err := os.ReadFile(openAPIPath(t, "agent"))
	if err != nil {
		t.Fatalf("read agent openapi: %v", err)
	}
	controlSpec, err := os.ReadFile(openAPIPath(t, "control"))
	if err != nil {
		t.Fatalf("read control openapi: %v", err)
	}
	agentText := string(agentSpec)
	controlText := string(controlSpec)

	postFacts := schemaBlock(t, agentText, "PostFactsRequest")
	for _, req := range []string{"machine_id", "stream_id", "seq", "sent_at", "time_zone", "utc_offset_minutes", "facts"} {
		if !strings.Contains(postFacts, "- "+req) {
			t.Fatalf("PostFactsRequest required missing %q in:\n%s", req, postFacts)
		}
	}
	if !strings.Contains(postFacts, "format: uuid") {
		t.Fatal("PostFactsRequest uuid fields missing format: uuid")
	}
	if !strings.Contains(postFacts, "minimum: 1") {
		t.Fatal("PostFactsRequest seq missing minimum: 1")
	}
	if !strings.Contains(postFacts, "maxItems: 500") {
		t.Fatal("PostFactsRequest facts missing maxItems: 500")
	}
	if !strings.Contains(postFacts, "minimum: -840") || !strings.Contains(postFacts, "maximum: 840") {
		t.Fatal("PostFactsRequest utc_offset_minutes missing −840..840 bounds")
	}
	if !strings.Contains(postFacts, "nullable: true") {
		t.Fatal("PostFactsRequest utc_offset_minutes must be nullable so Go binds *int32 (omission ≠ 0)")
	}

	factItem := schemaBlock(t, agentText, "FactItem")
	if !strings.Contains(factItem, "minimum: 0") || !strings.Contains(factItem, "maximum: 300") {
		t.Fatal("FactItem dur_s missing 0..300 bounds")
	}

	factData := schemaBlock(t, agentText, "FactData")
	if !strings.Contains(factData, "maxLength: 256") {
		t.Fatal("FactData title missing maxLength: 256")
	}
	if !strings.Contains(factData, "idle_s:") || !strings.Contains(factData, "minimum: 0") {
		t.Fatal("FactData idle_s missing minimum: 0")
	}
	if !strings.Contains(factData, "exes:") || !strings.Contains(factData, "ExeName") {
		t.Fatal("FactData exes must reference ExeName (per-item maxLength)")
	}
	exeName := schemaBlock(t, agentText, "DadJoke.Common.ExeName")
	if !strings.Contains(exeName, "maxLength: 260") {
		t.Fatal("ExeName missing maxLength: 260 (exes item constraint)")
	}

	kindBlock := schemaBlock(t, agentText, "DadJoke.Common.FactKind")
	wantKinds := []string{"session.state", "proc.sample", "fg.sample", "queue.dropped", "session.restarted"}
	for _, k := range wantKinds {
		if !strings.Contains(kindBlock, "- "+k) {
			t.Fatalf("FactKind missing %q in:\n%s", k, kindBlock)
		}
	}
	// Exact set: no extras beyond the five values.
	enumLines := 0
	for _, line := range strings.Split(kindBlock, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "- ") && !strings.HasPrefix(trim, "- $") {
			enumLines++
		}
	}
	if enumLines != len(wantKinds) {
		t.Fatalf("FactKind enum count = %d, want %d; block:\n%s", enumLines, len(wantKinds), kindBlock)
	}

	hb := schemaBlock(t, agentText, "HeartbeatRequest")
	for _, req := range []string{"time_zone", "utc_offset_minutes"} {
		if !strings.Contains(hb, "- "+req) {
			t.Fatalf("HeartbeatRequest required missing %q", req)
		}
	}

	factsPath := pathBlock(t, agentText, "/v1/facts")
	for _, code := range []string{"'200'", "'400'", "'401'", "'413'", "'429'", "'503'"} {
		if !strings.Contains(factsPath, code+":") {
			t.Fatalf("/v1/facts missing response %s", code)
		}
	}
	if !strings.Contains(factsPath, "Retry-After:") {
		t.Fatal("/v1/facts 429 missing Retry-After")
	}
	if !strings.Contains(factsPath, "ErrorResponse") {
		t.Fatal("/v1/facts error responses missing ErrorResponse")
	}

	usagePath := pathBlock(t, controlText, "/v1/machines/{machine_id}/usage")
	if !strings.Contains(usagePath, "name: date") || !strings.Contains(usagePath, "required: true") {
		t.Fatal("usage operation missing required date query")
	}
	if !strings.Contains(usagePath, "format: date") {
		t.Fatal("usage date query missing format: date")
	}
	if strings.Contains(usagePath, "name: tz") {
		t.Fatal("usage operation must not declare tz parameter")
	}
	for _, code := range []string{"'200'", "'400'", "'401'", "'404'", "'429'", "'503'"} {
		if !strings.Contains(usagePath, code+":") {
			t.Fatalf("usage missing response %s", code)
		}
	}

	appUsage := schemaBlock(t, controlText, "AppUsage")
	for _, req := range []string{"exe", "seconds", "fg_seconds"} {
		if !strings.Contains(appUsage, "- "+req) {
			t.Fatalf("AppUsage required missing %q", req)
		}
	}
}

var pathKeyRE = regexp.MustCompile(`(?m)^  (/[^:\n]+):\n`)

// pathBlock returns the YAML text for a named paths entry.
func pathBlock(t *testing.T, openapi, path string) string {
	t.Helper()
	matches := pathKeyRE.FindAllStringSubmatchIndex(openapi, -1)
	var start, end int = -1, len(openapi)
	for i, m := range matches {
		key := openapi[m[2]:m[3]]
		if key == path {
			start = m[1]
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			break
		}
	}
	if start < 0 {
		t.Fatalf("path %q not found", path)
	}
	return openapi[start:end]
}

func TestGoModels_RawTokenOnlyOnCreatedResponses(t *testing.T) {
	t.Parallel()

	assertHasJSONField(t, reflect.TypeOf(controlapi.CreatedAgentToken{}), "token")
	assertHasJSONField(t, reflect.TypeOf(controlapi.CreatedApiKey{}), "token")
	assertHasJSONField(t, reflect.TypeOf(controlapi.AuthSession{}), "access_token")
	assertLacksJSONField(t, reflect.TypeOf(controlapi.AgentTokenSummary{}), "token")
	assertLacksJSONField(t, reflect.TypeOf(controlapi.ApiKeySummary{}), "token")
	assertLacksJSONField(t, reflect.TypeOf(controlapi.UserMe{}), "password_hash")
	assertLacksJSONField(t, reflect.TypeOf(controlapi.AuthSession{}), "password_hash")

	controlSpec, err := os.ReadFile(openAPIPath(t, "control"))
	if err != nil {
		t.Fatalf("read control openapi: %v", err)
	}
	text := string(controlSpec)

	createdAgent := schemaBlock(t, text, "CreatedAgentToken")
	summaryAgent := schemaBlock(t, text, "AgentTokenSummary")
	createdKey := schemaBlock(t, text, "CreatedApiKey")
	summaryKey := schemaBlock(t, text, "ApiKeySummary")

	if !strings.Contains(createdAgent, "token:") {
		t.Fatal("CreatedAgentToken schema must include token")
	}
	if strings.Contains(summaryAgent, "token:") {
		t.Fatal("AgentTokenSummary schema must not include token")
	}
	if !strings.Contains(createdKey, "token:") {
		t.Fatal("CreatedApiKey schema must include token")
	}
	if strings.Contains(summaryKey, "token:") {
		t.Fatal("ApiKeySummary schema must not include token")
	}
}

func assertHasJSONField(t *testing.T, typ reflect.Type, jsonName string) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == jsonName {
			return
		}
	}
	t.Fatalf("%s missing json field %q", typ.Name(), jsonName)
}

func assertLacksJSONField(t *testing.T, typ reflect.Type, jsonName string) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == jsonName {
			t.Fatalf("%s must not have json field %q", typ.Name(), jsonName)
		}
	}
}

var schemaKeyRE = regexp.MustCompile(`(?m)^    ([A-Za-z0-9_.]+):\n`)

// schemaBlock returns the YAML text for a named components.schemas entry.
func schemaBlock(t *testing.T, openapi, name string) string {
	t.Helper()
	matches := schemaKeyRE.FindAllStringSubmatchIndex(openapi, -1)
	var start, end int = -1, len(openapi)
	for i, m := range matches {
		key := openapi[m[2]:m[3]]
		if key == name {
			start = m[1] // content begins after "    Name:\n"
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			break
		}
	}
	if start < 0 {
		t.Fatalf("schema %q not found", name)
	}
	return openapi[start:end]
}
