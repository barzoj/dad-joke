package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
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

func postFactsMap(t *testing.T, base, agentToken string, body map[string]any) (int, map[string]any, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	code, out := postFactsRaw(t, base, agentToken, raw)
	var m map[string]any
	if len(out) > 0 && out[0] == '{' {
		_ = json.Unmarshal(out, &m)
	}
	return code, m, out
}


func getUsageJSON(t *testing.T, base, pat, machineID, date string) (int, map[string]any, []byte) {
	t.Helper()
	code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date="+date, pat, nil)
	var m map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &m)
	}
	return code, m, raw
}

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestFacts_HappyPathAndIdleIgnored(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-hp-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	streamID := uuid.NewString()

	// Enroll machine via heartbeat.
	code, _ := postHeartbeat(t, base, agent, map[string]any{
		"machine_id": machineID, "agent_version": "0.0.1", "hostname": "pc",
		"os_version": "Win", "applied_policy_version": 0,
		"time_zone": "Europe/Amsterdam", "utc_offset_minutes": 60,
	})
	if code != http.StatusOK {
		t.Fatalf("heartbeat %d", code)
	}

	postSample := func(stream string, seq int64, idle int) (map[string]any, []byte, int) {
		t.Helper()
		body := map[string]any{
			"machine_id": machineID, "stream_id": stream, "seq": seq,
			"sent_at": "2026-11-28T12:00:00Z", "time_zone": "Europe/Amsterdam",
			"utc_offset_minutes": 60,
			"facts": []any{
				map[string]any{
					"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 60,
					"data": map[string]any{"exes": []any{"notepad.exe"}},
				},
				map[string]any{
					"ts": "2026-11-28T12:00:00Z", "kind": "fg.sample", "plugin": "p", "dur_s": 60,
					"data": map[string]any{"exe": "notepad.exe", "pid": 1, "title": "n", "idle_s": idle},
				},
			},
		}
		code, ack, raw := postFactsMap(t, base, agent, body)
		return ack, raw, code
	}
	ack, raw, code := postSample(streamID, 1, 0)
	if code != http.StatusOK || ack["duplicate"] != false {
		t.Fatalf("idle=0 status=%d ack=%v body=%s", code, ack, raw)
	}
	ack, raw, code = postSample(streamID, 1, 0) // replay
	if code != http.StatusOK || ack["duplicate"] != true {
		t.Fatalf("replay status=%d ack=%v body=%s", code, ack, raw)
	}
	if st.FactCount() != 2 {
		t.Fatalf("fact count after replay=%d", st.FactCount())
	}

	code, usageBody, raw := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	if code != http.StatusOK {
		t.Fatalf("usage %d %s", code, raw)
	}
	apps, _ := usageBody["apps"].([]any)
	if len(apps) != 1 {
		t.Fatalf("apps=%v", usageBody)
	}
	app := apps[0].(map[string]any)
	if app["exe"] != "notepad.exe" || int(app["seconds"].(float64)) != 60 || int(app["fg_seconds"].(float64)) != 60 {
		t.Fatalf("app=%v", app)
	}

	// Separate machine: idle_s=3600 must still attribute only dur_s to fg_seconds.
	machine2 := uuid.NewString()
	_ = enrollMachine(t, base, agent, machine2, "Europe/Amsterdam", 60)
	code, _, raw = postFactsMap(t, base, agent, map[string]any{
		"machine_id": machine2, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T12:00:00Z", "time_zone": "Europe/Amsterdam", "utc_offset_minutes": 60,
		"facts": []any{
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 60,
				"data": map[string]any{"exes": []any{"notepad.exe"}},
			},
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "fg.sample", "plugin": "p", "dur_s": 60,
				"data": map[string]any{"exe": "notepad.exe", "pid": 1, "title": "n", "idle_s": 3600},
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("idle=3600 status=%d body=%s", code, raw)
	}
	code, usageBody, raw = getUsageJSON(t, base, pat, machine2, "2026-11-28")
	if code != http.StatusOK {
		t.Fatalf("usage2 %d %s", code, raw)
	}
	app = usageBody["apps"].([]any)[0].(map[string]any)
	if int(app["seconds"].(float64)) != 60 || int(app["fg_seconds"].(float64)) != 60 {
		t.Fatalf("idle=3600 must not affect totals: %v", app)
	}
}

func TestFacts_EmptyForeground(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-fg-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)

	code, _, raw := postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
		"facts": []any{
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 30,
				"data": map[string]any{"exes": []any{"a.exe"}},
			},
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "fg.sample", "plugin": "p", "dur_s": 30,
				"data": map[string]any{"exe": "", "pid": 1, "title": "x", "idle_s": 0},
			},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d %s", code, raw)
	}
	code, usageBody, raw := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	if code != http.StatusOK {
		t.Fatalf("usage %d %s", code, raw)
	}
	apps := usageBody["apps"].([]any)
	app := apps[0].(map[string]any)
	if int(app["seconds"].(float64)) != 30 || int(app["fg_seconds"].(float64)) != 0 {
		t.Fatalf("app=%v", app)
	}
}

