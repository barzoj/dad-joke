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
