package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj/dad-joke/internal/server"
	"github.com/google/uuid"
)

func TestIntegration_LocalAuthRegisterLoginMeTokensLogout(t *testing.T) {
	st := openMongoStore(t)
	secure := false
	base := startTestServer(t, st, server.Config{CookieSecure: &secure})
	email := "mongo-auth-" + uuid.NewString()[:8] + "@example.com"
	pass := "password123"

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": pass,
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/auth/login", "", map[string]any{
		"email": email, "password": pass,
	})
	if code != http.StatusOK {
		t.Fatalf("login: %d %s", code, body)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatal(err)
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/auth/me", sess.AccessToken, nil)
	if code != http.StatusOK {
		t.Fatalf("me: %d %s", code, body)
	}
	if strings.Contains(string(body), "password_hash") {
		t.Fatal("password_hash leaked")
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/api-keys", sess.AccessToken, map[string]any{"name": "pat"})
	if code != http.StatusCreated {
		t.Fatalf("pat: %d %s", code, body)
	}
	var pat struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &pat)
	if !strings.HasPrefix(pat.Token, "dj_pat_") {
		t.Fatalf("pat=%q", pat.Token)
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/agent-tokens", sess.AccessToken, map[string]any{"name": "agt"})
	if code != http.StatusCreated {
		t.Fatalf("agt: %d %s", code, body)
	}
	var agt struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &agt)
	if !strings.HasPrefix(agt.Token, "dj_agt_") {
		t.Fatalf("agt=%q", agt.Token)
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/auth/logout", sess.AccessToken, nil)
	if code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", code, body)
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/auth/me", sess.AccessToken, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d %s", code, body)
	}
}

func TestIntegration_DuplicateEmailNoOrphanTenant(t *testing.T) {
	st := openMongoStore(t)
	base := startTestServer(t, st, server.Config{})
	email := "dup-mongo-" + uuid.NewString()[:8] + "@example.com"
	tenant := "tenant-" + uuid.NewString()[:8]

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": "password123", "tenant_name": tenant,
	})
	if code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": "password123", "tenant_name": tenant + "-b",
	})
	if code != http.StatusConflict {
		t.Fatalf("dup: %d %s", code, body)
	}
	if _, err := st.Tenants().GetByName(t.Context(), tenant+"-b"); err == nil {
		t.Fatal("orphan tenant left behind")
	}
}
