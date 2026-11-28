package server

import (
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/store"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func TestValidateMachineTimeZone(t *testing.T) {
	t.Parallel()
	if err := validateMachineTimeZone("UTC", 0); err != nil {
		t.Fatal(err)
	}
	if err := validateMachineTimeZone("", 0); err == nil {
		t.Fatal("empty time_zone")
	}
	if err := validateMachineTimeZone(strings.Repeat("a", 65), 0); err == nil {
		t.Fatal("time_zone too long")
	}
	if err := validateMachineTimeZone("UTC", 841); err == nil {
		t.Fatal("offset too large")
	}
	if err := validateMachineTimeZone("UTC", -841); err == nil {
		t.Fatal("offset too small")
	}
}

func TestValidateUsageDate(t *testing.T) {
	t.Parallel()
	if err := validateUsageDate(openapi_types.Date{}); err == nil {
		t.Fatal("zero date")
	}
	d := openapi_types.Date{Time: time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)}
	if err := validateUsageDate(d); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePostFactsRequest(t *testing.T) {
	t.Parallel()
	valid := &agentapi.PostFactsRequest{
		MachineId:        mustParseUUID(t, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		StreamId:         mustParseUUID(t, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"),
		Seq:              1,
		SentAt:           time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC),
		TimeZone:         "UTC",
		UtcOffsetMinutes: 0,
		Facts:            []agentapi.FactItem{},
	}
	if err := validatePostFactsRequest(valid); err != nil {
		t.Fatal(err)
	}
	bad := *valid
	bad.Seq = 0
	if err := validatePostFactsRequest(&bad); err == nil {
		t.Fatal("seq 0")
	}
	bad = *valid
	bad.Facts = make([]agentapi.FactItem, store.FactBatchMaxCount+1)
	for i := range bad.Facts {
		bad.Facts[i] = agentapi.FactItem{
			Ts: time.Now().UTC(), Kind: agentapi.ProcSample, Plugin: "p", DurS: 1,
			Data: agentapi.FactData{Exes: &[]string{}},
		}
	}
	if err := validatePostFactsRequest(&bad); err == nil {
		t.Fatal("501 facts")
	}
	long := strings.Repeat("x", factExeMaxLen+1)
	bad = *valid
	bad.Facts = []agentapi.FactItem{{
		Ts: time.Now().UTC(), Kind: agentapi.ProcSample, Plugin: "p", DurS: 1,
		Data: agentapi.FactData{Exes: &[]string{long}},
	}}
	if err := validatePostFactsRequest(&bad); err == nil {
		t.Fatal("exe item too long")
	}
	// Titles longer than max are accepted at contract-bound stage (truncated later).
	title := strings.Repeat("あ", store.FactTitleMaxLen+1)
	bad = *valid
	bad.Facts = []agentapi.FactItem{{
		Ts: time.Now().UTC(), Kind: agentapi.FgSample, Plugin: "p", DurS: 1,
		Data: agentapi.FactData{
			Exe: ptrExe("n.exe"), Pid: ptrInt32(1), Title: &title, IdleS: ptrInt32(0),
		},
	}}
	if err := validatePostFactsRequest(&bad); err != nil {
		t.Fatalf("long title should pass contract validate: %v", err)
	}
}

func ptrExe(s string) *agentapi.DadJokeCommonExeName {
	return &s
}

func ptrInt32(v int32) *int32 { return &v }

func mustParseUUID(t *testing.T, s string) openapi_types.UUID {
	t.Helper()
	var id openapi_types.UUID
	if err := id.UnmarshalText([]byte(s)); err != nil {
		t.Fatal(err)
	}
	return id
}
