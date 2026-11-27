package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func createAgentToken(t *testing.T, base, pat string) string {
	t.Helper()
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "agent"})
	if code != http.StatusCreated {
		t.Fatalf("create agent token: %d %s", code, body)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	return created.Token
}

func postHeartbeat(t *testing.T, base, agentToken string, body map[string]any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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

func TestAgentHeartbeat_Authenticated(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "hb-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	token := createAgentToken(t, base, pat)

	machineID := uuid.NewString()
	code, body := postHeartbeat(t, base, token, map[string]any{
		"machine_id":             machineID,
		"agent_version":          "0.0.1",
		"hostname":               "kid-pc",
		"os_version":             "Windows 11 Home",
		"applied_policy_version": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("heartbeat status = %d %s", code, body)
	}
	var out struct {
		ServerTime           time.Time `json:"server_time"`
		PendingJobIds        []string  `json:"pending_job_ids"`
		DesiredPolicyVersion int32     `json:"desired_policy_version"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.PendingJobIds == nil {
		t.Fatal("pending_job_ids should be non-nil empty slice")
	}
	if len(out.PendingJobIds) != 0 {
		t.Fatalf("pending_job_ids = %#v", out.PendingJobIds)
	}
	if out.DesiredPolicyVersion != 0 {
		t.Fatalf("desired_policy_version = %d, want 0", out.DesiredPolicyVersion)
	}
	if !out.ServerTime.Equal(now) {
		t.Fatalf("server_time = %v, want %v", out.ServerTime, now)
	}

	got, err := st.Machines().Get(context.Background(), tenantID, machineID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hostname != "kid-pc" || got.AppliedPolicyVersion != 1 || !got.LastSeenAt.Equal(now) {
		t.Fatalf("stored machine = %+v", got)
	}
}

func TestAgentHeartbeat_CrossTenantIsolation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantA, patA := seedTenantPAT(t, st, "hbA-"+uuid.NewString()[:8])
	tenantB, patB := seedTenantPAT(t, st, "hbB-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	tokenA := createAgentToken(t, base, patA)
	tokenB := createAgentToken(t, base, patB)

	machineID := uuid.NewString()
	code, body := postHeartbeat(t, base, tokenA, map[string]any{
		"machine_id": machineID, "agent_version": "a1", "hostname": "host-a",
		"os_version": "WinA", "applied_policy_version": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("tenant A heartbeat: %d %s", code, body)
	}
	code, body = postHeartbeat(t, base, tokenB, map[string]any{
		"machine_id": machineID, "agent_version": "b1", "hostname": "host-b",
		"os_version": "WinB", "applied_policy_version": 9,
	})
	if code != http.StatusOK {
		t.Fatalf("tenant B heartbeat: %d %s", code, body)
	}

	a, err := st.Machines().Get(context.Background(), tenantA, machineID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Machines().Get(context.Background(), tenantB, machineID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Hostname != "host-a" || a.AgentVersion != "a1" || a.AppliedPolicyVersion != 1 {
		t.Fatalf("tenant A machine polluted: %+v", a)
	}
	if b.Hostname != "host-b" || b.AgentVersion != "b1" || b.AppliedPolicyVersion != 9 {
		t.Fatalf("tenant B machine unexpected: %+v", b)
	}
}

func TestAgentHeartbeat_InvalidMachineID400(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "hbinv-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	token := createAgentToken(t, base, pat)

	cases := []struct {
		name string
		id   any
	}{
		{name: "malformed", id: "not-a-uuid"},
		{name: "empty", id: ""},
		{name: "nil_uuid", id: "00000000-0000-0000-0000-000000000000"},
		{name: "uuid_v1", id: "550e8400-e29b-11d4-a716-446655440000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, body := postHeartbeat(t, base, token, map[string]any{
				"machine_id": tc.id, "agent_version": "0.0.1", "hostname": "pc",
				"os_version": "Win", "applied_policy_version": 0,
			})
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d %s", code, body)
			}
			assertErrorResponse(t, body)
		})
	}
}

func TestAgentHeartbeat_UpsertInPlaceNoExtraDocs(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "hbup-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	token := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	for i, ver := range []string{"0.0.1", "0.0.2", "0.0.3"} {
		code, body := postHeartbeat(t, base, token, map[string]any{
			"machine_id": machineID, "agent_version": ver, "hostname": "pc",
			"os_version": "Win", "applied_policy_version": i,
		})
		if code != http.StatusOK {
			t.Fatalf("heartbeat %d: %d %s", i, code, body)
		}
	}
	listed, err := st.Machines().ListByTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("document count = %d, want 1", len(listed))
	}
	if listed[0].AgentVersion != "0.0.3" || listed[0].AppliedPolicyVersion != 2 {
		t.Fatalf("fields not updated in place: %+v", listed[0])
	}
}