func TestFacts_CrashBetweenInsertAndApply(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	st.AfterFactInsertErr = errors.New("injected crash")
	_, pat := seedTenantPAT(t, st, "facts-crash-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)
	streamID := uuid.NewString()
	body := map[string]any{
		"machine_id": machineID, "stream_id": streamID, "seq": 1,
		"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
		"facts": []any{
			map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 15,
				"data": map[string]any{"exes": []any{"a.exe"}},
			},
		},
	}
	code, _, raw := postFactsMap(t, base, agent, body)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("injected crash want 503 got %d %s", code, raw)
	}
	if st.FactCount() != 1 {
		t.Fatalf("facts should be inserted before fault: %d", st.FactCount())
	}
	st.AfterFactInsertErr = nil
	code, ack, raw := postFactsMap(t, base, agent, body)
	if code != http.StatusOK || ack["duplicate"] != false {
		t.Fatalf("retry status=%d ack=%v body=%s", code, ack, raw)
	}
	code, usageBody, _ := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	apps := usageBody["apps"].([]any)
	if len(apps) != 1 || int(apps[0].(map[string]any)["seconds"].(float64)) != 15 {
		t.Fatalf("usage after retry=%v", usageBody)
	}
	// Second replay now duplicate
	code, ack, _ = postFactsMap(t, base, agent, body)
	if code != http.StatusOK || ack["duplicate"] != true {
		t.Fatalf("second replay=%v", ack)
	}
}

func TestFacts_NewStreamAndGaps(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-gap-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)
	s1, s2 := uuid.NewString(), uuid.NewString()

	postOK := func(stream string, seq int64, dur int) {
		t.Helper()
		code, _, raw := postFactsMap(t, base, agent, map[string]any{
			"machine_id": machineID, "stream_id": stream, "seq": seq,
			"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
			"facts": []any{map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": dur,
				"data": map[string]any{"exes": []any{"a.exe"}},
			}},
		})
		if code != http.StatusOK {
			t.Fatalf("seq=%d %d %s", seq, code, raw)
		}
	}
	postOK(s1, 10, 10) // gap allowed
	postOK(s2, 1, 5)   // independent stream
	code, usageBody, _ := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	sec := int(usageBody["apps"].([]any)[0].(map[string]any)["seconds"].(float64))
	if sec != 15 {
		t.Fatalf("seconds=%d", sec)
	}
}

func TestFacts_MachineLocalMidnight(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 23, 45, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-mid-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)

	// Amsterdam: 23:30Z → 00:30 next day local in winter (+1)
	code, _, raw := postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T23:30:00Z", "time_zone": "Europe/Amsterdam", "utc_offset_minutes": 60,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T23:30:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 10,
			"data": map[string]any{"exes": []any{"ams.exe"}},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("ams %d %s", code, raw)
	}
	code, _, raw = postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T23:30:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T23:30:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 10,
			"data": map[string]any{"exes": []any{"utc.exe"}},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("utc %d %s", code, raw)
	}
	_, u29, _ := getUsageJSON(t, base, pat, machineID, "2026-11-29")
	apps29 := u29["apps"].([]any)
	if len(apps29) != 1 || apps29[0].(map[string]any)["exe"] != "ams.exe" {
		t.Fatalf("2026-11-29 = %v", u29)
	}
	_, u28, _ := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	apps28 := u28["apps"].([]any)
	if len(apps28) != 1 || apps28[0].(map[string]any)["exe"] != "utc.exe" {
		t.Fatalf("2026-11-28 = %v", u28)
	}
}

