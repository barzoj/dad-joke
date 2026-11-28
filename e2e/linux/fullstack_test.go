//go:build e2e && linux

package linux_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/server"
	mongostore "github.com/barzoj/dad-joke/internal/store/mongo"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func mongoURI(t *testing.T) string {
	t.Helper()
	uri := strings.TrimSpace(os.Getenv("DAD_JOKE_MONGODB_URI"))
	if uri == "" {
		t.Skip("DAD_JOKE_MONGODB_URI not set")
	}
	return uri
}

func openStore(t *testing.T) *mongostore.Store {
	t.Helper()
	uri := mongoURI(t)
	dbName := "dadjoke_e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	var st *mongostore.Store
	var lastErr error
	backoff := 250 * time.Millisecond
	for attempt := 1; attempt <= 5; attempt++ {
		st, lastErr = mongostore.Open(ctx, mongostore.Config{URI: uri, Database: dbName})
		if lastErr == nil {
			break
		}
		if attempt == 5 {
			t.Fatalf("mongo Open: %v", lastErr)
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		_ = st.Close(dropCtx)
	})
	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	return st
}

func startLiveServer(t *testing.T, st *mongostore.Store) string {
	t.Helper()
	secure := false
	allow := true
	srv, err := server.New(server.Config{
		Addr:         "127.0.0.1:0",
		Store:        st,
		CookieSecure: &secure,
		AllowSignup:  &allow,
	})
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

type roundTripAuth struct {
	bearer string
}

func (r roundTripAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if r.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.bearer)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func mcpConnect(t *testing.T, base, bearer string) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "e2e-linux", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcpsdk.StreamableClientTransport{
		Endpoint:   strings.TrimRight(base, "/") + "/mcp",
		HTTPClient: &http.Client{Transport: roundTripAuth{bearer: bearer}},
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
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
	assertStructuredContentObject(t, res)
	var v any
	if err := json.Unmarshal([]byte(toolText(t, res)), &v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

// assertStructuredContentObject enforces MCP CallToolResult.structuredContent
// is a JSON object (Record<string, unknown>), never an array or primitive.
// Successful calls must include a non-nil object; error calls may omit it.
func assertStructuredContentObject(t *testing.T, res *mcpsdk.CallToolResult) {
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
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("StructuredContent must be a JSON object, got %s", raw)
	}
	if res.IsError {
		return
	}
	text := toolText(t, res)
	var textMap map[string]any
	if err := json.Unmarshal([]byte(text), &textMap); err != nil {
		t.Fatalf("content text must be a JSON object matching structuredContent; text=%s sc=%s", text, raw)
	}
}

func assertErrorJSON(t *testing.T, raw []byte) {
	t.Helper()
	var er struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &er); err != nil {
		t.Fatalf("ErrorResponse decode: %v body=%s", err, raw)
	}
	if er.Code == "" || er.Message == "" || er.RequestID == "" {
		t.Fatalf("incomplete ErrorResponse: %+v", er)
	}
}

func postJSON(t *testing.T, method, url, bearer string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
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

func registerLogin(t *testing.T, base, email, password, tenant string) string {
	t.Helper()
	code, raw := postJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": password, "tenant_name": tenant,
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, raw)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sess.AccessToken, "dj_ses_") {
		t.Fatalf("session prefix: %q", sess.AccessToken)
	}
	return sess.AccessToken
}

func issuePAT(t *testing.T, base, session string) string {
	t.Helper()
	code, raw := postJSON(t, http.MethodPost, base+"/v1/api-keys", session, map[string]any{
		"name": "e2e-pat",
	})
	if code != http.StatusCreated {
		t.Fatalf("create api-key: %d %s", code, raw)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, "dj_pat_") {
		t.Fatalf("pat prefix: %q", created.Token)
	}
	return created.Token
}

func heartbeat(t *testing.T, base, agentTok, machineID string, applied int) (int, map[string]any) {
	t.Helper()
	code, raw := postJSON(t, http.MethodPost, base+"/v1/heartbeat", agentTok, map[string]any{
		"machine_id":             machineID,
		"agent_version":          "0.0.1-e2e",
		"hostname":               "e2e-linux",
		"os_version":             "linux",
		"applied_policy_version": applied,
		"time_zone":              "UTC",
		"utc_offset_minutes":     0,
	})
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return code, body
}

