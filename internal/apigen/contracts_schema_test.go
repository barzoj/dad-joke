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
