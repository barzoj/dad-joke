package mcp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	djmcp "github.com/barzoj/dad-joke/internal/mcp"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

// TestMCP_StructuredContentWireObject asserts the MCP CallToolResult contract on
// the raw JSON-RPC wire for all seven tools: every successful call MUST include
// a JSON-object structuredContent; content[0].text matches; empty lists are [].
func TestMCP_StructuredContentWireObject(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-sc-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})
	mcpURL := strings.TrimRight(base, "/") + "/mcp"

	listed := callJSONRPC(t, mcpURL, pat, 1, "tools/list", map[string]any{})
	toolsRaw, ok := listed["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list missing tools: %v", listed)
	}
	metaByName := map[string]map[string]any{}
	for _, raw := range toolsRaw {
		tool, _ := raw.(map[string]any)
		name, _ := tool["name"].(string)
		metaByName[name] = tool
	}
	for _, name := range djmcp.ToolNames() {
		if metaByName[name] == nil {
			t.Fatalf("tools/list missing %q", name)
		}
		inSchema, ok := metaByName[name]["inputSchema"].(map[string]any)
		if !ok || inSchema["type"] != "object" {
			t.Fatalf("tool %q inputSchema must be object, got %v", name, metaByName[name]["inputSchema"])
		}
		if out, exists := metaByName[name]["outputSchema"]; exists && out != nil {
			outSchema, ok := out.(map[string]any)
			if !ok || outSchema["type"] != "object" {
				t.Fatalf("tool %q outputSchema must be object when present, got %v", name, out)
			}
		}
	}

	// --- empty list cases (before enrollment) ---
	emptyMachines := callToolWire(t, mcpURL, pat, 10, "list_machines", map[string]any{})
	assertWireObjectResult(t, emptyMachines, false)
	assertWireListField(t, emptyMachines, "machines", 0)

	emptyTokens := callToolWire(t, mcpURL, pat, 11, "manage_agent_tokens", map[string]any{
		"action": "list",
	})
	assertWireObjectResult(t, emptyTokens, false)
	assertWireListField(t, emptyTokens, "tokens", 0)

	// --- populate: create agent token, enroll machine ---
	created := callToolWire(t, mcpURL, pat, 12, "manage_agent_tokens", map[string]any{
		"action": "create", "name": "wire-agent",
	})
	assertWireObjectResult(t, created, false)
	createdObj := wirePayloadObject(t, created)
	agentTok, _ := createdObj["token"].(string)
	tokID, _ := createdObj["id"].(string)
	if !strings.HasPrefix(agentTok, "dj_agt_") {
		t.Fatalf("create token: %v", createdObj)
	}

	machineID := uuid.NewString()
	if heartbeat(t, base, agentTok, machineID) != http.StatusOK {
		t.Fatal("heartbeat")
	}

	populatedMachines := callToolWire(t, mcpURL, pat, 13, "list_machines", map[string]any{})
	assertWireObjectResult(t, populatedMachines, false)
	assertWireListField(t, populatedMachines, "machines", 1)

	gotMachine := callToolWire(t, mcpURL, pat, 14, "get_machine", map[string]any{
		"machine_id": machineID,
	})
	assertWireObjectResult(t, gotMachine, false)
	if wirePayloadObject(t, gotMachine)["machine_id"] != machineID {
		t.Fatalf("get_machine: %v", gotMachine)
	}

	policy := callToolWire(t, mcpURL, pat, 15, "set_policy", map[string]any{
		"machine_id":       machineID,
		"expected_version": 0,
		"document":         map[string]any{"block": []string{"game.exe"}},
	})
	assertWireObjectResult(t, policy, false)

	job := callToolWire(t, mcpURL, pat, 16, "run_script", map[string]any{
		"machine_id": machineID,
		"type":       "cmd",
		"payload":    "echo wire",
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})
	assertWireObjectResult(t, job, false)
	jobID, _ := wirePayloadObject(t, job)["id"].(string)

	gotJob := callToolWire(t, mcpURL, pat, 17, "get_job", map[string]any{"job_id": jobID})
	assertWireObjectResult(t, gotJob, false)

	canceled := callToolWire(t, mcpURL, pat, 18, "get_job", map[string]any{
		"job_id": jobID, "action": "cancel",
	})
	assertWireObjectResult(t, canceled, false)

	events := callToolWire(t, mcpURL, pat, 19, "list_events", map[string]any{
		"machine_id": machineID,
	})
	assertWireObjectResult(t, events, false)
	eventsObj := wirePayloadObject(t, events)
	if _, ok := eventsObj["events"].([]any); !ok {
		t.Fatalf("list_events events must be array, got %T %v", eventsObj["events"], eventsObj)
	}

	listedTokens := callToolWire(t, mcpURL, pat, 20, "manage_agent_tokens", map[string]any{
		"action": "list",
	})
	assertWireObjectResult(t, listedTokens, false)
	assertWireListField(t, listedTokens, "tokens", 1)
	rawListed, _ := json.Marshal(listedTokens)
	assertNoSecret(t, string(rawListed), agentTok, pat)

	revoked := callToolWire(t, mcpURL, pat, 21, "manage_agent_tokens", map[string]any{
		"action": "revoke", "id": tokID,
	})
	assertWireObjectResult(t, revoked, false)

	// --- error cases: isError=true; structuredContent omitted or still an object ---
	errMachine := callToolWire(t, mcpURL, pat, 30, "get_machine", map[string]any{
		"machine_id": uuid.NewString(),
	})
	assertWireObjectResult(t, errMachine, true)

	errAction := callToolWire(t, mcpURL, pat, 31, "manage_agent_tokens", map[string]any{
		"action": "nope",
	})
	assertWireObjectResult(t, errAction, true)

	errJob := callToolWire(t, mcpURL, pat, 32, "get_job", map[string]any{
		"job_id": "not-a-uuid",
	})
	assertWireObjectResult(t, errJob, true)

	errPolicy := callToolWire(t, mcpURL, pat, 33, "set_policy", map[string]any{
		"machine_id":       "not-a-uuid",
		"expected_version": 0,
		"document":         map[string]any{"block": []string{"x"}},
	})
	assertWireObjectResult(t, errPolicy, true)

	errScript := callToolWire(t, mcpURL, pat, 34, "run_script", map[string]any{
		"machine_id": machineID,
		"type":       "not-an-interpreter",
		"payload":    "echo x",
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})
	assertWireObjectResult(t, errScript, true)

	errEvents := callToolWire(t, mcpURL, pat, 35, "list_events", map[string]any{
		"machine_id": "not-a-uuid",
	})
	assertWireObjectResult(t, errEvents, true)

	errRevoke := callToolWire(t, mcpURL, pat, 36, "manage_agent_tokens", map[string]any{
		"action": "revoke", "id": uuid.NewString(),
	})
	assertWireObjectResult(t, errRevoke, true)
}

