package sampler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/sensor"
	"github.com/barzoj/dad-joke/internal/winsess"
)

type logCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (l *logCapture) Log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, fmt.Sprintf(format, args...))
}

func (l *logCapture) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.msgs, "\n")
}

type staticSensor struct {
	facts []sensor.Fact
}

func (s staticSensor) Collect(context.Context, time.Time) ([]sensor.Fact, error) {
	return s.facts, nil
}

func testSampleCfg() config.Config {
	cfg := config.WithTestIdentity(config.Default())
	cfg.SampleIntervalSeconds = 15
	return cfg
}

func oneSessionFact() []sensor.Fact {
	sid := "S-1-5-21-1"
	state := agentapi.Active
	sessionID := "1"
	uname := "kid"
	return []sensor.Fact{{
		Ts: time.Now().UTC(), Kind: agentapi.SessionState, Plugin: "sessions", DurS: 15,
		UserSid: &sid,
		Data:    agentapi.FactData{SessionId: &sessionID, Username: &uname, State: &state},
	}}
}

func TestSampleEnqueuesBatchWithZone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &factsq.Queue{Dir: dir}
	cfg := testSampleCfg()
	sid := "S-1-5-21-1"
	state := agentapi.Active
	sessionID := "1"
	uname := "kid"
	exes := []string{"notepad.exe"}
	loop := &Loop{
		Config: &cfg,
		Queue:  q,
		Log:    NopLogger{},
		Clock:  RealClock{},
		Zone:   func(time.Time) (string, int32) { return "Europe/Amsterdam", 60 },
		Sensors: sensor.Registry{staticSensor{facts: []sensor.Fact{{
			Ts: time.Now().UTC(), Kind: agentapi.SessionState, Plugin: "sessions", DurS: 15,
			UserSid: &sid,
			Data:    agentapi.FactData{SessionId: &sessionID, Username: &uname, State: &state},
		}, {
			Ts: time.Now().UTC(), Kind: agentapi.ProcSample, Plugin: "sessions", DurS: 15,
			UserSid: &sid,
			Data:    agentapi.FactData{Exes: &exes},
		}}},
		},
	}
	if err := loop.sampleOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	seq, data, ok, err := q.Peek()
	if err != nil || !ok || seq != 1 {
		t.Fatalf("peek seq=%d ok=%v err=%v", seq, ok, err)
	}
	var body agentapi.PostFactsRequest
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body.TimeZone != "Europe/Amsterdam" || body.UtcOffsetMinutes == nil || *body.UtcOffsetMinutes != 60 {
		t.Fatalf("zone=%q offset=%v", body.TimeZone, body.UtcOffsetMinutes)
	}
	if len(body.Facts) != 2 {
		t.Fatalf("facts=%d", len(body.Facts))
	}
	_ = winsess.Default() // ensure package linked for stub
}

func TestSampleSkipsEmptyFactsNoEnqueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &factsq.Queue{Dir: dir, Cap: 1 << 20, ApplyACL: func(string) error { return nil }}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	before := q.NextSeq()
	cfg := testSampleCfg()
	loop := &Loop{
		Config:  &cfg,
		Queue:   q,
		Log:     NopLogger{},
		Clock:   RealClock{},
		Zone:    func(time.Time) (string, int32) { return "UTC", 0 },
		Sensors: sensor.Registry{staticSensor{}}, // 0 facts
	}
	if err := loop.sampleOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if q.NextSeq() != before {
		t.Fatalf("NextSeq = %d, want unchanged %d", q.NextSeq(), before)
	}
	pending, err := q.PendingSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %v, want empty (no enqueue)", pending)
	}
}

func TestResetClearsSessions(t *testing.T) {
	t.Parallel()
	loop := New(nil)
	loop.ConfigPath = "unused"
	loop.Reset() // must not panic when Sessions set by New
}

// failCreateTempFS injects CreateTemp errors after Open (ENOSPC / EACCES).
type failCreateTempFS struct {
	factsq.OSFS
	err   error
	armed bool
}

func (f *failCreateTempFS) CreateTemp(dir, pattern string) (*os.File, error) {
	if f.armed {
		return nil, f.err
	}
	return f.OSFS.CreateTemp(dir, pattern)
}

func TestSampleContinuesOnENOSPC(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	failFS := &failCreateTempFS{}
	q := &factsq.Queue{Dir: dir, Cap: 1 << 20, ApplyACL: func(string) error { return nil }, FS: failFS}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	cfg := testSampleCfg()
	loop := &Loop{
		Config:  &cfg,
		Queue:   q,
		Log:     NopLogger{},
		Clock:   RealClock{},
		Zone:    func(time.Time) (string, int32) { return "UTC", 0 },
		Sensors: sensor.Registry{staticSensor{facts: oneSessionFact()}},
	}
	if err := loop.sampleOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	first, data, ok, err := q.Peek()
	if err != nil || !ok || first != 1 {
		t.Fatalf("first peek seq=%d ok=%v err=%v", first, ok, err)
	}

	failFS.armed = true
	failFS.err = errors.New("no space left on device")
	logs := &logCapture{}
	loop.Log = logs
	loop.safeSample(context.Background(), cfg)
	if !strings.Contains(logs.joined(), "sample failed (continuing)") {
		t.Fatalf("expected continuing log, got %q", logs.joined())
	}
	failFS.armed = false
	seq, got, ok, err := q.Peek()
	if err != nil || !ok || seq != 1 {
		t.Fatalf("queue damaged: seq=%d ok=%v err=%v", seq, ok, err)
	}
	if string(got) != string(data) {
		t.Fatal("existing batch mutated after ENOSPC")
	}
}

func TestSampleContinuesOnReadOnlyQueue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &factsq.Queue{Dir: dir, Cap: 1 << 20, ApplyACL: func(string) error { return nil }}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	cfg := testSampleCfg()
	loop := &Loop{
		Config:  &cfg,
		Queue:   q,
		Log:     NopLogger{},
		Clock:   RealClock{},
		Zone:    func(time.Time) (string, int32) { return "UTC", 0 },
		Sensors: sensor.Registry{staticSensor{facts: oneSessionFact()}},
	}
	if err := loop.sampleOnce(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	logs := &logCapture{}
	loop.Log = logs
	loop.safeSample(context.Background(), cfg)
	if !strings.Contains(logs.joined(), "sample failed (continuing)") {
		t.Fatalf("expected continuing log, got %q", logs.joined())
	}
	pending, err := q.PendingSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != 1 {
		t.Fatalf("PendingSeqs = %v, want [1]", pending)
	}
}