func TestFacts_ZoneChangeMidDay(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 23, 45, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-zone-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)

	// Amsterdam local already 29th
	code, _, raw := postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T23:30:00Z", "time_zone": "Europe/Amsterdam", "utc_offset_minutes": 60,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T23:30:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 10,
			"data": map[string]any{"exes": []any{"ams.exe"}},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	// America/New_York still 28th (UTC-5)
	code, _, raw = postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T23:30:00Z", "time_zone": "America/New_York", "utc_offset_minutes": -300,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T23:30:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 10,
			"data": map[string]any{"exes": []any{"ny.exe"}},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	_, u29, _ := getUsageJSON(t, base, pat, machineID, "2026-11-29")
	_, u28, _ := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	if len(u29["apps"].([]any)) != 1 || u29["apps"].([]any)[0].(map[string]any)["exe"] != "ams.exe" {
		t.Fatalf("29=%v", u29)
	}
	if len(u28["apps"].([]any)) != 1 || u28["apps"].([]any)[0].(map[string]any)["exe"] != "ny.exe" {
		t.Fatalf("28=%v", u28)
	}
}

func TestFacts_ClockSkew(t *testing.T) {
	t.Parallel()
	serverNow := time.Date(2026, 11, 28, 12, 10, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-skew-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(serverNow)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)

	// sent_at 10 min behind; ts = sent_at - 1m → ts_server = server_now - 1m
	code, _, raw := postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T11:59:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 10,
			"data": map[string]any{"exes": []any{"a.exe"}},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	// Usage lands on 2026-11-28 (ts_server = 12:09)
	code, u, _ := getUsageJSON(t, base, pat, machineID, "2026-11-28")
	if code != http.StatusOK || len(u["apps"].([]any)) != 1 {
		t.Fatalf("usage=%v", u)
	}
}

func TestFacts_TitleTruncate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-title-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)

	title := strings.Repeat("あ", 300)
	code, _, raw := postFactsMap(t, base, agent, map[string]any{
		"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T12:00:00Z", "kind": "fg.sample", "plugin": "p", "dur_s": 1,
			"data": map[string]any{"exe": "n.exe", "pid": 1, "title": title, "idle_s": 0},
		}},
	})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if st.FactCount() != 1 {
		t.Fatal(st.FactCount())
	}
	// Peek stored title via memory map is private; verify via re-read isn't exposed —
	// assert UTF-8 rune count of truncated value through a second post that would fail
	// if we rejected >256. Truncation is covered by usage package unit tests; here we
	// only assert accept of 300-rune title.
	_ = utf8.RuneCountInString(title)
}

func TestFacts_ValidationMatrix(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-val-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)
	streamID := uuid.NewString()

	baseFact := func() map[string]any {
		return map[string]any{
			"machine_id": machineID, "stream_id": streamID, "seq": 1,
			"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
			"facts": []any{map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 1,
				"data": map[string]any{"exes": []any{}},
			}},
		}
	}
	must400 := func(name string, mutate func(map[string]any)) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := baseFact()
			mutate(body)
			code, _, raw := postFactsMap(t, base, agent, body)
			if code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", code, raw)
			}
			assertErrorResponse(t, raw)
			if st.FactCount() != 0 {
				t.Fatalf("wrote facts on validation failure: %d", st.FactCount())
			}
		})
	}
	must400("dur_s_0_sample", func(b map[string]any) {
		b["facts"].([]any)[0].(map[string]any)["dur_s"] = 0
	})
	must400("dur_s_301", func(b map[string]any) {
		b["facts"].([]any)[0].(map[string]any)["dur_s"] = 301
	})
	must400("dur_s_nonzero_queue", func(b map[string]any) {
		b["facts"] = []any{map[string]any{
			"ts": "2026-11-28T12:00:00Z", "kind": "queue.dropped", "plugin": "p", "dur_s": 1,
			"data": map[string]any{"dropped_batches": 1, "dropped_bytes": 2},
		}}
	})
	must400("ts_after_sent_at", func(b map[string]any) {
		b["facts"].([]any)[0].(map[string]any)["ts"] = "2026-11-28T12:00:01Z"
	})
	must400("ts_older_than_retention", func(b map[string]any) {
		b["facts"].([]any)[0].(map[string]any)["ts"] = "2026-10-01T12:00:00Z"
		b["sent_at"] = "2026-10-01T12:00:00Z"
	})
	must400("unknown_zone", func(b map[string]any) {
		b["time_zone"] = "Not/ARealZone"
	})
	must400("empty_zone", func(b map[string]any) {
		b["time_zone"] = ""
	})
	must400("offset_mismatch", func(b map[string]any) {
		b["utc_offset_minutes"] = 60
	})
	must400("bad_machine", func(b map[string]any) {
		b["machine_id"] = "not-a-uuid"
	})
	must400("bad_stream", func(b map[string]any) {
		b["stream_id"] = "not-a-uuid"
	})
	must400("501_facts", func(b map[string]any) {
		items := make([]any, store.FactBatchMaxCount+1)
		for i := range items {
			items[i] = map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 1,
				"data": map[string]any{"exes": []any{}},
			}
		}
		b["facts"] = items
	})
	must400("one_bad_of_many", func(b map[string]any) {
		items := make([]any, 100)
		for i := range items {
			items[i] = map[string]any{
				"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 1,
				"data": map[string]any{"exes": []any{}},
			}
		}
		items[50].(map[string]any)["dur_s"] = 0
		b["facts"] = items
	})

	t.Run("empty_facts_ok", func(t *testing.T) {
		t.Parallel()
		code, ack, raw := postFactsMap(t, base, agent, map[string]any{
			"machine_id": machineID, "stream_id": uuid.NewString(), "seq": 1,
			"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
			"facts": []any{},
		})
		if code != http.StatusOK || ack["duplicate"] != false {
			t.Fatalf("%d %v %s", code, ack, raw)
		}
	})

	t.Run("body_413", func(t *testing.T) {
		t.Parallel()
		pad := strings.Repeat("x", store.FactBatchMaxBytes)
		oversized := fmt.Sprintf(
			`{"machine_id":%q,"stream_id":%q,"seq":1,"sent_at":"2026-11-28T12:00:00Z","time_zone":"UTC","utc_offset_minutes":0,"facts":[{"ts":"2026-11-28T12:00:00Z","kind":"proc.sample","plugin":"p","dur_s":1,"data":{"exes":[],"pad":%q}}]}`,
			machineID, uuid.NewString(), pad,
		)
		code, raw := postFactsRaw(t, base, agent, []byte(oversized))
		if code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d body=%s", code, raw)
		}
	})
}

