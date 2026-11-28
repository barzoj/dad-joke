package sessionagent

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
)

func TestQueueSinkEnqueueRestarted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &factsq.Queue{Dir: dir}
	cfg := config.WithTestIdentity(config.Default())
	sink := &QueueSink{
		Config: &cfg,
		Queue:  q,
		Clock:  newMemClock(time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)),
		Zone:   func(time.Time) (string, int32) { return "UTC", 0 },
	}
	if err := sink.EnqueueRestarted(7); err != nil {
		t.Fatal(err)
	}
	_, data, ok, err := q.Peek()
	if err != nil || !ok {
		t.Fatalf("peek: ok=%v err=%v", ok, err)
	}
	var body agentapi.PostFactsRequest
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Facts) != 1 || body.Facts[0].Kind != agentapi.SessionRestarted {
		t.Fatalf("facts=%+v", body.Facts)
	}
	if body.Facts[0].DurS != 0 {
		t.Fatalf("dur_s=%d", body.Facts[0].DurS)
	}
	if body.Facts[0].Data.SessionId == nil || *body.Facts[0].Data.SessionId != "7" {
		t.Fatalf("session_id=%v", body.Facts[0].Data.SessionId)
	}
	_ = filepath.Separator
}
