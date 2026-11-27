package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/audit"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func putPolicy(t *testing.T, base, bearer, machineID string, body any) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodPut, base+"/v1/machines/"+machineID+"/policy", bearer, body)
	return code, raw
}

func getControlPolicy(t *testing.T, base, bearer, machineID string) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/policy", bearer, nil)
	return code, raw
}

func getAgentPolicy(t *testing.T, base, agentToken, machineID string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/v1/policy?machine_id="+machineID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, out
}

func decodePolicyBody(t *testing.T, raw []byte) (version int32, doc map[string]any) {
	t.Helper()
	var out struct {
		Version  int32          `json:"version"`
		Document map[string]any `json:"document"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode policy: %v body=%s", err, raw)
	}
	return out.Version, out.Document
}

// AC1: first write → version 1; subsequent updates increment atomically.
func TestPolicyCRUD_VersionIncrement(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "pol-ver-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	code, body := putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"v": 1},
	})
	if code != http.StatusOK {
		t.Fatalf("first put: %d %s", code, body)
	}
	ver, _ := decodePolicyBody(t, body)
	if ver != 1 {
		t.Fatalf("version = %d, want 1", ver)
	}

	code, body = putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 1,
		"document":         map[string]any{"v": 2},
	})
	if code != http.StatusOK {
		t.Fatalf("second put: %d %s", code, body)
	}
	ver, doc := decodePolicyBody(t, body)
	if ver != 2 {
		t.Fatalf("version = %d, want 2", ver)
	}
	if doc["v"].(float64) != 2 {
		t.Fatalf("document = %#v", doc)
	}

	code, body = getControlPolicy(t, base, pat, machineID)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, body)
	}
	ver, _ = decodePolicyBody(t, body)
	if ver != 2 {
		t.Fatalf("get version = %d, want 2", ver)
	}
}

// AC2: stale expected_version → 409 and no overwrite.
func TestPolicyCRUD_StaleExpectedVersion(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "pol-stale-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	code, body := putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"keep": true},
	})
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}

	code, body = putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 99,
		"document":         map[string]any{"keep": false},
	})
	if code != http.StatusConflict {
		t.Fatalf("stale put: %d %s", code, body)
	}

	got, err := st.Policies().Get(context.Background(), tenantID, machineID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Fatalf("version overwritten to %d", got.Version)
	}
	var doc map[string]any
	if err := json.Unmarshal(got.Document, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["keep"] != true {
		t.Fatalf("document overwritten: %#v", doc)
	}
}

// AC3: heartbeat immediately reflects desired_policy_version after PUT.
func TestPolicyCRUD_HeartbeatReflectsVersion(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "pol-hb-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agentTok := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"ok": true},
	})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}

	code, body = postHeartbeat(t, base, agentTok, map[string]any{
		"machine_id":             machineID,
		"agent_version":          "0.0.1",
		"hostname":               "kid-pc",
		"os_version":             "Windows 11 Home",
		"applied_policy_version": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", code, body)
	}
	var hb struct {
		DesiredPolicyVersion int32 `json:"desired_policy_version"`
	}
	if err := json.Unmarshal(body, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.DesiredPolicyVersion != 1 {
		t.Fatalf("desired_policy_version = %d, want 1", hb.DesiredPolicyVersion)
	}

	code, _ = putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 1,
		"document":         map[string]any{"ok": true, "n": 2},
	})
	if code != http.StatusOK {
		t.Fatalf("update: %d", code)
	}
	code, body = postHeartbeat(t, base, agentTok, map[string]any{
		"machine_id":             machineID,
		"agent_version":          "0.0.1",
		"hostname":               "kid-pc",
		"os_version":             "Windows 11 Home",
		"applied_policy_version": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("heartbeat2: %d %s", code, body)
	}
	if err := json.Unmarshal(body, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.DesiredPolicyVersion != 2 {
		t.Fatalf("desired_policy_version = %d, want 2", hb.DesiredPolicyVersion)
	}
}

// AC4 + AC7: agent gets only own tenant policy; PAT/session → 401; missing/other → 404.
func TestPolicyCRUD_AgentTenantIsolationAndAuth(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, patA := seedTenantPAT(t, st, "polA-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "polB-"+uuid.NewString()[:8])
	secure := false
	base := startTestServer(t, st, server.Config{CookieSecure: &secure})
	agentA := createAgentToken(t, base, patA)
	agentB := createAgentToken(t, base, patB)
	machineID := uuid.NewString()
	otherMachine := uuid.NewString()

	code, body := putPolicy(t, base, patA, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"tenant": "A"},
	})
	if code != http.StatusOK {
		t.Fatalf("put A: %d %s", code, body)
	}

	code, body = getAgentPolicy(t, base, agentA, machineID)
	if code != http.StatusOK {
		t.Fatalf("agent A get: %d %s", code, body)
	}
	_, doc := decodePolicyBody(t, body)
	if doc["tenant"] != "A" {
		t.Fatalf("document = %#v", doc)
	}

	// Cross-tenant: tenant B agent cannot see A's policy (404, not leak).
	code, body = getAgentPolicy(t, base, agentB, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant agent get: %d %s", code, body)
	}

	// Another machine id with no policy → 404.
	code, body = getAgentPolicy(t, base, agentA, otherMachine)
	if code != http.StatusNotFound {
		t.Fatalf("missing machine: %d %s", code, body)
	}

	// PAT on AgentAPI → 401.
	code, body = getAgentPolicy(t, base, patA, machineID)
	if code != http.StatusUnauthorized {
		t.Fatalf("pat on agent: %d %s", code, body)
	}

	// Session on AgentAPI → 401.
	code, _, regBody := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":    "pol-" + uuid.NewString()[:8] + "@example.com",
		"password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, regBody)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(regBody, &sess); err != nil {
		t.Fatal(err)
	}
	code, body = getAgentPolicy(t, base, sess.AccessToken, machineID)
	if code != http.StatusUnauthorized {
		t.Fatalf("session on agent: %d %s", code, body)
	}
}

// AC5: GET unknown machine_id → 404 (never empty policy).
func TestPolicyCRUD_GetUnknown404(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "pol-404-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	code, body := getControlPolicy(t, base, pat, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("control get missing: %d %s", code, body)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "not_found" {
		t.Fatalf("code = %q", errBody.Code)
	}
	if bytes.Contains(body, []byte(`"document"`)) {
		t.Fatalf("404 must not return a policy document: %s", body)
	}
}

// AC6: concurrent PUT with same expected_version → exactly one 200, one 409.
func TestPolicyCRUD_ConcurrentPutRace(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "pol-race-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	code, body := putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"n": 0},
	})
	if code != http.StatusOK {
		t.Fatalf("seed: %d %s", code, body)
	}

	const n = 2
	var (
		wg       sync.WaitGroup
		okCount  atomic.Int32
		conflict atomic.Int32
		start    = make(chan struct{})
		errs     = make(chan string, n)
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			raw, err := json.Marshal(map[string]any{
				"expected_version": 1,
				"document":         map[string]any{"n": i + 1},
			})
			if err != nil {
				errs <- err.Error()
				return
			}
			req, err := http.NewRequest(http.MethodPut, base+"/v1/machines/"+machineID+"/policy", bytes.NewReader(raw))
			if err != nil {
				errs <- err.Error()
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+pat)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			out, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			switch res.StatusCode {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflict.Add(1)
			default:
				errs <- "unexpected status"
				_ = out
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}

	if okCount.Load() != 1 || conflict.Load() != 1 {
		t.Fatalf("ok=%d conflict=%d, want 1/1", okCount.Load(), conflict.Load())
	}
	code, body = getControlPolicy(t, base, pat, machineID)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, body)
	}
	ver, _ := decodePolicyBody(t, body)
	if ver != 2 {
		t.Fatalf("final version = %d, want 2", ver)
	}
}

// AC8: every successful PUT writes an audit log entry.
func TestPolicyCRUD_AuditOnSuccess(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "pol-aud-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	before, err := st.Audits().ListByTenant(context.Background(), tenantID, 100)
	if err != nil {
		t.Fatal(err)
	}

	code, body := putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"a": 1},
	})
	if code != http.StatusOK {
		t.Fatalf("put1: %d %s", code, body)
	}
	code, body = putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 1,
		"document":         map[string]any{"a": 2},
	})
	if code != http.StatusOK {
		t.Fatalf("put2: %d %s", code, body)
	}

	// Stale put must not audit.
	code, _ = putPolicy(t, base, pat, machineID, map[string]any{
		"expected_version": 1,
		"document":         map[string]any{"a": 3},
	})
	if code != http.StatusConflict {
		t.Fatalf("stale: %d", code)
	}

	after, err := st.Audits().ListByTenant(context.Background(), tenantID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var policyAudits int
	for _, e := range after[len(before):] {
		if e.Action == audit.ActionPolicyCreate {
			policyAudits++
			if e.Resource != "policy:"+machineID {
				t.Fatalf("resource = %q", e.Resource)
			}
		}
	}
	if policyAudits != 2 {
		t.Fatalf("policy audits = %d, want 2 (failed put must not audit)", policyAudits)
	}
}

// AC9: invalid JSON → 400; oversized body → 413.
func TestPolicyCRUD_InvalidAndOversized(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "pol-bad-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	req, err := http.NewRequest(http.MethodPut, base+"/v1/machines/"+machineID+"/policy",
		strings.NewReader(`{"expected_version":0,"document":`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pat)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid json: %d %s", res.StatusCode, raw)
	}

	huge := `{"expected_version":0,"document":{"pad":"` + strings.Repeat("x", 2<<20) + `"}}`
	req, err = http.NewRequest(http.MethodPut, base+"/v1/machines/"+machineID+"/policy", strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pat)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized: %d %s", res.StatusCode, raw)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Code != "payload_too_large" {
		t.Fatalf("code = %q, want payload_too_large", errBody.Code)
	}
}

func TestPolicyCRUD_ControlCrossTenant404(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, patA := seedTenantPAT(t, st, "polCtlA-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "polCtlB-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	code, body := putPolicy(t, base, patA, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"x": 1},
	})
	if code != http.StatusOK {
		t.Fatalf("put: %d %s", code, body)
	}
	code, body = getControlPolicy(t, base, patB, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant get: %d %s", code, body)
	}
}

func TestPolicyCRUD_SessionCanPut(t *testing.T) {
	t.Parallel()
	st := memory.New()
	secure := false
	base := startTestServer(t, st, server.Config{CookieSecure: &secure})
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":    "sess-pol-" + uuid.NewString()[:8] + "@example.com",
		"password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatal(err)
	}
	machineID := uuid.NewString()
	code, body = putPolicy(t, base, sess.AccessToken, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"via": "session"},
	})
	if code != http.StatusOK {
		t.Fatalf("session put: %d %s", code, body)
	}
}

// Ensure store.Policy remains tenant-scoped at the DAL for memory (AC4 unit).
func TestPolicyStore_CrossTenantIsolation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	ctx := context.Background()
	machineID := uuid.NewString()
	pol := &store.Policy{TenantID: "ten-a", MachineID: machineID, Document: json.RawMessage(`{"a":1}`)}
	if err := st.Policies().Put(ctx, pol, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Policies().Get(ctx, "ten-b", machineID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant get: %v, want ErrNotFound", err)
	}
}