func TestFacts_AuthAndTenantIsolation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, patA := seedTenantPAT(t, st, "facts-isoA-"+uuid.NewString()[:8])
	_, patB := seedTenantPAT(t, st, "facts-isoB-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agentA := createAgentToken(t, base, patA)
	agentB := createAgentToken(t, base, patB)
	machineA := uuid.NewString()
	_ = enrollMachine(t, base, agentA, machineA, "UTC", 0)

	body := map[string]any{
		"machine_id": machineA, "stream_id": uuid.NewString(), "seq": 1,
		"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
		"facts": []any{map[string]any{
			"ts": "2026-11-28T12:00:00Z", "kind": "proc.sample", "plugin": "p", "dur_s": 10,
			"data": map[string]any{"exes": []any{"a.exe"}},
		}},
	}
	code, _, raw := postFactsMap(t, base, agentA, body)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	// Tenant B cannot read usage
	code, _, raw = getUsageJSON(t, base, patB, machineA, "2026-11-28")
	if code != http.StatusNotFound {
		t.Fatalf("cross-tenant usage want 404 got %d %s", code, raw)
	}
	// Agent B posting A's machine_id writes into B's tenant (different key) — never into A.
	before := st.FactCount()
	code, _, raw = postFactsMap(t, base, agentB, body)
	if code != http.StatusOK {
		t.Fatalf("agentB post %d %s", code, raw)
	}
	if st.FactCount() != before+1 {
		t.Fatalf("expected B's own fact row, count=%d before=%d", st.FactCount(), before)
	}
	code, uA, _ := getUsageJSON(t, base, patA, machineA, "2026-11-28")
	if code != http.StatusOK || int(uA["apps"].([]any)[0].(map[string]any)["seconds"].(float64)) != 10 {
		t.Fatalf("tenant A usage polluted: %v", uA)
	}

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()
		code, raw := postFactsRaw(t, base, "", []byte(`{}`))
		if code != http.StatusUnauthorized {
			t.Fatalf("%d %s", code, raw)
		}
		code, _, raw = getUsageJSON(t, base, "", machineA, "2026-11-28")
		if code != http.StatusUnauthorized {
			t.Fatalf("%d %s", code, raw)
		}
	})
	t.Run("pat_on_agent_route", func(t *testing.T) {
		t.Parallel()
		raw, _ := json.Marshal(body)
		code, out := postFactsRaw(t, base, patA, raw)
		if code != http.StatusUnauthorized {
			t.Fatalf("%d %s", code, out)
		}
	})
	t.Run("agent_on_control_route", func(t *testing.T) {
		t.Parallel()
		code, _, raw := getUsageJSON(t, base, agentA, machineA, "2026-11-28")
		if code != http.StatusUnauthorized {
			t.Fatalf("%d %s", code, raw)
		}
	})
	t.Run("usage_missing_machine_404", func(t *testing.T) {
		t.Parallel()
		code, _, raw := getUsageJSON(t, base, patA, uuid.NewString(), "2026-11-28")
		if code != http.StatusNotFound {
			t.Fatalf("%d %s", code, raw)
		}
	})
	t.Run("usage_empty_day", func(t *testing.T) {
		t.Parallel()
		code, u, raw := getUsageJSON(t, base, patA, machineA, "2026-11-27")
		if code != http.StatusOK {
			t.Fatalf("%d %s", code, raw)
		}
		apps, ok := u["apps"].([]any)
		if !ok || apps == nil || len(apps) != 0 {
			t.Fatalf("apps=%v", u["apps"])
		}
	})
	t.Run("heartbeat_invalid_zone", func(t *testing.T) {
		t.Parallel()
		code, raw := postHeartbeat(t, base, agentA, map[string]any{
			"machine_id": machineA, "agent_version": "0.0.1", "hostname": "pc",
			"os_version": "Win", "applied_policy_version": 0,
			"time_zone": "Not/AZone", "utc_offset_minutes": 0,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("%d %s", code, raw)
		}
	})
}

