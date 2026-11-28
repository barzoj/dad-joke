package sampler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/sensor"
	"github.com/barzoj/dad-joke/internal/winsess"
)

type staticSensor struct {
	facts []sensor.Fact
}

func (s staticSensor) Collect(context.Context, time.Time) ([]sensor.Fact, error) {
	return s.facts, nil
}

func TestSampleEnqueuesBatchWithZone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &factsq.Queue{Dir: dir}
	cfg := config.WithTestIdentity(config.Default())
	cfg.SampleIntervalSeconds = 15
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

func TestResetClearsSessions(t *testing.T) {
	t.Parallel()
	loop := New(nil)
	loop.ConfigPath = "unused"
	loop.Reset() // must not panic when Sessions set by New
}
