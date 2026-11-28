package sessionagent

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/google/uuid"
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

// TestQueueSinkClampsDroppedBatchesInt32 would fail if DroppedBatches were
// assigned from int without a bounds check (gosec G115).
func TestQueueSinkClampsDroppedBatchesInt32(t *testing.T) {
	t.Parallel()
	cfg := config.WithTestIdentity(config.Default())
	mq := &captureQueue{stream: uuid.NewString()}
	sink := &QueueSink{
		Config: &cfg,
		Queue:  mq,
		Clock:  newMemClock(time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)),
		Zone:   func(time.Time) (string, int32) { return "UTC", 0 },
	}
	mq.forceDrop = factsq.Dropped{Batches: math.MaxInt, Bytes: 9}
	if err := sink.EnqueueRestarted(1); err != nil {
		t.Fatal(err)
	}
	var body agentapi.PostFactsRequest
	if err := json.Unmarshal(mq.data, &body); err != nil {
		t.Fatal(err)
	}
	var dropped *agentapi.FactItem
	for i := range body.Facts {
		if body.Facts[i].Kind == agentapi.QueueDropped {
			dropped = &body.Facts[i]
			break
		}
	}
	if dropped == nil || dropped.Data.DroppedBatches == nil {
		t.Fatalf("missing queue.dropped: %+v", body.Facts)
	}
	if *dropped.Data.DroppedBatches != math.MaxInt32 {
		t.Fatalf("DroppedBatches=%d want MaxInt32 clamp", *dropped.Data.DroppedBatches)
	}
}

type captureQueue struct {
	stream    string
	forceDrop factsq.Dropped
	data      []byte
}

func (c *captureQueue) EnqueueBuild(build func(streamID string, seq int64, dropped factsq.Dropped) ([]byte, error)) (int64, factsq.Dropped, error) {
	data, err := build(c.stream, 1, c.forceDrop)
	c.data = data
	return 1, c.forceDrop, err
}
