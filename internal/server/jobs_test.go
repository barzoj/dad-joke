package server_test

import (
	"context"
	"encoding/json"
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

func createJob(t *testing.T, base, bearer, machineID string, body any) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodPost, base+"/v1/machines/"+machineID+"/jobs", bearer, body)
	return code, raw
}

func listJobs(t *testing.T, base, bearer, machineID string) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/jobs", bearer, nil)
	return code, raw
}

func getJob(t *testing.T, base, bearer, jobID string) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodGet, base+"/v1/jobs/"+jobID, bearer, nil)
	return code, raw
}

func cancelJob(t *testing.T, base, bearer, jobID string) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodPost, base+"/v1/jobs/"+jobID+"/cancel", bearer, nil)
	return code, raw
}

func claimJob(t *testing.T, base, agentToken, jobID, machineID string) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodPost, base+"/v1/jobs/"+jobID+"/claim", agentToken, map[string]any{
		"machine_id": machineID,
	})
	return code, raw
}

func postJobResult(t *testing.T, base, agentToken, jobID string, body map[string]any) (int, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodPost, base+"/v1/jobs/"+jobID+"/result", agentToken, body)
	return code, raw
}

func decodeJobID(t *testing.T, raw []byte) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.ID == "" {
		t.Fatalf("decode job id: %v body=%s", err, raw)
	}
	return out.ID
}

func jobCreateBody(payload string, expiresAt time.Time) map[string]any {
	return map[string]any{
		"type":       "cmd",
		"payload":    payload,
		"expires_at": expiresAt.UTC().Format(time.RFC3339Nano),
	}
}

