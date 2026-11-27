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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st, err := mongostore.Open(ctx, mongostore.Config{URI: uri, Database: dbName})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close(context.Background())
	})
	if err := st.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	return st
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
