package sessions

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/winsess"
)

type fakeSource struct {
	sessions []winsess.Session
	procs    []winsess.Process
	errS     error
	errP     error
}

func (f fakeSource) Sessions(context.Context) ([]winsess.Session, error) {
	return f.sessions, f.errS
}
func (f fakeSource) Processes(context.Context) ([]winsess.Process, error) {
	return f.procs, f.errP
}

func TestCollectZeroSessions(t *testing.T) {
	t.Parallel()
	s := New(fakeSource{}, time.Minute)
	facts, err := s.Collect(context.Background(), time.Now())
	if err != nil || len(facts) != 0 {
		t.Fatalf("facts=%d err=%v", len(facts), err)
	}
}

func TestCollectOneActive(t *testing.T) {
	t.Parallel()
	src := fakeSource{
		sessions: []winsess.Session{{ID: 1, UserName: "kid", UserSID: "S-1-5-21-1", State: winsess.StateActive}},
		procs: []winsess.Process{
			{SessionID: 1, PID: 10, Exe: `C:\Windows\notepad.exe`, OwnerSID: "S-1-5-21-1"},
			{SessionID: 1, PID: 11, Exe: `C:\Windows\System32\svchost.exe`, OwnerSID: "S-1-5-18"}, // SYSTEM excluded
			{SessionID: 1, PID: 12, Exe: `D:\Apps\Chrome.EXE`, OwnerSID: "S-1-5-21-1"},
			{SessionID: 1, PID: 13, Exe: `C:\a\notepad.exe`, OwnerSID: "S-1-5-21-1"}, // dedupe
		},
	}
	s := New(src, time.Minute)
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	facts, err := s.Collect(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts=%d", len(facts))
	}
	if facts[0].Kind != agentapi.SessionState || facts[1].Kind != agentapi.ProcSample {
		t.Fatalf("kinds %v %v", facts[0].Kind, facts[1].Kind)
	}
	if facts[0].DurS != 60 {
		t.Fatalf("first dur=%d", facts[0].DurS)
	}
	exes := *facts[1].Data.Exes
	if len(exes) != 2 || exes[0] != "Chrome.EXE" || exes[1] != "notepad.exe" {
		t.Fatalf("exes=%v", exes)
	}
}

func TestCollectLockedDisconnectedSkipSession0(t *testing.T) {
	t.Parallel()
	src := fakeSource{
		sessions: []winsess.Session{
			{ID: 0, UserName: "", UserSID: "", State: winsess.StateActive}, // skipped
			{ID: 2, UserName: "a", UserSID: "S-1-5-21-a", State: winsess.StateLocked},
			{ID: 3, UserName: "b", UserSID: "S-1-5-21-b", State: winsess.StateDisconnected},
		},
	}
	s := New(src, 30*time.Second)
	facts, err := s.Collect(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 4 {
		t.Fatalf("facts=%d", len(facts))
	}
	if *facts[0].Data.State != agentapi.Locked {
		t.Fatalf("state=%v", *facts[0].Data.State)
	}
	if *facts[2].Data.State != agentapi.Disconnected {
		t.Fatalf("state=%v", *facts[2].Data.State)
	}
}

func TestCollectMissingSIDNoPanic(t *testing.T) {
	t.Parallel()
	src := fakeSource{
		sessions: []winsess.Session{{ID: 1, UserName: "x", UserSID: "", State: winsess.StateActive}},
	}
	s := New(src, time.Minute)
	facts, err := s.Collect(context.Background(), time.Now())
	if err != nil || len(facts) != 2 {
		t.Fatalf("%v %d", err, len(facts))
	}
	if facts[0].UserSid != nil {
		t.Fatal("expected empty sid omitted")
	}
}

func TestCollectUnicodeBasenameAndCap(t *testing.T) {
	t.Parallel()
	procs := make([]winsess.Process, 0, 250)
	for i := 0; i < 250; i++ {
		procs = append(procs, winsess.Process{
			OwnerSID: "S-1-5-21-u",
			Exe:      fmt.Sprintf(`C:\Apps\файл %d.exe`, i),
		})
	}
	src := fakeSource{
		sessions: []winsess.Session{{ID: 1, UserName: "u", UserSID: "S-1-5-21-u", State: winsess.StateActive}},
		procs:    procs,
	}
	s := New(src, time.Minute)
	facts, err := s.Collect(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	exes := *facts[1].Data.Exes
	if len(exes) != 200 {
		t.Fatalf("capped=%d", len(exes))
	}
	if !strings.HasPrefix(exes[0], "файл") {
		t.Fatalf("unicode basename lost: %q", exes[0])
	}
}

func TestCollectWTSError(t *testing.T) {
	t.Parallel()
	s := New(fakeSource{errS: fmt.Errorf("wts down")}, time.Minute)
	_, err := s.Collect(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDurSArithmetic(t *testing.T) {
	t.Parallel()
	s := New(fakeSource{
		sessions: []winsess.Session{{ID: 1, UserName: "u", UserSID: "S-1-5-21", State: winsess.StateActive}},
	}, time.Minute)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f1, _ := s.Collect(context.Background(), t0)
	if f1[0].DurS != 60 {
		t.Fatalf("first=%d", f1[0].DurS)
	}
	f2, _ := s.Collect(context.Background(), t0.Add(60*time.Second))
	if f2[0].DurS != 60 {
		t.Fatalf("normal=%d", f2[0].DurS)
	}
	f3, _ := s.Collect(context.Background(), t0.Add(60*time.Second+90*time.Second))
	if f3[0].DurS != 90 {
		t.Fatalf("late=%d", f3[0].DurS)
	}
	// Exactly 2×interval → clamp to max (not a sleep reset)
	f4, _ := s.Collect(context.Background(), t0.Add(60*time.Second+90*time.Second+120*time.Second))
	if f4[0].DurS != 120 {
		t.Fatalf("clamped=%d", f4[0].DurS)
	}
	// Clock backwards → interval
	f5, _ := s.Collect(context.Background(), t0)
	if f5[0].DurS != 60 {
		t.Fatalf("backwards=%d", f5[0].DurS)
	}
	// Sleep jump > 2×interval → reset to interval
	f6, _ := s.Collect(context.Background(), t0.Add(10*time.Minute))
	if f6[0].DurS != 60 {
		t.Fatalf("sleep jump=%d", f6[0].DurS)
	}
}

func TestReset(t *testing.T) {
	t.Parallel()
	s := New(fakeSource{
		sessions: []winsess.Session{{ID: 1, UserName: "u", UserSID: "S-1-5-21", State: winsess.StateActive}},
	}, time.Minute)
	t0 := time.Now()
	_, _ = s.Collect(context.Background(), t0)
	s.Reset()
	f, _ := s.Collect(context.Background(), t0.Add(time.Hour))
	if f[0].DurS != 60 {
		t.Fatalf("after reset=%d", f[0].DurS)
	}
}
