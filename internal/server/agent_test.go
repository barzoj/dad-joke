package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestAgentHeartbeat_Authenticated(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "hb-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "agent"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	machineID := uuid.NewString()
	hb, _ := json.Marshal(map[string]any{
		"machine_id":             machineID,
		"agent_version":          "0.0.1",
		"hostname":               "kid-pc",
		"os_version":             "Windows 11 Home",
		"applied_policy_version": 1,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(hb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+created.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat status = %d", res.StatusCode)
	}
	var out struct {
		PendingJobIds        []string `json:"pending_job_ids"`
		DesiredPolicyVersion int32    `json:"desired_policy_version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.PendingJobIds == nil {
		t.Fatal("pending_job_ids should be non-nil empty slice")
	}
}
