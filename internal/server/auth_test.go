package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/ratelimit"
	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/barzoj/dad-joke/internal/tokenhash"
	"github.com/google/uuid"
)

func startTestServer(t *testing.T, st store.Store, cfg server.Config) string {
	t.Helper()
	cfg.Store = st
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	srv, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx, ln) }()
	return "http://" + ln.Addr().String()
}

func doJSON(t *testing.T, method, url, bearer string, body any) (int, http.Header, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header, raw
}

func seedTenantPAT(t *testing.T, st store.Store, name string) (tenantID, rawPAT string) {
	t.Helper()
	res, err := bootstrap.Run(context.Background(), st, name)
	if err != nil {
		t.Fatal(err)
	}
	return res.TenantID, res.RawKey
}

func TestAuth_MissingToken401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	code, _, body := doJSON(t, http.MethodGet, base+"/v1/agent-tokens", "", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestAuth_MalformedBearer401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/agent-tokens", nil)
	req.Header.Set("Authorization", "Token not-a-bearer")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestAuth_InvalidToken401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	fake := "dj_pat_" + "00" + uuid.NewString() + uuid.NewString()[:30]
	code, _, body := doJSON(t, http.MethodGet, base+"/v1/api-keys", fake, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestAuth_WrongTokenTypeCrossAPI(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "cross-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})

	// Create an agent token via ControlAPI.
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "agent"})
	if code != http.StatusCreated {
		t.Fatalf("create agent token: %d %s", code, body)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	// dj_agt_ on ControlAPI → 401
	code, _, body = doJSON(t, http.MethodGet, base+"/v1/api-keys", created.Token, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("agt on control: %d %s", code, body)
	}

	// dj_pat_ on AgentAPI → 401
	hb, _ := json.Marshal(map[string]any{
		"machine_id":             uuid.NewString(),
		"agent_version":          "0.0.1",
		"hostname":               "pc",
		"os_version":             "Windows 11",
		"applied_policy_version": 0,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(hb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pat)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pat on agent: %d", res.StatusCode)
	}
}

func TestAuth_RevokedToken401AndAudit(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "revoke-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "to-revoke"})
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

	code, _, body = doJSON(t, http.MethodDelete, base+"/v1/agent-tokens/"+created.ID, pat, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", code, body)
	}

	// Immediate 401 on AgentAPI.
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
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked heartbeat: %d", res.StatusCode)
	}

	audits, err := st.Audits().ListByTenant(context.Background(), tenantID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawRevoke bool
	for _, a := range audits {
		if a.Action == "token.revoke" && a.Resource == "token:"+created.ID {
			sawRevoke = true
		}
		for _, v := range a.Details {
			if v == created.Token || v == pat {
				t.Fatalf("raw secret leaked into audit: %+v", a)
			}
		}
	}
	if !sawRevoke {
		t.Fatalf("missing revoke audit: %+v", audits)
	}
}

func TestAuth_CrossTenantIsolation404(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, patA := seedTenantPAT(t, st, "tenA-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "tenB-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", patA, map[string]any{"name": "a-token"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}

	// Tenant B cannot list Tenant A's tokens.
	code, _, body = doJSON(t, http.MethodGet, base+"/v1/agent-tokens", patB, nil)
	if code != http.StatusOK {
		t.Fatalf("list B: %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item["id"] == created.ID {
			t.Fatal("tenant B saw tenant A token")
		}
	}

	// Tenant B cannot delete Tenant A's token → 404
	code, _, body = doJSON(t, http.MethodDelete, base+"/v1/agent-tokens/"+created.ID, patB, nil)
	if code != http.StatusNotFound {
		t.Fatalf("delete cross-tenant: %d %s", code, body)
	}
	assertErrorResponse(t, body)
}

func TestAuth_RateLimit429RetryAfter(t *testing.T) {
	t.Parallel()
	st := memory.New()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	authFail := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	base := startTestServer(t, st, server.Config{
		AuthFailureLimiter: authFail,
		Clock:              clock,
	})

	bad := "dj_pat_" + "ff" + uuid.NewString() + uuid.NewString()[:30]
	var saw429 bool
	var retryAfter string
	for i := 0; i < 6; i++ {
		code, hdr, body := doJSON(t, http.MethodGet, base+"/v1/api-keys", bad, nil)
		if code == http.StatusTooManyRequests {
			saw429 = true
			retryAfter = hdr.Get("Retry-After")
			assertErrorResponse(t, body)
			break
		}
		if code != http.StatusUnauthorized {
			t.Fatalf("iter %d: status=%d body=%s", i, code, body)
		}
	}
	if !saw429 {
		t.Fatal("expected 429 after repeated invalid tokens")
	}
	n, err := strconv.Atoi(retryAfter)
	if err != nil || n < 1 {
		t.Fatalf("Retry-After = %q", retryAfter)
	}
}

func TestAuth_HealthzUnauthenticated(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	code, _, body := doJSON(t, http.MethodGet, base+"/healthz", "", nil)
	if code != http.StatusOK {
		t.Fatalf("status = %d body=%s", code, body)
	}
}

func TestTokenCRUD_RawSecretOnlyOnCreate(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "crud-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	raw, _ := created["token"].(string)
	if tokenhash.KindOf(raw) != store.TokenKindAgent {
		t.Fatalf("raw token = %q", raw)
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/agent-tokens", pat, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d", len(list))
	}
	if _, ok := list[0]["token"]; ok {
		t.Fatal("list must not include raw token")
	}

	// Stored hash only.
	tok, err := st.Tokens().GetByID(context.Background(), tenantID, list[0]["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if tok.Hash != tokenhash.Hash(raw) {
		t.Fatal("hash mismatch")
	}
	if tok.Hash == raw {
		t.Fatal("raw leaked into hash")
	}

	audits, _ := st.Audits().ListByTenant(context.Background(), tenantID, 20)
	var sawIssue bool
	for _, a := range audits {
		if a.Action == "token.issue" && a.Resource == "token:"+tok.ID {
			sawIssue = true
		}
	}
	if !sawIssue {
		t.Fatalf("missing issue audit: %+v", audits)
	}
}

func TestApiKeyCRUD(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "apikey-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/api-keys", pat, map[string]any{"name": "mcp"})
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
	if created.Token[:7] != "dj_pat_" {
		t.Fatalf("token = %q", created.Token)
	}

	code, _, body = doJSON(t, http.MethodGet, base+"/v1/api-keys", pat, nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}

	code, _, body = doJSON(t, http.MethodDelete, base+"/v1/api-keys/"+created.ID, pat, nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", code, body)
	}

	code, _, _ = doJSON(t, http.MethodGet, base+"/v1/api-keys", created.Token, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("revoked key still works: %d", code)
	}
}

func TestAuth_MissingBearerOnAgentAPI401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	base := startTestServer(t, st, server.Config{})
	hb, _ := json.Marshal(map[string]any{
		"machine_id":             uuid.NewString(),
		"agent_version":          "0.0.1",
		"hostname":               "pc",
		"os_version":             "Windows 11",
		"applied_policy_version": 0,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(hb))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", res.StatusCode, raw)
	}
	assertErrorResponse(t, raw)
}

func TestAuth_ExpiredToken401(t *testing.T) {
	t.Parallel()
	st := memory.New()
	tenantID, pat := seedTenantPAT(t, st, "expired-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	raw, prefix, hash, err := tokenhash.Generate(store.TokenKindAgent)
	if err != nil {
		t.Fatal(err)
	}
	tok := &store.Token{
		TenantID:  tenantID,
		Kind:      store.TokenKindAgent,
		Name:      "expired",
		Prefix:    prefix,
		Hash:      hash,
		CreatedAt: past,
		ExpiresAt: &past,
	}
	if err := st.Tokens().Create(context.Background(), tok); err != nil {
		t.Fatal(err)
	}

	base := startTestServer(t, st, server.Config{Clock: func() time.Time { return now }})
	hb, _ := json.Marshal(map[string]any{
		"machine_id":             uuid.NewString(),
		"agent_version":          "0.0.1",
		"hostname":               "pc",
		"os_version":             "Windows 11",
		"applied_policy_version": 0,
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(hb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+raw)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired agent token: %d %s", res.StatusCode, body)
	}
	assertErrorResponse(t, body)

	// Control PAT still works (sanity).
	code, _, _ := doJSON(t, http.MethodGet, base+"/v1/api-keys", pat, nil)
	if code != http.StatusOK {
		t.Fatalf("pat status = %d", code)
	}
}

func TestAuth_RepeatedRevokedToken429(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "rev429-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	authFail := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 3, Clock: clock})
	base := startTestServer(t, st, server.Config{
		AuthFailureLimiter: authFail,
		Clock:              clock,
	})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "soon-revoked"})
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
	code, _, body = doJSON(t, http.MethodDelete, base+"/v1/agent-tokens/"+created.ID, pat, nil)
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
	var saw429 bool
	var retryAfter string
	for i := 0; i < 6; i++ {
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/heartbeat", bytes.NewReader(hb))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+created.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode == http.StatusTooManyRequests {
			saw429 = true
			retryAfter = res.Header.Get("Retry-After")
			assertErrorResponse(t, raw)
			break
		}
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("iter %d: status=%d body=%s", i, res.StatusCode, raw)
		}
	}
	if !saw429 {
		t.Fatal("expected 429 after repeated revoked-token auth failures")
	}
	n, err := strconv.Atoi(retryAfter)
	if err != nil || n < 1 {
		t.Fatalf("Retry-After = %q", retryAfter)
	}
}

