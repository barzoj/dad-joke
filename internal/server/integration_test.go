package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/server"
	mongostore "github.com/barzoj/dad-joke/internal/store/mongo"
	"github.com/google/uuid"
)

func openMongoStore(t *testing.T) *mongostore.Store {
	t.Helper()
	uri := strings.TrimSpace(os.Getenv("DAD_JOKE_MONGODB_URI"))
	if uri == "" {
		t.Skip("DAD_JOKE_MONGODB_URI not set")
	}
	dbName := "dadjoke_auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	st := openMongoWithRetry(t, ctx, mongostore.Config{URI: uri, Database: dbName})
	t.Cleanup(func() {
		_ = st.Close(context.Background())
	})
	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	return st
}

// openMongoWithRetry retries Open on transient ping / server-selection errors
// while the CI Mongo service is still warming up under parallel go test load.
func openMongoWithRetry(t *testing.T, ctx context.Context, cfg mongostore.Config) *mongostore.Store {
	t.Helper()
	const maxAttempts = 5
	backoff := 250 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		st, err := mongostore.Open(ctx, cfg)
		if err == nil {
			return st
		}
		lastErr = err
		if !isTransientMongoOpenError(err) {
			t.Fatalf("Open: %v", err)
		}
		if attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Open: %v (last: %v)", ctx.Err(), lastErr)
		case <-time.After(backoff):
		}
		if backoff < 4*time.Second {
			backoff *= 2
		}
	}
	t.Fatalf("Open after %d attempts: %v", maxAttempts, lastErr)
	return nil
}

func isTransientMongoOpenError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"server selection",
		"connection refused",
		"i/o timeout",
		"no reachable servers",
		"context deadline exceeded",
		"connection reset",
		"broken pipe",
		"temporarily unavailable",
		"wait queue timeout",
		"topology is closed",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func TestIntegration_AuthTokenCRUDAndRevoke(t *testing.T) {
	st := openMongoStore(t)
	res, err := bootstrap.Run(context.Background(), st, "mongo-auth-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", res.RawKey, map[string]any{"name": "agent"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	code, _, body = doJSON(t, http.MethodDelete, base+"/v1/agent-tokens/"+created.ID, res.RawKey, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", code, body)
	}

	hb, _ := json.Marshal(map[string]any{
		"machine_id":             uuid.NewString(),
		"agent_version":          "0.0.1",
		"hostname":               "pc",
		"os_version":             "Windows 11",
		"applied_policy_version": 0,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(hb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+created.Token)
	httpRes, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = httpRes.Body.Close() }()
	if httpRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d", httpRes.StatusCode)
	}

	audits, err := st.Audits().ListByTenant(context.Background(), res.TenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawIssue, sawRevoke bool
	for _, a := range audits {
		switch a.Action {
		case "token.issue":
			if a.Resource == "token:"+created.ID {
				sawIssue = true
			}
		case "token.revoke":
			if a.Resource == "token:"+created.ID {
				sawRevoke = true
			}
		}
	}
	if !sawIssue || !sawRevoke {
		t.Fatalf("audits missing issue/revoke: %+v", audits)
	}
}

func TestIntegration_CrossTenantDelete404(t *testing.T) {
	st := openMongoStore(t)
	a, err := bootstrap.Run(context.Background(), st, "mongo-a-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	b, err := bootstrap.Run(context.Background(), st, "mongo-b-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/api-keys", a.RawKey, map[string]any{"name": "only-a"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	code, _, body = doJSON(t, http.MethodDelete, base+"/v1/api-keys/"+created.ID, b.RawKey, nil)
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant delete: %d %s", code, body)
	}
}

func TestIntegration_HeartbeatMachinesTenantIsolationAndUpsert(t *testing.T) {
	st := openMongoStore(t)
	a, err := bootstrap.Run(context.Background(), st, "mongo-hb-a-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	b, err := bootstrap.Run(context.Background(), st, "mongo-hb-b-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	base := startTestServer(t, st, server.Config{})
	tokenA := createAgentToken(t, base, a.RawKey)
	tokenB := createAgentToken(t, base, b.RawKey)

	machineID := uuid.NewString()
	code, body := postHeartbeat(t, base, tokenA, map[string]any{
		"machine_id": machineID, "agent_version": "a1", "hostname": "host-a",
		"os_version": "WinA", "applied_policy_version": 1,
	})
	if code != http.StatusOK {
		t.Fatalf("tenant A heartbeat: %d %s", code, body)
	}
	code, body = postHeartbeat(t, base, tokenB, map[string]any{
		"machine_id": machineID, "agent_version": "b9", "hostname": "host-b",
		"os_version": "WinB", "applied_policy_version": 9,
	})
	if code != http.StatusOK {
		t.Fatalf("tenant B heartbeat: %d %s", code, body)
	}

	// Spoof: Tenant B heartbeats same machine_id again — must not touch Tenant A.
	code, body = postHeartbeat(t, base, tokenB, map[string]any{
		"machine_id": machineID, "agent_version": "b-spoof", "hostname": "spoofed",
		"os_version": "WinB", "applied_policy_version": 99,
	})
	if code != http.StatusOK {
		t.Fatalf("spoof heartbeat: %d %s", code, body)
	}

	gotA, err := st.Machines().Get(context.Background(), a.TenantID, machineID)
	if err != nil {
		t.Fatal(err)
	}
	if gotA.Hostname != "host-a" || gotA.AgentVersion != "a1" || gotA.AppliedPolicyVersion != 1 {
		t.Fatalf("tenant A polluted: %+v", gotA)
	}
	gotB, err := st.Machines().Get(context.Background(), b.TenantID, machineID)
	if err != nil {
		t.Fatal(err)
	}
	if gotB.Hostname != "spoofed" || gotB.AgentVersion != "b-spoof" {
		t.Fatalf("tenant B not updated: %+v", gotB)
	}

	// Repeated heartbeats for A must not create extra documents.
	for i := 0; i < 3; i++ {
		code, body = postHeartbeat(t, base, tokenA, map[string]any{
			"machine_id": machineID, "agent_version": "a-refresh", "hostname": "host-a",
			"os_version": "WinA", "applied_policy_version": 2,
		})
		if code != http.StatusOK {
			t.Fatalf("repeat heartbeat: %d %s", code, body)
		}
	}
	listedA, err := st.Machines().ListByTenant(context.Background(), a.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedA) != 1 {
		t.Fatalf("tenant A document count = %d, want 1", len(listedA))
	}
	if listedA[0].AgentVersion != "a-refresh" || listedA[0].AppliedPolicyVersion != 2 {
		t.Fatalf("in-place update failed: %+v", listedA[0])
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID, b.RawKey, nil)
	if code != http.StatusOK {
		t.Fatalf("B get own machine: %d %s", code, body)
	}
	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines", a.RawKey, nil)
	if code != http.StatusOK {
		t.Fatalf("A list: %d %s", code, body)
	}
	var listA []map[string]any
	if err := json.Unmarshal(body, &listA); err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0]["machine_id"] != machineID || listA[0]["hostname"] != "host-a" {
		t.Fatalf("A list unexpected: %s", body)
	}
}