func TestFacts_BodySizeExactOK(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-lim-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	streamID := uuid.NewString()
	_ = enrollMachine(t, base, agent, machineID, "UTC", 0)

	prefix := fmt.Sprintf(
		`{"machine_id":%q,"stream_id":%q,"seq":1,"sent_at":"2026-11-28T12:00:00Z","time_zone":"UTC","utc_offset_minutes":0,"facts":[{"ts":"2026-11-28T12:00:00Z","kind":"session.restarted","plugin":"p","dur_s":0,"data":{"pad":"`,
		machineID, streamID,
	)
	suffix := `"}}]}`
	need := store.FactBatchMaxBytes - len(prefix) - len(suffix)
	exact := prefix + strings.Repeat("y", need) + suffix
	code, raw := postFactsRaw(t, base, agent, []byte(exact))
	if code != http.StatusOK {
		t.Fatalf("exact-size status=%d body=%s", code, raw)
	}
}

func TestContractNegatives_ProductionPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	st := memory.New()
	_, pat := seedTenantPAT(t, st, "facts-neg-"+uuid.NewString()[:8])
	base := startTestServer(t, st, server.Config{Clock: fixedClock(now)})
	agent := createAgentToken(t, base, pat)
	machineID := uuid.NewString()
	streamID := uuid.NewString()

	t.Run("heartbeat_missing_time_zone_400", func(t *testing.T) {
		t.Parallel()
		code, raw := postHeartbeat(t, base, agent, map[string]any{
			"machine_id": machineID, "agent_version": "0.0.1", "hostname": "pc",
			"os_version": "Win", "applied_policy_version": 0,
		})
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
	})
	t.Run("facts_seq_zero_400", func(t *testing.T) {
		t.Parallel()
		code, _, raw := postFactsMap(t, base, agent, map[string]any{
			"machine_id": machineID, "stream_id": streamID, "seq": 0,
			"sent_at": "2026-11-28T12:00:00Z", "time_zone": "UTC", "utc_offset_minutes": 0,
			"facts": []any{},
		})
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
	})
	t.Run("usage_empty_date_400", func(t *testing.T) {
		t.Parallel()
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=", pat, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", code, raw)
		}
	})
	t.Run("usage_tz_ignored_still_works", func(t *testing.T) {
		t.Parallel()
		_ = enrollMachine(t, base, agent, machineID, "UTC", 0)
		code, _, raw := doJSON(t, http.MethodGet, base+"/v1/machines/"+machineID+"/usage?date=2026-11-28&tz=UTC", pat, nil)
		if code != http.StatusOK {
			t.Fatalf("status=%d body=%s", code, raw)
		}
	})
}

func enrollMachine(t *testing.T, base, agent, machineID, tz string, offset int) string {
	t.Helper()
	code, raw := postHeartbeat(t, base, agent, map[string]any{
		"machine_id": machineID, "agent_version": "0.0.1", "hostname": "pc",
		"os_version": "Win", "applied_policy_version": 0,
		"time_zone": tz, "utc_offset_minutes": offset,
	})
	if code != http.StatusOK {
		t.Fatalf("enroll heartbeat %d %s", code, raw)
	}
	return machineID
}