func claimAndComplete(t *testing.T, base, agentTok, machineID, jobID, stdout string, exitCode int32) {
	t.Helper()
	code, raw := postJSON(t, http.MethodPost, base+"/v1/jobs/"+jobID+"/claim", agentTok, map[string]any{
		"machine_id": machineID,
	})
	if code != http.StatusOK {
		t.Fatalf("claim: %d %s", code, raw)
	}
	code, raw = postJSON(t, http.MethodPost, base+"/v1/jobs/"+jobID+"/result", agentTok, map[string]any{
		"machine_id":  machineID,
		"status":      "succeeded",
		"exit_code":   exitCode,
		"stdout":      stdout,
		"stderr":      "",
		"duration_ms": 12,
	})
	if code != http.StatusNoContent {
		t.Fatalf("result: %d %s", code, raw)
	}
	// Best-effort event ingest matching the Windows agent.
	_, _ = postJSON(t, http.MethodPost, base+"/v1/events", agentTok, map[string]any{
		"machine_id": machineID,
		"events": []map[string]any{{
			"event_id": uuid.NewString(),
			"ts":       time.Now().UTC().Format(time.RFC3339Nano),
			"type":     "job.result",
			"payload":  map[string]any{"job_id": jobID, "status": "succeeded", "exit_code": exitCode},
		}},
	})
}

