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

	// Oversized: 256 KiB + 1 → 413.
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

	// Exactly FactBatchMaxBytes of body does not trip the facts limiter by itself
	// when the JSON is valid and within the limit. Build a body of exactly the
	// limit using a pad field sized so total UTF-8 length == FactBatchMaxBytes.
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
	// Stub returns 501 once the body is accepted by MaxBytesReader.
	if code != http.StatusNotImplemented {
		t.Fatalf("exact-size status=%d body=%s", code, raw)
	}
	assertErrorResponse(t, raw)
}
