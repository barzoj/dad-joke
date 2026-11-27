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
	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
)

// Mongo-backed coverage for all T-12 acceptance criteria (HTTP + DAL).
func TestIntegration_Jobs_AllAcceptanceCriteria(t *testing.T) {
	st := openMongoStore(t)
	a, err := bootstrap.Run(context.Background(), st, "mongo-job-a-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	b, err := bootstrap.Run(context.Background(), st, "mongo-job-b-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)
	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	agentA := createAgentToken(t, base, a.RawKey)
	agentB := createAgentToken(t, base, b.RawKey)
	machineID := uuid.NewString()
	otherMachine := uuid.NewString()

	// AC10 + create
	code, body := createJob(t, base, a.RawKey, machineID, jobCreateBody("echo mongo", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	jobID := decodeJobID(t, body)

	code, hbBody := postHeartbeat(t, base, agentA, map[string]any{
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
		t.Fatalf("AC10 pending = %#v", hb.PendingJobIds)
	}

	// AC1 claim + second claim 409
	code, body = claimJob(t, base, agentA, jobID, machineID)
	if code != http.StatusOK {
		t.Fatalf("AC1 claim: %d %s", code, body)
	}
	code, body = claimJob(t, base, agentA, jobID, machineID)
	if code != http.StatusConflict {
		t.Fatalf("AC1 second claim: %d %s", code, body)
	}

	// AC8 cancel running → 409
	code, body = cancelJob(t, base, a.RawKey, jobID)
	if code != http.StatusConflict {
		t.Fatalf("AC8 cancel running: %d %s", code, body)
	}

	// Complete then AC2 second result 409; AC8 cancel terminal 409
	code, body = postJobResult(t, base, agentA, jobID, map[string]any{
		"machine_id": machineID, "status": "succeeded", "exit_code": 0, "stdout": "ok",
	})
	if code != http.StatusNoContent {
		t.Fatalf("complete: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agentA, jobID, map[string]any{
		"machine_id": machineID, "status": "failed", "exit_code": 1,
	})
	if code != http.StatusConflict {
		t.Fatalf("AC2 finished result: %d %s", code, body)
	}
	code, body = cancelJob(t, base, a.RawKey, jobID)
	if code != http.StatusConflict {
		t.Fatalf("AC8 cancel terminal: %d %s", code, body)
	}

	// AC2 cancel pending + result on canceled → 409
	code, body = createJob(t, base, a.RawKey, machineID, jobCreateBody("cancel-me", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create cancel: %d %s", code, body)
	}
	cancelID := decodeJobID(t, body)
	code, body = cancelJob(t, base, a.RawKey, cancelID)
	if code != http.StatusNoContent {
		t.Fatalf("AC2 cancel: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agentA, cancelID, map[string]any{
		"machine_id": machineID, "status": "succeeded",
	})
	if code != http.StatusConflict {
		t.Fatalf("AC2 result canceled: %d %s", code, body)
	}

	// AC3+AC7 expiry
	code, body = createJob(t, base, a.RawKey, machineID, jobCreateBody("expired", now.Add(-time.Minute)))
	if code != http.StatusCreated {
		t.Fatalf("create expired: %d %s", code, body)
	}
	expiredID := decodeJobID(t, body)
	code, hbBody = postHeartbeat(t, base, agentA, map[string]any{
		"machine_id": machineID, "agent_version": "1", "hostname": "h",
		"os_version": "Win", "applied_policy_version": 0,
	})
	if code != http.StatusOK {
		t.Fatalf("hb2: %d %s", code, hbBody)
	}
	if err := json.Unmarshal(hbBody, &hb); err != nil {
		t.Fatal(err)
	}
	for _, id := range hb.PendingJobIds {
		if id == expiredID {
			t.Fatalf("AC7 expired id still pending: %#v", hb.PendingJobIds)
		}
	}
	got, err := st.Jobs().Get(context.Background(), a.TenantID, expiredID)
	if err != nil || got.Status != store.JobTimedOut {
		t.Fatalf("AC3 timed_out: %v %+v", err, got)
	}
	code, body = claimJob(t, base, agentA, expiredID, machineID)
	if code != http.StatusConflict {
		t.Fatalf("AC3 claim expired: %d %s", code, body)
	}

	// AC4 oversized
	big := strings.Repeat("y", store.JobIOMaxBytes+1)
	code, body = createJob(t, base, a.RawKey, machineID, jobCreateBody(big, now.Add(time.Hour)))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("AC4 payload: %d %s", code, body)
	}
	code, body = createJob(t, base, a.RawKey, machineID, jobCreateBody("size", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create size: %d %s", code, body)
	}
	sizeID := decodeJobID(t, body)
	code, body = claimJob(t, base, agentA, sizeID, machineID)
	if code != http.StatusOK {
		t.Fatalf("claim size: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agentA, sizeID, map[string]any{
		"machine_id": machineID, "status": "succeeded", "stderr": big,
	})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("AC4 stderr: %d %s", code, body)
	}

	// AC5 wrong machine
	code, body = createJob(t, base, a.RawKey, machineID, jobCreateBody("mach", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create mach: %d %s", code, body)
	}
	machID := decodeJobID(t, body)
	code, body = claimJob(t, base, agentA, machID, otherMachine)
	if code != http.StatusNotFound {
		t.Fatalf("AC5 wrong machine claim: %d %s", code, body)
	}

	// AC6 cross-tenant
	code, body = getJob(t, base, b.RawKey, jobID)
	if code != http.StatusNotFound {
		t.Fatalf("AC6 get: %d %s", code, body)
	}
	code, body = cancelJob(t, base, b.RawKey, cancelID)
	if code != http.StatusNotFound {
		t.Fatalf("AC6 cancel: %d %s", code, body)
	}
	code, body = claimJob(t, base, agentB, machID, machineID)
	if code != http.StatusNotFound {
		t.Fatalf("AC6 claim: %d %s", code, body)
	}
	code, body = postJobResult(t, base, agentB, machID, map[string]any{
		"machine_id": machineID, "status": "succeeded",
	})
	if code != http.StatusNotFound {
		t.Fatalf("AC6 result: %d %s", code, body)
	}

	// AC9 concurrent double-claim
	code, body = createJob(t, base, a.RawKey, machineID, jobCreateBody("race", now.Add(time.Hour)))
	if code != http.StatusCreated {
		t.Fatalf("create race: %d %s", code, body)
	}
	raceID := decodeJobID(t, body)
	var okCount, conflictCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := claimJob(t, base, agentA, raceID, machineID)
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
		t.Fatalf("AC9 ok = %d", okCount.Load())
	}
	if conflictCount.Load() != 23 {
		t.Fatalf("AC9 conflict = %d", conflictCount.Load())
	}

	// AC11 audits
	entries, err := st.Audits().ListByTenant(context.Background(), a.TenantID, 100)
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
			t.Fatalf("AC11 missing %q in %#v", want, seen)
		}
	}
}
