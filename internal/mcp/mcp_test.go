package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	djmcp "github.com/barzoj/dad-joke/internal/mcp"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func startFullServer(t *testing.T, st store.Store, cfg server.Config) string {
	t.Helper()
	cfg.Store = st
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	secure := false
	if cfg.CookieSecure == nil {
		cfg.CookieSecure = &secure
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, ln) }()
	return "http://" + ln.Addr().String()
}

func seedPAT(t *testing.T, st store.Store, name string) (tenantID, raw string) {
	t.Helper()
	res, err := bootstrap.Run(context.Background(), st, name)
	if err != nil {
		t.Fatal(err)
	}
	return res.TenantID, res.RawKey
}

func mcpConnect(t *testing.T, base, bearer, cookie string) *mcpsdk.ClientSession {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripAuth{bearer: bearer, cookie: cookie}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "mcp-test", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcpsdk.StreamableClientTransport{
		Endpoint:   strings.TrimRight(base, "/") + "/mcp",
		HTTPClient: httpClient,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type roundTripAuth struct {
	bearer string
	cookie string
}

func (r roundTripAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	if r.cookie != "" {
		req.Header.Set("Cookie", "dj_session="+r.cookie)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func callTool(t *testing.T, session *mcpsdk.ClientSession, name string, args map[string]any) *mcpsdk.CallToolResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func toolText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty tool content")
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("content type %T", res.Content[0])
	}
	return tc.Text
}

func decodeToolJSON(t *testing.T, res *mcpsdk.CallToolResult) any {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error: %s", toolText(t, res))
	}
	assertStructuredContentIsObject(t, res)
	text := toolText(t, res)
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("decode: %v body=%s", err, text)
	}
	return v
}

func assertStructuredContentIsObject(t *testing.T, res *mcpsdk.CallToolResult) {
	t.Helper()
	if res == nil {
		t.Fatal("nil CallToolResult")
	}
	if res.IsError {
		if res.StructuredContent == nil {
			return
		}
	} else if res.StructuredContent == nil {
		t.Fatal("successful tool call missing StructuredContent object")
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("StructuredContent token: %v raw=%s", err, raw)
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		t.Fatalf("StructuredContent must be a JSON object, got %s", raw)
	}
}

func postMCPRaw(t *testing.T, url string, headers map[string]string, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}

func assertNoSecret(t *testing.T, haystack string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(haystack, s) {
			t.Fatalf("raw secret leaked")
		}
	}
}

func heartbeat(t *testing.T, base, agentTok, machineID string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"machine_id":             machineID,
		"agent_version":          "0.0.1",
		"hostname":               "pc",
		"os_version":             "Windows 11",
		"applied_policy_version": 0,
		"time_zone":              "UTC",
		"utc_offset_minutes":     0,
	})
	req, err := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agentTok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

func TestMCP_ToolsListExactlyEight(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-tools-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})
	session := mcpConnect(t, base, pat, "")

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantNames := djmcp.ToolNames()
	if len(listed.Tools) != len(wantNames) {
		t.Fatalf("tools = %d, want %d", len(listed.Tools), len(wantNames))
	}
	want := make(map[string]bool, len(wantNames))
	for _, n := range wantNames {
		want[n] = true
	}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func TestMCP_AuthPATAndSession_Invalid401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-auth-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})

	_ = mcpConnect(t, base, pat, "")

	code, body := postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json, text/event-stream",
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", code, body)
	}
	assertNoSecret(t, string(body), pat)

	bad := "dj_pat_" + strings.Repeat("0", 64)
	code, body = postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json, text/event-stream",
		"Authorization": "Bearer " + bad,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("bad pat status=%d body=%s", code, body)
	}
	assertNoSecret(t, string(body), bad, pat)

	email := "mcp-user-" + uuid.NewString()[:8] + "@example.com"
	regBody, _ := json.Marshal(map[string]any{
		"email": email, "password": "password123", "tenant_name": "sess-" + uuid.NewString()[:8],
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("register: %d %s", res.StatusCode, b)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sess.AccessToken, "dj_ses_") {
		t.Fatalf("session token prefix: %q", sess.AccessToken)
	}
	_ = mcpConnect(t, base, sess.AccessToken, "")
	_ = mcpConnect(t, base, "", sess.AccessToken)

	logoutReq, _ := http.NewRequest(http.MethodPost, base+"/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+sess.AccessToken)
	logoutRes, err := http.DefaultClient.Do(logoutReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = logoutRes.Body.Close()

	code, body = postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json, text/event-stream",
		"Authorization": "Bearer " + sess.AccessToken,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d body=%s", code, body)
	}
	assertNoSecret(t, string(body), sess.AccessToken, pat)
}

func TestMCP_StreamableHTTP_NegativeAcceptAndAuth(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-neg-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})

	code, body := postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + pat,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code == http.StatusOK {
		t.Fatalf("missing Accept should fail, got 200")
	}
	assertNoSecret(t, string(body), pat)

	code, body = postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": "Bearer " + pat,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code == http.StatusOK {
		t.Fatalf("invalid Accept should fail, got 200")
	}
	assertNoSecret(t, string(body), pat)

	code, body = postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json, text/event-stream",
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%s", code, body)
	}
}