func TestAuth_MutationLimiter429RetryAfter(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "mut-"+uuid.NewString()[:8])
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	mutations := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 2, Clock: clock})
	base := startTestServer(t, st, server.Config{
		MutationLimiter: mutations,
		Clock:           clock,
	})

	var lastCode int
	var lastHdr http.Header
	var lastBody []byte
	for i := 0; i < 4; i++ {
		lastCode, lastHdr, lastBody = doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{
			"name": "mut-" + strconv.Itoa(i),
		})
		if lastCode == http.StatusTooManyRequests {
			break
		}
		if lastCode != http.StatusCreated {
			t.Fatalf("iter %d: status=%d body=%s", i, lastCode, lastBody)
		}
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", lastCode, lastBody)
	}
	if ra := lastHdr.Get("Retry-After"); ra == "" {
		t.Fatal("missing Retry-After")
	}
	assertErrorResponse(t, lastBody)
}

func TestAuth_SessionMutationLimiter429RetryAfter(t *testing.T) {
	t.Parallel()
	st := memory.New()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	mutations := ratelimit.New(ratelimit.Config{Window: time.Minute, MaxHits: 2, Clock: clock})
	base := startTestServer(t, st, server.Config{
		MutationLimiter: mutations,
		Clock:           clock,
	})
	code, _, body := doJSON(t, http.MethodPost, base+"/v1/auth/register", "", map[string]any{
		"email": "mutses-" + uuid.NewString()[:8] + "@example.com", "password": "password123",
	})
	if code != http.StatusCreated {
		t.Fatalf("register: %d %s", code, body)
	}
	var sess struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatal(err)
	}

	var lastCode int
	var lastHdr http.Header
	var lastBody []byte
	for i := 0; i < 4; i++ {
		lastCode, lastHdr, lastBody = doJSON(t, http.MethodPost, base+"/v1/api-keys", sess.AccessToken, map[string]any{
			"name": "ses-mut-" + strconv.Itoa(i),
		})
		if lastCode == http.StatusTooManyRequests {
			break
		}
		if lastCode != http.StatusCreated {
			t.Fatalf("iter %d: status=%d body=%s", i, lastCode, lastBody)
		}
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d body=%s", lastCode, lastBody)
	}
	if ra := lastHdr.Get("Retry-After"); ra == "" {
		t.Fatal("missing Retry-After")
	}
	assertErrorResponse(t, lastBody)
}

