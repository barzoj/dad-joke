package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/internal/server"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/store/memory"
	"github.com/google/uuid"
)

func postFactsRaw(t *testing.T, base, agentToken string, raw []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/facts", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if agentToken != "" {
		req.Header.Set("Authorization", "Bearer "+agentToken)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, out
}

func validFactsJSON(machineID string) []byte {
	streamID := uuid.NewString()
	return []byte(fmt.Sprintf(`{
		"machine_id":%q,
		"stream_id":%q,
		"seq":1,
		"sent_at":"2026-11-28T12:00:00Z",
		"time_zone":"UTC",
		"utc_offset_minutes":0,
		"facts":[]
	}`, machineID, streamID))
}

func TestFactsAndUsage_Stub501JSON(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-501-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()

	t.Run("post_facts_501", func(t *testing.T) {
		t.Parallel()
		code, raw := postFactsRaw(t, base, agent, validFactsJSON(machineID))
		if code != http.StatusNotImplemented {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
		var er struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(raw, &er); err != nil {
			t.Fatal(err)
		}
		if er.Code != "not_implemented" {
			t.Fatalf("code=%q", er.Code)
		}
	})

	t.Run("get_usage_501", func(t *testing.T) {
		t.Parallel()
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=2026-11-28", pat, nil)
		if code != http.StatusNotImplemented {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("post_facts_unauthenticated_401", func(t *testing.T) {
		t.Parallel()
		code, raw := postFactsRaw(t, base, "", validFactsJSON(machineID))
		if code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("get_usage_unauthenticated_401", func(t *testing.T) {
		t.Parallel()
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=2026-11-28", "", nil)
		if code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})
}

func TestFacts_BodySizeLimit(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-lim-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: func() time.Time {
		return time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	}})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	streamID := uuid.NewString()

	pad := strings.Repeat("x", store.FactBatchMaxBytes)
	oversized := fmt.Sprintf(
		`{"machine_id":%q,"stream_id":%q,"seq":1,"sent_at":"2026-11-28T12:00:00Z","time_zone":"UTC","utc_offset_minutes":0,"facts":[{"ts":"2026-11-28T12:00:00Z","kind":"proc.sample","plugin":"p","dur_s":0,"data":{"exes":[],"pad":%q}}]}`,
		machineID, streamID, pad,
	)
	code, raw := postFactsRaw(t, base, agent, []byte(oversized))
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", code, raw)
	}
	assertErrorResponse(t, raw)

	prefix := fmt.Sprintf(
		`{"machine_id":%q,"stream_id":%q,"seq":1,"sent_at":"2026-11-28T12:00:00Z","time_zone":"UTC","utc_offset_minutes":0,"facts":[{"ts":"2026-11-28T12:00:00Z","kind":"session.restarted","plugin":"p","dur_s":0,"data":{"pad":"`,
		machineID, streamID,
	)
	suffix := `"}}]}`
	need := store.FactBatchMaxBytes - len(prefix) - len(suffix)
	if need < 1 {
		t.Fatalf("prefix too large: %d", len(prefix))
	}
	exact := prefix + strings.Repeat("y", need) + suffix
	if len(exact) != store.FactBatchMaxBytes {
		t.Fatalf("exact len=%d want %d", len(exact), store.FactBatchMaxBytes)
	}
	code, raw = postFactsRaw(t, base, agent, []byte(exact))
	if code != http.StatusNotImplemented {
		t.Fatalf("exact-size status=%d body=%s", code, raw)
	}
	assertErrorResponse(t, raw)
}

func TestContractNegatives_ProductionPath(t *testing.T) {
	t.Parallel()
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-neg-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	streamID := uuid.NewString()

	t.Run("heartbeat_missing_time_zone_400", func(t *testing.T) {
		t.Parallel()
		code, raw := postHeartbeat(t, base, agent, map[string]any{
			"machine_id":             machineID,
			"agent_version":          "0.0.1",
			"hostname":               "pc",
			"os_version":             "Win",
			"applied_policy_version": 0,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("heartbeat_empty_time_zone_400", func(t *testing.T) {
		t.Parallel()
		code, raw := postHeartbeat(t, base, agent, map[string]any{
			"machine_id":             machineID,
			"agent_version":          "0.0.1",
			"hostname":               "pc",
			"os_version":             "Win",
			"applied_policy_version": 0,
			"time_zone":              "",
			"utc_offset_minutes":     0,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	baseFact := func() map[string]any {
		return map[string]any{
			"machine_id":         machineID,
			"stream_id":          streamID,
			"seq":                1,
			"sent_at":            "2026-11-28T12:00:00Z",
			"time_zone":          "UTC",
			"utc_offset_minutes": 0,
			"facts": []any{
				map[string]any{
					"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 1,
					"data": map[string]any{"exes": []any{}},
				},
			},
		}
	}
	postFactsMap := func(t *testing.T, body map[string]any) (int, []byte) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return postFactsRaw(t, base, agent, raw)
	}

	t.Run("facts_seq_zero_400", func(t *testing.T) {
		t.Parallel()
		body := baseFact()
		body["seq"] = 0
		code, raw := postFactsMap(t, body)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("facts_dur_s_301_400", func(t *testing.T) {
		t.Parallel()
		body := baseFact()
		facts := body["facts"].([]any)
		facts[0].(map[string]any)["dur_s"] = 301
		code, raw := postFactsMap(t, body)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("facts_unknown_kind_400", func(t *testing.T) {
		t.Parallel()
		body := baseFact()
		facts := body["facts"].([]any)
		facts[0].(map[string]any)["kind"] = "nope.kind"
		code, raw := postFactsMap(t, body)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("facts_title_257_runes_400", func(t *testing.T) {
		t.Parallel()
		body := baseFact()
		title := strings.Repeat("あ", 257)
		if utf8.RuneCountInString(title) != 257 {
			t.Fatal("title rune count")
		}
		body["facts"] = []any{
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "fg.sample", "plugin": "p", "dur_s": 1,
				"data": map[string]any{
					"exe": "notepad.exe", "pid": 1, "title": title, "idle_s": 0,
				},
			},
		}
		code, raw := postFactsMap(t, body)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("facts_501_items_400", func(t *testing.T) {
		t.Parallel()
		body := baseFact()
		items := make([]any, store.FactBatchMaxCount+1)
		for i := range items {
			items[i] = map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 0,
				"data": map[string]any{"exes": []any{}},
			}
		}
		body["facts"] = items
		code, raw := postFactsMap(t, body)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("facts_exe_item_too_long_400", func(t *testing.T) {
		t.Parallel()
		body := baseFact()
		long := strings.Repeat("e", 261)
		body["facts"] = []any{
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 0,
				"data": map[string]any{"exes": []any{long}},
			},
		}
		code, raw := postFactsMap(t, body)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("usage_empty_date_400", func(t *testing.T) {
		t.Parallel()
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=", pat, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("usage_missing_date_400", func(t *testing.T) {
		t.Parallel()
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage", pat, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})

	t.Run("usage_tz_ignored_still_501", func(t *testing.T) {
		t.Parallel()
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=2026-11-28&tz=UTC", pat, nil)
		if code != http.StatusNotImplemented {
			t.Fatalf("status=%d body=%s", code, raw)
		}
		assertErrorResponse(t, raw)
	})
}
