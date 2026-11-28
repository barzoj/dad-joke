package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/audit"
	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/google/uuid"
)

// Mongo-backed coverage for all T-11 acceptance criteria (HTTP + DAL).
func TestIntegration_PolicyCRUD_AllAcceptanceCriteria(t *testing.T) {
	st := openMongoStore(t)
	a, err := bootstrap.Run(context.Background(), st, "mongo-pol-a-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	b, err := bootstrap.Run(context.Background(), st, "mongo-pol-b-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 16, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agentA := createAgentToken(t, base, a.RawKey)
	agentB := createAgentToken(t, base, b.RawKey)
	machineID := uuid.NewString()
	otherMachine := uuid.NewString()

	// AC5: GET unknown → 404
	code, body := getControlPolicy(t, base, a.RawKey, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("AC5 control get missing: %d %s", code, body)
	}

	// AC1: first write → version 1
	code, body = putPolicy(t, base, a.RawKey, machineID, map[string]any{
		"expected_version": 0,
		"document":         map[string]any{"block": []any{"notepad.exe"}},
	})
	if code != http.StatusOK {
		t.Fatalf("AC1 first put: %d %s", code, body)
	}
	ver, _ := decodePolicyBody(t, body)
	if ver != 1 {
		t.Fatalf("AC1 version = %d, want 1", ver)
	}

	// AC1: update increments
	code, body = putPolicy(t, base, a.RawKey, machineID, map[string]any{
		"expected_version": 1,
		"document":         map[string]any{"block": []any{"notepad.exe", "cmd.exe"}},
	})
	if code != http.StatusOK {
		t.Fatalf("AC1 second put: %d %s", code, body)
	}
	ver, _ = decodePolicyBody(t, body)
	if ver != 2 {
		t.Fatalf("AC1 version = %d, want 2", ver)
	}

	// AC2: stale → 409, no overwrite
	code, body = putPolicy(t, base, a.RawKey, machineID, map[string]any{
		"expected_version": 1,
		"document":         map[string]any{"block": []any{"evil"}},
	})
	if code != http.StatusConflict {
		t.Fatalf("AC2 stale: %d %s", code, body)
	}
	got, err := st.Policies().Get(context.Background(), a.TenantID, machineID)
	if err != nil || got.Version != 2 {
		t.Fatalf("AC2 overwritten: %v %+v", err, got)
	}

	// AC3: heartbeat reflects desired_policy_version immediately
	code, body = postHeartbeat(t, base, agentA, map[string]any{
		"machine_id": machineID, "agent_version": "0.0.1", "hostname": "kid",
		"os_version": "Win", "applied_policy_version": 0,
		"time_zone":          "UTC",
		"utc_offset_minutes": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("AC3 heartbeat: %d %s", code, body)
	}
	var hb struct {
		DesiredPolicyVersion int32 `json:"desired_policy_version"`
	}
	if err := json.Unmarshal(body, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.DesiredPolicyVersion != 2 {
		t.Fatalf("AC3 desired_policy_version = %d, want 2", hb.DesiredPolicyVersion)
	}

	// AC4 / AC7: agent own policy OK; cross-tenant / other machine 404; PAT/session 401
	code, body = getAgentPolicy(t, base, agentA, machineID)
	if code != http.StatusOK {
		t.Fatalf("AC4 agent get: %d %s", code, body)
	}
	code, body = getAgentPolicy(t, base, agentB, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("AC4 cross-tenant: %d %s", code, body)
	}
	code, body = getAgentPolicy(t, base, agentA, otherMachine)
	if code != http.StatusNotFound {
		t.Fatalf("AC7 other machine: %d %s", code, body)
	}
	code, body = getAgentPolicy(t, base, a.RawKey, machineID)
	if code != http.StatusUnauthorized {
		t.Fatalf("AC7 PAT: %d %s", code, body)
	}

	// AC8: audit entries for successful puts (2), not for conflict
	entries, err := st.Audits().ListByTenant(context.Background(), a.TenantID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var policyAudits int
	for _, e := range entries {
		if e.Action == audit.ActionPolicyCreate && e.Resource == "policy:"+machineID {
			policyAudits++
		}
	}
	if policyAudits < 2 {
		t.Fatalf("AC8 policy audits = %d, want >= 2", policyAudits)
	}

	// AC9: invalid JSON 400; oversized 413
	req, err := http.NewRequest(http.MethodPut, base+"/v1/machines/"+machineID+"/policy",
		strings.NewReader(`{"expected_version":2,"document":`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.RawKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("AC9 invalid json: %d %s", res.StatusCode, raw)
	}
	huge := `{"expected_version":2,"document":{"pad":"` + strings.Repeat("y", 2<<20) + `"}}`
	req, err = http.NewRequest(http.MethodPut, base+"/v1/machines/"+machineID+"/policy", strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.RawKey)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("AC9 oversized: %d %s", res.StatusCode, raw)
	}
}

func TestIntegration_PolicyConcurrentPutRace(t *testing.T) {
	st := openMongoStore(t)
	boot, err := bootstrap.Run(context.Background(), st, "mongo-pol-race-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	base := startTestServer(t, st, server.Config{})
	machineID := uuid.NewString()

	code, body := putPolicy(t, base, boot.RawKey, machineID, map[string]any{
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
			req, err := http.NewRequest(http.MethodPut, base+"/v1/machines/"+machineID+"/policy", strings.NewReader(string(raw)))
			if err != nil {
				errs <- err.Error()
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+boot.RawKey)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			_, _ = io.ReadAll(res.Body)
			_ = res.Body.Close()
			switch res.StatusCode {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflict.Add(1)
			default:
				errs <- "unexpected status"
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
		t.Fatalf("AC6 ok=%d conflict=%d, want 1/1", okCount.Load(), conflict.Load())
	}
}
