package sessionagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/tzmap"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

const pluginID = "sessionagent"

// QueueSink enqueues session.restarted markers into a durable factsq.
type QueueSink struct {
	ConfigPath string
	Config     *config.Config
	Queue      interface {
		EnqueueBuild(build func(streamID string, seq int64, dropped factsq.Dropped) ([]byte, error)) (seq int64, dropped factsq.Dropped, err error)
	}
	Clock Clock
	Zone  func(now time.Time) (iana string, offsetMinutes int32)
}

// NewQueueSink returns a production sink writing under ProgramData\DadJoke\facts.
func NewQueueSink() *QueueSink {
	paths := installer.DefaultPaths()
	return &QueueSink{
		ConfigPath: paths.ConfigPath,
		Queue:      &factsq.Queue{Dir: filepath.Join(paths.DataDir, "facts")},
		Clock:      RealClock{},
		Zone:       tzmap.Resolve,
	}
}

// EnqueueRestarted implements RestartSink.
func (s *QueueSink) EnqueueRestarted(sessionID uint32) error {
	if s.Queue == nil {
		return fmt.Errorf("sessionagent: nil queue")
	}
	if s.Clock == nil {
		s.Clock = RealClock{}
	}
	if s.Zone == nil {
		s.Zone = tzmap.Resolve
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	zone, offset := s.Zone(now)
	var machineID openapi_types.UUID
	if err := machineID.UnmarshalText([]byte(cfg.MachineID)); err != nil {
		return fmt.Errorf("machine_id: %w", err)
	}
	sid := strconv.FormatUint(uint64(sessionID), 10)
	_, _, err = s.Queue.EnqueueBuild(func(streamID string, seq int64, dropped factsq.Dropped) ([]byte, error) {
		var stream openapi_types.UUID
		if err := stream.UnmarshalText([]byte(streamID)); err != nil {
			return nil, err
		}
		facts := []agentapi.FactItem{restartedFact(now, sid)}
		if dropped.Batches > 0 {
			n := int32(dropped.Batches)
			b := dropped.Bytes
			facts = append([]agentapi.FactItem{{
				Ts:     now.UTC(),
				Kind:   agentapi.QueueDropped,
				Plugin: "factsq",
				DurS:   0,
				Data: agentapi.FactData{
					DroppedBatches: &n,
					DroppedBytes:   &b,
				},
			}}, facts...)
		}
		body := agentapi.PostFactsRequest{
			MachineId:        machineID,
			StreamId:         stream,
			Seq:              seq,
			SentAt:           now.UTC(),
			TimeZone:         zone,
			UtcOffsetMinutes: &offset,
			Facts:            facts,
		}
		return json.Marshal(body)
	})
	return err
}

func restartedFact(now time.Time, sessionID string) agentapi.FactItem {
	return agentapi.FactItem{
		Ts:     now.UTC(),
		Kind:   agentapi.SessionRestarted,
		Plugin: pluginID,
		DurS:   0,
		Data: agentapi.FactData{
			SessionId: &sessionID,
		},
	}
}

func (s *QueueSink) loadConfig() (config.Config, error) {
	if s.Config != nil {
		if err := s.Config.Validate(); err != nil {
			return config.Config{}, err
		}
		return *s.Config, nil
	}
	path := s.ConfigPath
	if path == "" {
		path = installer.DefaultPaths().ConfigPath
	}
	return config.LoadFile(path)
}