// AC1: claim pending→running; second claim → 409.
func TestJobs_ClaimAtomicAndSecondClaim409(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-claim-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("echo 1", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, body = claimJob(t, base, agent, jobID, machineID)
	if code != http.StatusOK {
		t.Fatalf("claim: %d %s", code, body)
	}
	var claimed struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.Status != "running" {
		t.Fatalf("status = %q", claimed.Status)
	}

	code, body = claimJob(t, base, agent, jobID, machineID)
	if code != http.StatusConflict {
		t.Fatalf("second claim: %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

// AC2: cancel pending; result on canceled/finished → 409.
func TestJobs_CancelPendingAndResultConflict(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-cancel-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("echo cancel", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, body = cancelJob(t, base, pat, jobID)
	if code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", code, body)
	}

	code, body = postJobResult(t, base, agent, jobID, map[string]any{
		"machine_id": machineID,
		"status":     "succeeded",
		"exit_code":  0,
		"stdout":     "x",
	})
	if code != http.StatusConflict {
		t.Fatalf("result on canceled: %d %s", code, body)
	}

	code, body = createJob(t, base, pat, machineID, jobCreateBody("echo done", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create2: %d %s", code, body)
	}
	job2 := decodeJobID(t, body)
	code, body = claimJob(t, base, agent, job2, machineID)
	if code != http.StatusOK {
		t.Fatalf("claim2: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agent, job2, map[string]any{
		"machine_id": machineID,
		"status":     "succeeded",
		"exit_code":  0,
	})
	if code != http.StatusNoContent {
		t.Fatalf("result: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agent, job2, map[string]any{
		"machine_id": machineID,
		"status":     "failed",
		"exit_code":  1,
	})
	if code != http.StatusConflict {
		t.Fatalf("result on finished: %d %s", code, body)
	}
}

// AC3+AC7: expired jobs leave pending_job_ids and are not claimable; become timed_out.
func TestJobs_ExpiredNotHandedOrClaimable(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "job-exp-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("alive", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("alive: %d %s", code, body)
	}
	aliveID := decodeJobID(t, body)

	code, body = createJob(t, base, pat, machineID, jobCreateBody("dead", now.Add(-time.Minute)))
	if code != http.StatusCreated {
		t.Fatalf("expired: %d %s", code, body)
	}
	expiredID := decodeJobID(t, body)

	code, hbBody := postHeartbeat(t, base, agent, map[string]any{
		"machine_id": machineID, "agent_version": "1", "hostname": "h",
		"os_version": "Win", "applied_policy_version": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", code, hbBody)
	}
	var hb struct {
		PendingJobIds []string `json:"pending_job_ids"`
	}
	if err := json.Unmarshal(hbBody, &hb); err != nil {
		t.Fatal(err)
	}
	if len(hb.PendingJobIds) != 1 || hb.PendingJobIds[0] != aliveID {
		t.Fatalf("pending_job_ids = %#v", hb.PendingJobIds)
	}

	got, err := st.Jobs().Get(context.Background(), tenantID, expiredID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.JobTimedOut {
		t.Fatalf("expired status = %s, want timed_out", got.Status)
	}

	code, body = claimJob(t, base, agent, expiredID, machineID)
	if code != http.StatusConflict {
		t.Fatalf("claim expired: %d %s", code, body)
	}
}

// AC4: oversized payload / stdout / stderr → 413.
func TestJobs_OversizedPayloadAndOutput(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-size-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	big := strings.Repeat("x", store.JobIOMaxBytes+1)
	code, body := createJob(t, base, pat, machineID, jobCreateBody(big, now.Add(time.Hour)))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized payload: %d %s", code, body)
	}

	code, body = createJob(t, base, pat, machineID, jobCreateBody("ok", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)
	code, body = claimJob(t, base, agent, jobID, machineID)
	if code != http.StatusOK {
		t.Fatalf("claim: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agent, jobID, map[string]any{
		"machine_id": machineID,
		"status":     "succeeded",
		"stdout":     big,
	})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized stdout: %d %s", code, body)
	}
}

// AC5: agent may only claim/complete jobs for its machine_id.
func TestJobs_MachineScopedClaimAndComplete(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-mach-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineA := uuid.NewString()
	machineB := uuid.NewString()

	code, body := createJob(t, base, pat, machineA, jobCreateBody("a", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, body = claimJob(t, base, agent, jobID, machineB)
	if code != http.StatusNotFound {
		t.Fatalf("wrong machine claim: %d %s", code, body)
	}

	code, body = claimJob(t, base, agent, jobID, machineA)
	if code != http.StatusOK {
		t.Fatalf("claim: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agent, jobID, map[string]any{
		"machine_id": machineB,
		"status":     "succeeded",
	})
	if code != http.StatusNotFound {
		t.Fatalf("wrong machine result: %d %s", code, body)
	}
}

// AC6: cross-tenant get/cancel/claim/result → 404 (never 403).
func TestJobs_CrossTenant404(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, patA := seedTenantPAT(t, st, "job-a-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "job-b-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agentB := createAgentToken(t, base, patB)
	machineID := uuid.NewString()

	code, body := createJob(t, base, patA, machineID, jobCreateBody("secret", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, body = getJob(t, base, patB, jobID)
	if code != http.StatusNotFound {
		t.Fatalf("cross get: %d %s", code, body)
	}
	code, body = cancelJob(t, base, patB, jobID)
	if code != http.StatusNotFound {
		t.Fatalf("cross cancel: %d %s", code, body)
	}
	code, body = claimJob(t, base, agentB, jobID, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("cross claim: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agentB, jobID, map[string]any{
		"machine_id": machineID,
		"status":     "succeeded",
	})
	if code != http.StatusNotFound {
		t.Fatalf("cross result: %d %s", code, body)
	}
}

// AC8: cancel running/terminal → 409.
func TestJobs_CancelRunningOrTerminal409(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-canc2-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("run", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)
	code, body = claimJob(t, base, agent, jobID, machineID)
	if code != http.StatusOK {
		t.Fatalf("claim: %d %s", code, body)
	}
	code, body = cancelJob(t, base, pat, jobID)
	if code != http.StatusConflict {
		t.Fatalf("cancel running: %d %s", code, body)
	}

	code, body = postJobResult(t, base, agent, jobID, map[string]any{
		"machine_id": machineID, "status": "failed", "exit_code": 1,
	})
	if code != http.StatusNoContent {
		t.Fatalf("result: %d %s", code, body)
	}
	code, body = cancelJob(t, base, pat, jobID)
	if code != http.StatusConflict {
		t.Fatalf("cancel terminal: %d %s", code, body)
	}
}

// AC9: concurrent double-claim — exactly one succeeds.
func TestJobs_ConcurrentDoubleClaim_Race(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-race-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("race", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	var okCount, conflictCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := claimJob(t, base, agent, jobID, machineID)
			switch code {
			case http.StatusOK:
				okCount.Add(1)
			case http.StatusConflict:
				conflictCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("ok claims = %d, want 1", okCount.Load())
	}
	if conflictCount.Load() != 31 {
		t.Fatalf("conflicts = %d, want 31", conflictCount.Load())
	}
}

// AC10: heartbeat returns newly enqueued pending_job_ids immediately.
func TestJobs_HeartbeatPendingImmediately(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-hb-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("now", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, hbBody := postHeartbeat(t, base, agent, map[string]any{
		"machine_id": machineID, "agent_version": "1", "hostname": "h",
		"os_version": "Win", "applied_policy_version": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", code, hbBody)
	}
	var hb struct {
		PendingJobIds []string `json:"pending_job_ids"`
	}
	if err := json.Unmarshal(hbBody, &hb); err != nil {
		t.Fatal(err)
	}
	if len(hb.PendingJobIds) != 1 || hb.PendingJobIds[0] != jobID {
		t.Fatalf("pending_job_ids = %#v", hb.PendingJobIds)
	}
}

// AC11: audit entries on create, claim, complete, cancel.
func TestJobs_AuditOnTransitions(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "job-aud-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	code, body := createJob(t, base, pat, machineID, jobCreateBody("aud", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, body = claimJob(t, base, agent, jobID, machineID)
	if code != http.StatusOK {
		t.Fatalf("claim: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agent, jobID, map[string]any{
		"machine_id": machineID, "status": "succeeded", "exit_code": 0,
	})
	if code != http.StatusNoContent {
		t.Fatalf("result: %d %s", code, body)
	}

	code, body = createJob(t, base, pat, machineID, jobCreateBody("aud2", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create2: %d %s", code, body)
	}
	job2 := decodeJobID(t, body)
	code, body = cancelJob(t, base, pat, job2)
	if code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", code, body)
	}

	entries, err := st.Audits().ListByTenant(context.Background(), tenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	for _, want := range []string{
		audit.ActionJobCreate, audit.ActionJobClaim, audit.ActionJobComplete, audit.ActionJobCancel,
	} {
		if !seen[want] {
			t.Fatalf("missing audit action %q in %#v", want, seen)
		}
	}
}

func TestJobs_ListAndGet(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "job-list-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	machineID := uuid.NewString()

	code, body := listJobs(t, base, pat, machineID)
	if code != http.StatusOK {
		t.Fatalf("empty list: %d %s", code, body)
	}
	var empty []any
	if err := json.Unmarshal(body, &empty); err != nil || empty == nil {
		t.Fatalf("want [], got %s", body)
	}

	code, body = createJob(t, base, pat, machineID, jobCreateBody("list-me", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, body = listJobs(t, base, pat, machineID)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	code, body = getJob(t, base, pat, jobID)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, body)
	}
}