func TestMCP_CrossTenantIsolation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, patA := seedPAT(t, st, "mcp-a-"+uuid.NewString()[:8])
	_, patB := seedPAT(t, st, "mcp-b-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})

	sessionA := mcpConnect(t, base, patA, "")
	created := decodeToolJSON(t, callTool(t, sessionA, "manage_agent_tokens", map[string]any{
		"action": "create", "name": "agent-a",
	})).(map[string]any)
	agentTok, _ := created["token"].(string)
	tokID, _ := created["id"].(string)
	if !strings.HasPrefix(agentTok, "dj_agt_") {
		t.Fatalf("agent token missing")
	}
	machineID := uuid.NewString()
	if heartbeat(t, base, agentTok, machineID) != http.StatusOK {
		t.Fatal("heartbeat")
	}

	sessionB := mcpConnect(t, base, patB, "")
	listB := decodeToolJSON(t, callTool(t, sessionB, "list_machines", nil)).(map[string]any)
	arr, _ := listB["machines"].([]any)
	if len(arr) != 0 {
		t.Fatalf("tenant B saw machines: %v", listB)
	}

	res := callTool(t, sessionB, "get_machine", map[string]any{"machine_id": machineID})
	if !res.IsError {
		t.Fatal("tenant B get_machine should error")
	}
	errText := toolText(t, res)
	assertNoSecret(t, errText, patA, patB, agentTok)
	if strings.Contains(errText, "goroutine") || strings.Contains(errText, "runtime/") {
		t.Fatalf("stack leaked: %s", errText)
	}

	res = callTool(t, sessionB, "manage_agent_tokens", map[string]any{
		"action": "revoke", "id": tokID,
	})
	if !res.IsError {
		t.Fatal("cross-tenant revoke should fail")
	}
	assertNoSecret(t, toolText(t, res), patA, patB, agentTok)

	listA := decodeToolJSON(t, callTool(t, sessionA, "list_machines", nil)).(map[string]any)
	arrA, _ := listA["machines"].([]any)
	if len(arrA) != 1 {
		t.Fatalf("tenant A machines = %v", listA)
	}
}

func TestMCP_ManageAgentTokensLifecycle(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-life-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})
	session := mcpConnect(t, base, pat, "")

	created := decodeToolJSON(t, callTool(t, session, "manage_agent_tokens", map[string]any{
		"action": "create", "name": "lifecycle",
	})).(map[string]any)
	agentTok, _ := created["token"].(string)
	tokID, _ := created["id"].(string)
	machineID := uuid.NewString()
	if heartbeat(t, base, agentTok, machineID) != http.StatusOK {
		t.Fatal("fresh agent token rejected")
	}

	_ = decodeToolJSON(t, callTool(t, session, "manage_agent_tokens", map[string]any{
		"action": "revoke", "id": tokID,
	}))

	if heartbeat(t, base, agentTok, machineID) != http.StatusUnauthorized {
		t.Fatal("revoked agent token still accepted")
	}
}

func TestMCP_ToolValidationAndNoLeak(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-val-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})
	session := mcpConnect(t, base, pat, "")

	res := callTool(t, session, "get_machine", map[string]any{"machine_id": uuid.NewString()})
	if !res.IsError {
		t.Fatal("unknown machine_id should error")
	}
	msg := toolText(t, res)
	assertNoSecret(t, msg, pat)
	if strings.Contains(msg, "goroutine") || strings.Contains(msg, "runtime/") {
		t.Fatalf("stack in error: %s", msg)
	}

	res = callTool(t, session, "manage_agent_tokens", map[string]any{"action": "nope"})
	if !res.IsError {
		t.Fatal("bad action should error")
	}
	assertNoSecret(t, toolText(t, res), pat)

	res = callTool(t, session, "get_job", map[string]any{"job_id": "not-a-uuid"})
	if !res.IsError {
		t.Fatal("bad job_id should error")
	}
	assertNoSecret(t, toolText(t, res), pat)
}

