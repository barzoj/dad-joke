// Package sampler is the service.Agent that runs sensors on sample_interval
// and enqueues AgentAPI PostFactsRequest batches into factsq.
package sampler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/sensor"
	"github.com/barzoj/dad-joke/internal/sensor/sessions"
	"github.com/barzoj/dad-joke/internal/tzmap"
	"github.com/barzoj/dad-joke/internal/winsess"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Logger is a minimal log sink.
type Logger interface {
	Log(format string, args ...any)
}

// NopLogger discards logs.
type NopLogger struct{}

// Log implements Logger.
func (NopLogger) Log(string, ...any) {}

// WriterLogger logs to an io.Writer.
type WriterLogger struct{ W io.Writer }

// Log implements Logger.
func (l WriterLogger) Log(format string, args ...any) {
	if l.W == nil {
		return
	}
	_, _ = fmt.Fprintf(l.W, format+"\n", args...)
}

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// BuildEnqueuer stores immutable batch bytes with seq assigned at enqueue.
type BuildEnqueuer interface {
	StreamID() string
	EnqueueBuild(build func(streamID string, seq int64, dropped factsq.Dropped) ([]byte, error)) (seq int64, dropped factsq.Dropped, err error)
}

// Loop samples sensors and enqueues facts batches.
type Loop struct {
	ConfigPath string
	Config     *config.Config
	Queue      BuildEnqueuer
	Sensors    sensor.Registry
	Sessions   *sessions.Sensor // optional; Reset() on wall-clock jump / PowerEvents
	Log        Logger
	Clock      Clock
	Zone       func(now time.Time) (iana string, offsetMinutes int32)

	mu sync.Mutex
}

// New returns a production sampler with the sessions sensor and default queue.
func New(q BuildEnqueuer) *Loop {
	paths := installer.DefaultPaths()
	if q == nil {
		q = &factsq.Queue{Dir: filepath.Join(paths.DataDir, "facts")}
	}
	sess := sessions.New(winsess.Default(), time.Duration(config.DefaultSampleIntervalSeconds)*time.Second)
	return &Loop{
		ConfigPath: paths.ConfigPath,
		Queue:      q,
		Sensors:    sensor.Registry{sess},
		Sessions:   sess,
		Log:        WriterLogger{W: os.Stderr},
		Clock:      RealClock{},
	}
}

// Reset clears sensor previous-sample state (sleep/resume hook for T-21).
func (l *Loop) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Sessions != nil {
		l.Sessions.Reset()
	}
}

// Run implements service.Agent.
func (l *Loop) Run(ctx context.Context) error {
	if l.Log == nil {
		l.Log = NopLogger{}
	}
	if l.Clock == nil {
		l.Clock = RealClock{}
	}
	if l.Zone == nil {
		l.Zone = tzmap.Resolve
	}
	if l.ConfigPath == "" {
		l.ConfigPath = installer.DefaultPaths().ConfigPath
	}
	if l.Queue == nil {
		paths := installer.DefaultPaths()
		l.Queue = &factsq.Queue{Dir: filepath.Join(paths.DataDir, "facts")}
	}
	if len(l.Sensors) == 0 {
		sess := sessions.New(winsess.Default(), time.Duration(config.DefaultSampleIntervalSeconds)*time.Second)
		l.Sessions = sess
		l.Sensors = sensor.Registry{sess}
	}

	cfg, err := l.loadConfig()
	if err != nil {
		l.Log.Log("sampler: load config failed (idle until stop): %v", err)
		return l.idle(ctx)
	}
	interval := time.Duration(cfg.SampleIntervalSeconds) * time.Second
	if l.Sessions != nil {
		l.Sessions.Interval = interval
	}
	l.Log.Log("sampler: loop starting (interval=%s machine_id=%s)", interval, cfg.MachineID)

	l.safeSample(ctx, cfg)

	for {
		timer := l.Clock.After(interval)
		select {
		case <-ctx.Done():
			l.Log.Log("sampler: loop stopping")
			return ctx.Err()
		case <-timer:
			next, loadErr := l.loadConfig()
			if loadErr != nil {
				l.Log.Log("sampler: reload config failed (continuing): %v", loadErr)
			} else {
				cfg = next
				interval = time.Duration(cfg.SampleIntervalSeconds) * time.Second
				if l.Sessions != nil {
					l.Sessions.Interval = interval
				}
			}
			l.safeSample(ctx, cfg)
		}
	}
}

func (l *Loop) idle(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (l *Loop) loadConfig() (config.Config, error) {
	if l.Config != nil {
		if err := l.Config.Validate(); err != nil {
			return config.Config{}, err
		}
		return *l.Config, nil
	}
	return config.LoadFile(l.ConfigPath)
}

func (l *Loop) safeSample(ctx context.Context, cfg config.Config) {
	if err := ctx.Err(); err != nil {
		return
	}
	if err := l.sampleOnce(ctx, cfg); err != nil {
		if err == context.Canceled {
			return
		}
		l.Log.Log("sampler: sample failed (continuing): %v", err)
	}
}

func (l *Loop) sampleOnce(ctx context.Context, cfg config.Config) error {
	now := l.Clock.Now()
	facts := l.Sensors.CollectAll(ctx, now, func(_ sensor.Sensor, err error) {
		l.Log.Log("sampler: sensor error (continuing): %v", err)
	})

	zone, offset := l.Zone(now)
	var machineID openapi_types.UUID
	if err := machineID.UnmarshalText([]byte(cfg.MachineID)); err != nil {
		return fmt.Errorf("machine_id: %w", err)
	}

	_, _, err := l.Queue.EnqueueBuild(func(streamID string, seq int64, dropped factsq.Dropped) ([]byte, error) {
		sid, err := parseUUID(streamID)
		if err != nil {
			return nil, err
		}
		batchFacts := append([]agentapi.FactItem(nil), facts...)
		if dropped.Batches > 0 {
			batchFacts = append([]agentapi.FactItem{droppedFact(now, dropped)}, batchFacts...)
		}
		body := agentapi.PostFactsRequest{
			MachineId:        machineID,
			StreamId:         sid,
			Seq:              seq,
			SentAt:           now.UTC(),
			TimeZone:         zone,
			UtcOffsetMinutes: &offset,
			Facts:            batchFacts,
		}
		return json.Marshal(body)
	})
	return err
}

func droppedFact(now time.Time, d factsq.Dropped) agentapi.FactItem {
	n := d.Batches
	if n < 0 {
		n = 0
	}
	if n > int(^uint32(0)>>1) {
		n = int(^uint32(0) >> 1)
	}
	batches := int32(n) // #nosec G115 -- clamped to int32 range above
	bytes := d.Bytes
	return agentapi.FactItem{
		Ts:     now.UTC(),
		Kind:   agentapi.QueueDropped,
		Plugin: "factsq",
		DurS:   0,
		Data: agentapi.FactData{
			DroppedBatches: &batches,
			DroppedBytes:   &bytes,
		},
	}
}

func parseUUID(s string) (openapi_types.UUID, error) {
	var id openapi_types.UUID
	err := id.UnmarshalText([]byte(strings.TrimSpace(s)))
	return id, err
}