// TestTC16_FullStackAccountTokenAgentMCPScript covers Issue #16 Linux E2E chain.
func TestTC16_FullStackAccountTokenAgentMCPScript(t *testing.T) {
	st := openStore(t)
	base := startLiveServer(t, st)

	emailA := "a-" + uuid.NewString()[:8] + "@example.com"
	pass := "E2e!pass-" + uuid.NewString()[:8]
	sessionA := registerLogin(t, base, emailA, pass, "tenant-a-"+uuid.NewString()[:8])
	patA := issuePAT(t, base, sessionA)

	mcpA := mcpConnect(t, base, patA)

	// Empty-list contract: structuredContent is an object with machines:[] / tokens:[].
	emptyMachines := decodeToolJSON(t, callTool(t, mcpA, "list_machines", nil)).(map[string]any)
	if arr, _ := emptyMachines["machines"].([]any); arr == nil || len(arr) != 0 {
		t.Fatalf("empty list_machines = %v (want machines:[])", emptyMachines)
	}
	emptyTokens := decodeToolJSON(t, callTool(t, mcpA, "manage_agent_tokens", map[string]any{
		"action": "list",
	})).(map[string]any)
	if arr, _ := emptyTokens["tokens"].([]any); arr == nil || len(arr) != 0 {
		t.Fatalf("empty manage_agent_tokens list = %v (want tokens:[])", emptyTokens)
	}

	created := decodeToolJSON(t, callTool(t, mcpA, "manage_agent_tokens", map[string]any{
		"action": "create", "name": "agent-a",
	})).(map[string]any)
	agentTok, _ := created["token"].(string)
	agentTokID, _ := created["id"].(string)
	if !strings.HasPrefix(agentTok, "dj_agt_") {
		t.Fatalf("agent token: %v", created)
	}

	machineID := uuid.NewString()
	code, hb := heartbeat(t, base, agentTok, machineID, 0)
	if code != http.StatusOK {
		t.Fatalf("heartbeat: %d %v", code, hb)
	}

	// Facts / usage contracts are mounted but not yet implemented (501 JSON).
	factsBody := map[string]any{
		"machine_id":         machineID,
		"stream_id":          uuid.NewString(),
		"seq":                1,
		"sent_at":            time.Now().UTC().Format(time.RFC3339),
		"time_zone":          "UTC",
		"utc_offset_minutes": 0,
		"facts":              []any{},
	}
	code, raw := postJSON(t, http.MethodPost, base+"/v1/facts", agentTok, factsBody)
	if code != http.StatusNotImplemented {
		t.Fatalf("POST /v1/facts want 501, got %d %s", code, raw)
	}
	assertErrorJSON(t, raw)
	code, raw = postJSON(t, http.MethodPost, base+"/v1/facts", "", factsBody)
	if code != http.StatusUnauthorized {
		t.Fatalf("POST /v1/facts unauthenticated want 401, got %d %s", code, raw)
	}
	code, raw = postJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=2026-11-28", patA, nil)
	if code != http.StatusNotImplemented {
		t.Fatalf("GET usage want 501, got %d %s", code, raw)
	}
	assertErrorJSON(t, raw)
	code, raw = postJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=2026-11-28", "", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("GET usage unauthenticated want 401, got %d %s", code, raw)
	}

	// Production-path contract validation (before 501 stub).
	code, raw = postJSON(t, http.MethodPost, base+"/v1/heartbeat", agentTok, map[string]any{
		"machine_id": machineID, "agent_version": "0.0.1-e2e", "hostname": "e2e-linux",
		"os_version": "linux", "applied_policy_version": 0,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("heartbeat missing time_zone want 400, got %d %s", code, raw)
	}
	assertErrorJSON(t, raw)
	badFacts := map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 0,
		"sent_at": time.Now().UTC().Format(time.RFC3339), "time_zone": "UTC",
		"utc_offset_minutes": 0, "facts": []any{},
	}
	code, raw = postJSON(t, http.MethodPost, base+"/v1/facts", agentTok, badFacts)
	if code != http.StatusBadRequest {
		t.Fatalf("facts seq=0 want 400, got %d %s", code, raw)
	}
	assertErrorJSON(t, raw)
	code, raw = postJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=", patA, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("usage empty date want 400, got %d %s", code, raw)
	}
	assertErrorJSON(t, raw)

	machinesObj := decodeToolJSON(t, callTool(t, mcpA, "list_machines", nil)).(map[string]any)
	machines, _ := machinesObj["machines"].([]any)
	if len(machines) != 1 {
		t.Fatalf("list_machines = %v", machinesObj)
	}

	pol := decodeToolJSON(t, callTool(t, mcpA, "set_policy", map[string]any{
		"machine_id":       machineID,
		"expected_version": 0,
		"document":         map[string]any{"block": []string{"game.exe"}},
	})).(map[string]any)
	if int(pol["version"].(float64)) != 1 {
		t.Fatalf("policy version = %v", pol["version"])
	}

	code, hb = heartbeat(t, base, agentTok, machineID, 0)
	if code != http.StatusOK {
		t.Fatalf("heartbeat after policy: %d", code)
	}
	if int(hb["desired_policy_version"].(float64)) != 1 {
		t.Fatalf("desired_policy_version = %v, want 1", hb["desired_policy_version"])
	}

	stdoutMarker := "hello-from-linux-e2e-" + uuid.NewString()[:8]
	job := decodeToolJSON(t, callTool(t, mcpA, "run_script", map[string]any{
		"machine_id": machineID,
		"type":       "cmd",
		"payload":    "echo " + stdoutMarker,
		"expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})).(map[string]any)
	jobID, _ := job["id"].(string)
	if jobID == "" {
		t.Fatalf("missing job id: %v", job)
	}

	code, hb = heartbeat(t, base, agentTok, machineID, 1)
	if code != http.StatusOK {
		t.Fatalf("heartbeat pending: %d", code)
	}
	pending, _ := hb["pending_job_ids"].([]any)
	found := false
	for _, id := range pending {
		if id == jobID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("pending_job_ids missing %s: %v", jobID, pending)
	}

	// Agent client claims and posts real stdout/exit_code (simulates Windows agent).
	out, err := exec.Command("sh", "-c", "echo "+stdoutMarker).CombinedOutput()
	if err != nil {
		t.Fatalf("local echo: %v", err)
	}
	claimAndComplete(t, base, agentTok, machineID, jobID, string(out), 0)

	got := decodeToolJSON(t, callTool(t, mcpA, "get_job", map[string]any{"job_id": jobID})).(map[string]any)
	if got["status"] != "succeeded" {
		t.Fatalf("job status = %v", got["status"])
	}
	stdout, _ := got["stdout"].(string)
	if !strings.Contains(stdout, stdoutMarker) {
		t.Fatalf("stdout missing marker: %q", stdout)
	}
	if int(got["exit_code"].(float64)) != 0 {
		t.Fatalf("exit_code = %v", got["exit_code"])
	}

	events := decodeToolJSON(t, callTool(t, mcpA, "list_events", map[string]any{
		"machine_id": machineID,
	})).(map[string]any)
	evts, _ := events["events"].([]any)
	if len(evts) == 0 {
		t.Fatalf("expected events, got %v", events)
	}

	// Cross-tenant isolation: Tenant B cannot see/control Tenant A's assets.
	emailB := "b-" + uuid.NewString()[:8] + "@example.com"
	sessionB := registerLogin(t, base, emailB, pass+"B", "tenant-b-"+uuid.NewString()[:8])
	patB := issuePAT(t, base, sessionB)
	mcpB := mcpConnect(t, base, patB)

	listBObj := decodeToolJSON(t, callTool(t, mcpB, "list_machines", nil)).(map[string]any)
	listB, _ := listBObj["machines"].([]any)
	if len(listB) != 0 {
		t.Fatalf("tenant B saw machines: %v", listBObj)
	}
	res := callTool(t, mcpB, "get_job", map[string]any{"job_id": jobID})
	if !res.IsError {
		t.Fatal("tenant B get_job should fail")
	}
	assertStructuredContentObject(t, res)
	res = callTool(t, mcpB, "list_events", map[string]any{"machine_id": machineID})
	if res.IsError {
		// error is fine
	} else {
		page := decodeToolJSON(t, res).(map[string]any)
		if ev, _ := page["events"].([]any); len(ev) != 0 {
			t.Fatalf("tenant B saw events: %v", page)
		}
	}
	res = callTool(t, mcpB, "manage_agent_tokens", map[string]any{
		"action": "revoke", "id": agentTokID,
	})
	if !res.IsError {
		t.Fatal("cross-tenant revoke should fail")
	}

	// Token revocation → subsequent agent use is 401.
	_ = decodeToolJSON(t, callTool(t, mcpA, "manage_agent_tokens", map[string]any{
		"action": "revoke", "id": agentTokID,
	}))
	code, _ = heartbeat(t, base, agentTok, machineID, 1)
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked agent heartbeat status=%d, want 401", code)
	}

	// Revoked PAT also yields HTTP 401 at the MCP transport.
	req, err := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+patA)
	// Revoke the PAT via ControlAPI session.
	keysCode, keysRaw := postJSON(t, http.MethodGet, base+"/v1/api-keys", sessionA, nil)
	if keysCode != http.StatusOK {
		t.Fatalf("list api-keys: %d %s", keysCode, keysRaw)
	}
	var keys []map[string]any
	if err := json.Unmarshal(keysRaw, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("expected at least one api key")
	}
	patID, _ := keys[0]["id"].(string)
	delCode, delRaw := postJSON(t, http.MethodDelete, base+"/v1/api-keys/"+patID, sessionA, nil)
	if delCode != http.StatusNoContent {
		t.Fatalf("delete api-key: %d %s", delCode, delRaw)
	}
	mcpRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mcpRes.Body.Close() }()
	if mcpRes.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(mcpRes.Body)
		t.Fatalf("revoked PAT mcp status=%d body=%s", mcpRes.StatusCode, body)
	}
}

