package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestMachines_ListAndGet(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "mach-"+uuid.NewString()[:8])
	machineID := uuid.NewString()
	now := time.Now().UTC()
	if err := st.Machines().Upsert(context.Background(), &store.Machine{
		ID:                   machineID,
		TenantID:             tenantID,
		Hostname:             "kid-pc",
		OSVersion:            "Windows 11 Home",
		AgentVersion:         "0.0.1",
		LastSeenAt:           now,
		AppliedPolicyVersion: 2,
		DesiredPolicyVersion: 3,
	}); err != nil {
		t.Fatal(err)
	}

	base := startTestServer(t, st, server.Config{})
	code, _, body := doJSON(t, http.MethodGet, base+"/v1/machines", pat, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["machine_id"] != machineID {
		t.Fatalf("list = %s", body)
	}
	if list[0]["status"] != "online" {
		t.Fatalf("status = %v", list[0]["status"])
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID, pat, nil)
	if code != http.StatusOK {
		t.Fatalf("get: %d %s", code, body)
	}

	other := uuid.NewString()
	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines/"+other, pat, nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing machine: %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestMachines_EmptyListIsJSONArray(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "empty-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	code, _, body := doJSON(t, http.MethodGet, base+"/v1/machines", pat, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list []any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("body not JSON array: %s", body)
	}
	if list == nil {
		t.Fatal("list unmarshaled to nil (JSON null), want []")
	}
	if len(list) != 0 {
		t.Fatalf("want empty array, got %s", body)
	}
}

func TestMachines_CrossTenantIsolation(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantA, patA := seedTenantPAT(t, st, "machA-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "machB-"+uuid.NewString()[:8])
	machineID := uuid.NewString()
	now := time.Now().UTC()
	if err := st.Machines().Upsert(context.Background(), &store.Machine{
		ID: machineID, TenantID: tenantA, Hostname: "only-a",
		OSVersion: "Win", AgentVersion: "1", LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID, patB, nil)
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant get: %d %s", code, body)
	}
	assertErrorResponse(t, body)

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines", patB, nil)
	if code != http.StatusOK {
		t.Fatalf("list B: %d %s", code, body)
	}
	var listB []map[string]any
	if err := json.Unmarshal(body, &listB); err != nil {
		t.Fatal(err)
	}
	if len(listB) != 0 {
		t.Fatalf("tenant B should see no machines, got %s", body)
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines", patA, nil)
	if code != http.StatusOK {
		t.Fatalf("list A: %d %s", code, body)
	}
	var listA []map[string]any
	if err := json.Unmarshal(body, &listA); err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0]["machine_id"] != machineID {
		t.Fatalf("tenant A list = %s", body)
	}
}

func TestMachines_OnlineBoundary(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "online-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	onlineID := uuid.NewString()
	offlineID := uuid.NewString()
	if err := st.Machines().Upsert(context.Background(), &store.Machine{
		ID: onlineID, TenantID: tenantID, Hostname: "on",
		OSVersion: "Win", AgentVersion: "1",
		LastSeenAt: now.Add(-3 * time.Minute), // exactly 3m → online
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Machines().Upsert(context.Background(), &store.Machine{
		ID: offlineID, TenantID: tenantID, Hostname: "off",
		OSVersion: "Win", AgentVersion: "1",
		LastSeenAt: now.Add(-3*time.Minute - time.Second), // >3m → offline
	}); err != nil {
		t.Fatal(err)
	}

	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})

	code, _, body := doJSON(t, http.MethodGet, base+"/v1/machines/"+onlineID, pat, nil)
	if code != http.StatusOK {
		t.Fatalf("online get: %d %s", code, body)
	}
	var on map[string]any
	if err := json.Unmarshal(body, &on); err != nil {
		t.Fatal(err)
	}
	if on["status"] != "online" {
		t.Fatalf("<=3m status = %v, want online", on["status"])
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/machines/"+offlineID, pat, nil)
	if code != http.StatusOK {
		t.Fatalf("offline get: %d %s", code, body)
	}
	var off map[string]any
	if err := json.Unmarshal(body, &off); err != nil {
		t.Fatal(err)
	}
	if off["status"] != "offline" {
		t.Fatalf(">3m status = %v, want offline", off["status"])
	}
}

func TestMachines_InvalidPathUUID(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "badpath-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	code, _, body := doJSON(t, http.MethodGet, base+"/v1/machines/not-a-uuid", pat, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid path uuid: %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestCreateToken_BadRequestEmptyName(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "badreq-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "  "})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", code, body)
	}
	assertErrorResponse(t, body)

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/api-keys", pat, map[string]any{"name": ""})
	if code != http.StatusBadRequest {
		t.Fatalf("api-key status = %d %s", code, body)
	}
}