func TestAuth_TokenCreateAuditFailureRevokesOrphan(t *testing.T) {
	t.Parallel()
	baseStore := memory.New()
	tenantID, pat := seedTenantPAT(t, baseStore, "auditfail-"+uuid.NewString()[:8])
	failing := &failingAuditStore{Store: baseStore, fail: true}
	base := startTestServer(t, failing, server.Config{})

	code, _, body := doJSON(t, http.MethodPost, base+"/v1/agent-tokens", pat, map[string]any{"name": "should-roll-back"})
	if code != http.StatusInternalServerError {
		t.Fatalf("create: %d %s", code, body)
	}
	assertErrorResponse(t, body)

	listed, err := baseStore.Tokens().ListByTenant(context.Background(), tenantID, store.TokenKindAgent)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("tokens = %d, want 1 revoked orphan", len(listed))
	}
	if listed[0].RevokedAt == nil {
		t.Fatal("orphan active secret left behind after audit failure")
	}
}

type failingAuditStore struct {
	store.Store
	fail bool
}

func (s *failingAuditStore) Audits() store.AuditStore {
	return failingAuditEntries{AuditStore: s.Store.Audits(), parent: s}
}

type failingAuditEntries struct {
	store.AuditStore
	parent *failingAuditStore
}

func (a failingAuditEntries) Append(ctx context.Context, e *store.AuditEntry) error {
	if a.parent.fail {
		return errors.New("audit append failed")
	}
	return a.AuditStore.Append(ctx, e)
}

func assertErrorResponse(t *testing.T, body []byte) {
	t.Helper()
	var er struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if er.Code == "" || er.Message == "" || er.RequestID == "" {
		t.Fatalf("incomplete ErrorResponse: %+v", er)
	}
}