func TestMCP_Upstream429MapsToToolError(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-429-"+uuid.NewString()[:8])
	mutations := ratelimit.New(ratelimit.Config{
		Window:  time.Minute,
		MaxHits: 1,
		Clock:   time.Now,
	})
	base := startFullServer(t, st, server.Config{MutationLimiter: mutations})
	session := mcpConnect(t, base, pat, "")

	// First mutation: create agent token (consumes the single mutation budget).
	created := decodeToolJSON(t, callTool(t, session, "manage_agent_tokens", map[string]any{
		"action": "create", "name": "budget",
	})).(map[string]any)
	agentTok, _ := created["token"].(string)
	machineID := uuid.NewString()
	if heartbeat(t, base, agentTok, machineID) != http.StatusOK {
		t.Fatal("heartbeat")
	}

	// Second mutation via MCP should surface ControlAPI 429 as a tool error.
	res := callTool(t, session, "run_script", map[string]any{
		"machine_id": machineID,
		"type":       "powershell",
		"payload":    "echo hi",
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})
	if !res.IsError {
		t.Fatal("expected rate_limited tool error")
	}
	msg := toolText(t, res)
	if !strings.Contains(msg, "rate_limited") && !strings.Contains(strings.ToLower(msg), "too many") {
		t.Fatalf("expected rate limit mapping, got %q", msg)
	}
	assertNoSecret(t, msg, pat, agentTok)
}

func TestMCP_RedactionInLogsAndErrors(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-redact-"+uuid.NewString()[:8])
	var logBuf bytes.Buffer
	base := startFullServer(t, st, server.Config{Log: log.New(&logBuf, "server: ", 0)})

	bad := "dj_pat_" + strings.Repeat("f", 64)
	code, body := postMCPRaw(t, base+"/mcp", map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json, text/event-stream",
		"Authorization": "Bearer " + bad,
	}, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if code != http.StatusUnauthorized {
		t.Fatalf("status=%d", code)
	}
	assertNoSecret(t, string(body), bad, pat)
	assertNoSecret(t, logBuf.String(), bad, pat)
	if djmcp.RedactSecrets("leak "+bad) == "leak "+bad {
		t.Fatal("RedactSecrets should scrub dj_pat_ values")
	}
}

func TestMCP_SetPolicyRunScriptGetJobListEvents(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedPAT(t, st, "mcp-flow-"+uuid.NewString()[:8])
	base := startFullServer(t, st, server.Config{})
	session := mcpConnect(t, base, pat, "")

	created := decodeToolJSON(t, callTool(t, session, "manage_agent_tokens", map[string]any{
		"action": "create", "name": "flow",
	})).(map[string]any)
	agentTok, _ := created["token"].(string)
	machineID := uuid.NewString()
	if heartbeat(t, base, agentTok, machineID) != http.StatusOK {
		t.Fatal("heartbeat failed")
	}

	_ = decodeToolJSON(t, callTool(t, session, "set_policy", map[string]any{
		"machine_id":       machineID,
		"expected_version": 0,
		"document":         map[string]any{"block": []string{"game.exe"}},
	}))

	job := decodeToolJSON(t, callTool(t, session, "run_script", map[string]any{
		"machine_id": machineID,
		"type":       "cmd",
		"payload":    "echo hi",
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})).(map[string]any)
	jobID, _ := job["id"].(string)

	got := decodeToolJSON(t, callTool(t, session, "get_job", map[string]any{"job_id": jobID})).(map[string]any)
	if got["status"] != "pending" {
		t.Fatalf("status=%v", got["status"])
	}

	_ = decodeToolJSON(t, callTool(t, session, "get_job", map[string]any{
		"job_id": jobID, "action": "cancel",
	}))

	events := decodeToolJSON(t, callTool(t, session, "list_events", map[string]any{
		"machine_id": machineID,
	})).(map[string]any)
	if _, ok := events["events"]; !ok {
		t.Fatalf("events payload: %v", events)
	}

	listed := decodeToolJSON(t, callTool(t, session, "manage_agent_tokens", map[string]any{
		"action": "list",
	}))
	rawListed, _ := json.Marshal(listed)
	assertNoSecret(t, string(rawListed), agentTok, pat)
}
