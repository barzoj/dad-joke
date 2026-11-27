package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func TestLocalAuth_RegisterCreatesTenantUserAndSession(t *testing.T) {
	t.Parallel()
	st := memory.New()
	secure := true
	base := startTestServer(t, st, server.Config{CookieSecure: &secure})

	email := "Parent+" + uuid.NewString()[:8] + "@Example.COM"
	code, hdr, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":    email,
		"password": "correct-horse-battery",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		User        struct {
			ID       string `json:"id"`
			Email    string `json:"email"`
			TenantID string `json:"tenant_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sess.AccessToken, "dj_ses_") {
		t.Fatalf("token = %q", sess.AccessToken)
	}
	if sess.TokenType != "Bearer" {
		t.Fatalf("token_type = %q", sess.TokenType)
	}
	if sess.User.Email != strings.ToLower(email) {
		t.Fatalf("email = %q", sess.User.Email)
	}
	if strings.Contains(string(body), "password_hash") || strings.Contains(string(body), "$argon2id$") {
		t.Fatalf("password material leaked: %s", body)
	}
	sc := hdr.Get("Set-Cookie")
	for _, want := range []string{"dj_session=", "HttpOnly", "SameSite=Lax", "Secure"} {
		if !strings.Contains(sc, want) {
			t.Fatalf("Set-Cookie missing %q: %q", want, sc)
		}
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/auth/me", sess.AccessToken, nil)
	if code != http.StatusOK {
		t.Fatalf("me: %d %s", code, body)
	}
}

func TestLocalAuth_DuplicateEmail409NoOrphanTenant(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	email := "dup-" + uuid.NewString()[:8] + "@example.com"
	tenantName := "fixed-tenant-" + uuid.NewString()[:8]

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":       email,
		"password":    "password123",
		"tenant_name": tenantName,
	})
	if code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}

	before, err := st.Tenants().GetByName(t.Context(), tenantName)
	if err != nil {
		t.Fatal(err)
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":       email,
		"password":    "password123",
		"tenant_name": tenantName + "-other",
	})
	if code != http.StatusConflict {
		t.Fatalf("dup: %d %s", code, body)
	}
	assertErrorResponse(t, body)

	// Original tenant still present; no second tenant for the failed attempt.
	got, err := st.Tenants().GetByName(t.Context(), tenantName)
	if err != nil || got.ID != before.ID {
		t.Fatalf("tenant changed: %v %+v", err, got)
	}
	if _, err := st.Tenants().GetByName(t.Context(), tenantName+"-other"); err == nil {
		t.Fatal("orphan tenant left behind")
	}
}

func TestLocalAuth_DuplicateEmailCaseInsensitive409(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	suffix := uuid.NewString()[:8]
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":    "Foo@" + suffix + ".Example.com",
		"password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	code, _, body = doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":    "foo@" + suffix + ".example.com",
		"password": "password123",
	})
	if code != http.StatusConflict {
		t.Fatalf("case-insensitive dup: %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestLocalAuth_PATOnlyMe401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	_, pat := seedTenantPAT(t, st, "pat-me-"+uuid.NewString()[:8])
	code, _, body := doJSON(t, http.MethodGet, base+"/v1/auth/me", pat, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestLocalAuth_AllowSignupFalse403(t *testing.T) {
	t.Parallel()
	st := memory.New()
	off := false
	base := startTestServer(t, st, server.Config{AllowSignup: &off})
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email":    "nope@example.com",
		"password": "password123",
	})
	if code != http.StatusForbidden {
		t.Fatalf("status = %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestLocalAuth_LoginUnknownAndWrongPasswordSame401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	email := "login-" + uuid.NewString()[:8] + "@example.com"
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}

	code1, _, body1 := doJSON(t, http.MethodPost, base+"/v1/auth/login", "", map[string]any{
		"email": email, "password": "wrong-password",
	})
	code2, _, body2 := doJSON(t, http.MethodPost, base+"/v1/auth/login", "", map[string]any{
		"email": "missing-" + uuid.NewString()[:8] + "@example.com", "password": "wrong-password",
	})
	if code1 != http.StatusUnauthorized || code2 != http.StatusUnauthorized {
		t.Fatalf("codes = %d %d", code1, code2)
	}
	var e1, e2 map[string]any
	_ = json.Unmarshal(body1, &e1)
	_ = json.Unmarshal(body2, &e2)
	if e1["code"] != e2["code"] || e1["message"] != e2["message"] {
		t.Fatalf("responses differ: %s vs %s", body1, body2)
	}
	if strings.Contains(string(body1), "password_hash") || strings.Contains(string(body2), "$argon2id$") {
		t.Fatal("password hash leaked")
	}
}

func TestLocalAuth_RegisterLoginMeTokensLogout(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	email := "flow-" + uuid.NewString()[:8] + "@example.com"
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

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/api-keys", sess.AccessToken, map[string]any{"name": "pat"})
	if code != http.StatusCreated {
		t.Fatalf("pat: %d %s", code, body)
	}
	var pat struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &pat); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pat.Token, "dj_pat_") {
		t.Fatalf("pat = %q", pat.Token)
	}

	code, _, body = doJSON(t, http.MethodPost, base+"/v1/agent-tokens", sess.AccessToken, map[string]any{"name": "agt"})
	if code != http.StatusCreated {
		t.Fatalf("agt: %d %s", code, body)
	}
	var agt struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &agt); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(agt.Token, "dj_agt_") {
		t.Fatalf("agt = %q", agt.Token)
	}

	// Agent token rejected on ControlAPI.
	code, _, _ = doJSON(t, http.MethodGet, base+"/v1/auth/me", agt.Token, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("agt on control me: %d", code)
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

func TestLocalAuth_LoginRateLimit429RetryAfter(t *testing.T) {
	t.Parallel()
	st := memory.New()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lim := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	base := startTestServer(t, st, server.Config{
		Clock:               clock,
		LoginFailureLimiter: lim,
	})
	email := "rl-" + uuid.NewString()[:8] + "@example.com"
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}

	var lastCode int
	var lastHdr http.Header
	var lastBody []byte
	for i := 0; i < 5; i++ {
		lastCode, lastHdr, lastBody = doJSON(t, http.MethodPost, base+"/v1/auth/login", "", map[string]any{
			"email": email, "password": "wrong",
		})
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", lastCode, lastBody)
	}
	if ra := lastHdr.Get("Retry-After"); ra == "" {
		t.Fatal("missing Retry-After")
	}
	assertErrorResponse(t, lastBody)
}

func TestLocalAuth_SessionRejectedOnAgentAPI(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	email := "agent-" + uuid.NewString()[:8] + "@example.com"
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": email, "password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(body, &sess)

	hb, _ := json.Marshal(map[string]any{
		"machine_id":             uuid.NewString(),
		"agent_version":          "0.0.1",
		"hostname":               "pc",
		"os_version":             "Windows 11",
		"applied_policy_version": 0,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", strings.NewReader(string(hb)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sess.AccessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", res.StatusCode)
	}
}