// TestTC16_FailClosedMongoUnavailable covers Issue #16 AC #5.
func TestTC16_FailClosedMongoUnavailable(t *testing.T) {
	mongoURI(t) // require the suite's mongo env so this job is intentional

	// 1) Startup must fail cleanly when MongoDB is unreachable (no listener).
	serverBin := buildServer(t)
	badURI := "mongodb://127.0.0.1:1"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, serverBin)
	cmd.Env = append(os.Environ(),
		"DAD_JOKE_MONGODB_URI="+badURI,
		"DAD_JOKE_HTTP_ADDR=127.0.0.1:0",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("server started with unreachable mongo; want non-zero exit")
	}
	if !strings.Contains(stderr.String(), "open store") && !strings.Contains(stderr.String(), "mongo") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}

	// 2) /healthz returns 503 when the store Ping fails after start.
	st := openStore(t)
	base := startLiveServer(t, st)
	code, raw := postJSON(t, http.MethodGet, base+"/healthz", "", nil)
	if code != http.StatusOK {
		t.Fatalf("healthz before close: %d %s", code, raw)
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("close store: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, raw = postJSON(t, http.MethodGet, base+"/healthz", "", nil)
		if code == http.StatusServiceUnavailable {
			var body map[string]string
			_ = json.Unmarshal(raw, &body)
			if body["status"] != "unavailable" {
				t.Fatalf("body = %s", raw)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("healthz never went unavailable; last=%d %s", code, raw)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func buildServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "server")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/server")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build server: %v\n%s", err, raw)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// e2e/linux → repo root
	root := filepath.Clean(filepath.Join(wd, "../.."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root from %s: %v", wd, err)
	}
	return root
}

func TestTC16_HealthzOK(t *testing.T) {
	st := openStore(t)
	base := startLiveServer(t, st)
	code, raw := postJSON(t, http.MethodGet, base+"/healthz", "", nil)
	if code != http.StatusOK {
		t.Fatalf("healthz: %d %s", code, raw)
	}
	if !strings.Contains(string(raw), `"ok"`) {
		t.Fatalf("body=%s", raw)
	}
}