func callJSONRPC(t *testing.T, mcpURL, pat string, id int, method string, params map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatal(err)
	}
	code, raw := postMCPRaw(t, mcpURL, map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json, text/event-stream",
		"Authorization": "Bearer " + pat,
	}, string(body))
	if code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", method, code, raw)
	}
	envelope := parseJSONRPCResult(t, raw)
	if errObj, ok := envelope["error"]; ok && errObj != nil {
		t.Fatalf("%s json-rpc error: %v", method, errObj)
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing result object: %s", method, raw)
	}
	return result
}

func callToolWire(t *testing.T, mcpURL, pat string, id int, name string, args map[string]any) map[string]any {
	t.Helper()
	return callJSONRPC(t, mcpURL, pat, id, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
}

func parseJSONRPCResult(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	trimmed := bytes.TrimSpace(raw)
	// Stateless JSONResponse is a single JSON object; tolerate SSE "data:" framing.
	if bytes.HasPrefix(trimmed, []byte("event:")) || bytes.HasPrefix(trimmed, []byte("data:")) {
		var dataLine []byte
		for _, line := range bytes.Split(trimmed, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				dataLine = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				break
			}
		}
		if len(dataLine) == 0 {
			t.Fatalf("sse response missing data line: %s", raw)
		}
		trimmed = dataLine
	}
	var envelope map[string]any
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		t.Fatalf("decode json-rpc: %v body=%s", err, raw)
	}
	return envelope
}

func assertWireObjectResult(t *testing.T, result map[string]any, expectError bool) {
	t.Helper()
	isError, _ := result["isError"].(bool)
	if expectError && !isError {
		t.Fatalf("expected isError tool result, got %v", result)
	}
	if !expectError && isError {
		t.Fatalf("unexpected tool error: %v", result)
	}

	sc, hasSC := result["structuredContent"]
	scObj, scIsObj := sc.(map[string]any)
	if !expectError {
		if !hasSC || sc == nil {
			t.Fatalf("successful tool call missing structuredContent object: %v", result)
		}
		if !scIsObj {
			t.Fatalf("structuredContent must be a JSON object, got %T (%v)", sc, sc)
		}
	} else if hasSC && sc != nil && !scIsObj {
		t.Fatalf("error structuredContent must be a JSON object when present, got %T (%v)", sc, sc)
	}

	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("missing content[0]")
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	if text == "" && !expectError {
		t.Fatal("empty content text")
	}
	if text == "" {
		return
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		if expectError {
			return // plain-text tool errors are allowed
		}
		t.Fatalf("content text not JSON: %v body=%s", err, text)
	}
	if !expectError {
		textObj, ok := decoded.(map[string]any)
		if !ok {
			t.Fatalf("content text must be a JSON object, got %T (%s)", decoded, text)
		}
		textRaw, _ := json.Marshal(textObj)
		scRaw, _ := json.Marshal(scObj)
		var textNorm, scNorm any
		_ = json.Unmarshal(textRaw, &textNorm)
		_ = json.Unmarshal(scRaw, &scNorm)
		textRaw2, _ := json.Marshal(textNorm)
		scRaw2, _ := json.Marshal(scNorm)
		if string(textRaw2) != string(scRaw2) {
			t.Fatalf("content text != structuredContent\ntext=%s\nsc=%s", textRaw2, scRaw2)
		}
	}
}

func wirePayloadObject(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	sc, ok := result["structuredContent"].(map[string]any)
	if !ok || sc == nil {
		t.Fatalf("structuredContent object required, got %T (%v)", result["structuredContent"], result["structuredContent"])
	}
	return sc
}

func assertWireListField(t *testing.T, result map[string]any, field string, wantLen int) {
	t.Helper()
	obj := wirePayloadObject(t, result)
	raw, exists := obj[field]
	if !exists {
		t.Fatalf("missing %q in %v", field, obj)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("%q must be array (never null), got %T", field, raw)
	}
	if len(arr) != wantLen {
		t.Fatalf("%q len=%d want %d (%v)", field, len(arr), wantLen, obj)
	}
	// Round-trip through JSON to ensure empty encodes as [] not null.
	encoded, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	needle := fmt.Sprintf(`"%s":null`, field)
	if bytes.Contains(encoded, []byte(needle)) {
		t.Fatalf("%q serialized as null: %s", field, encoded)
	}
	if wantLen == 0 {
		emptyNeedle := fmt.Sprintf(`"%s":[]`, field)
		if !bytes.Contains(encoded, []byte(emptyNeedle)) {
			t.Fatalf("empty %q must serialize as [], got %s", field, encoded)
		}
	}
}
