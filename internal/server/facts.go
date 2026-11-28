package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/gen/go/controlapi"
	"github.com/barzoj/dad-joke/internal/authctx"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/usage"
	"github.com/google/uuid"
)

// PostFacts accepts an append-only facts batch and updates usage aggregates.
//
// Validation is all-or-nothing before any write. Duplicate (stream_id, seq)
// replays return duplicate:true when nothing new was inserted and no usage
// document changed. A retry after a crash between insert and Apply returns
// duplicate:false and completes the aggregate.
func (a *AgentAPI) PostFacts(ctx context.Context, req agentapi.PostFactsRequestObject) (agentapi.PostFactsResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return agentapi.PostFacts401JSONResponse{
			Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString(),
		}, nil
	}
	if req.Body == nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: "missing body", RequestId: uuid.NewString(),
		}, nil
	}
	body := req.Body
	machineID := body.MachineId.String()
	if err := validateBatchMachineID(machineID); err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	streamID := body.StreamId.String()
	if err := config.ValidateMachineID(streamID); err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: "stream_id must be a UUIDv4", RequestId: uuid.NewString(),
		}, nil
	}
	if err := validatePostFactsRequest(body); err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	if err := validateFactBatchCount(len(body.Facts), store.FactBatchMaxCount); err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	retention := a.factsRetentionDays()
	now := a.now()
	if err := validateFactsBatchSemantics(body, now, retention); err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}

	// Empty batch: no-op success (duplicate:false); nothing written.
	if len(body.Facts) == 0 {
		return agentapi.PostFacts200JSONResponse{
			AcceptedSeq: body.Seq,
			Duplicate:   false,
		}, nil
	}

	sentAt := body.SentAt.UTC()
	facts := make([]store.Fact, 0, len(body.Facts))
	samples := make([]usage.SampleInput, 0, len(body.Facts))

	for i, item := range body.Facts {
		dataJSON, err := marshalFactData(item.Data)
		if err != nil {
			return agentapi.PostFacts400JSONResponse{
				Code: "bad_request", Message: fmt.Sprintf("facts[%d]: data must be JSON", i), RequestId: uuid.NewString(),
			}, nil
		}
		if item.Data.Title != nil {
			trunc, err := usage.TruncateRunes(*item.Data.Title, store.FactTitleMaxLen)
			if err != nil {
				return agentapi.PostFacts400JSONResponse{
					Code: "bad_request", Message: fmt.Sprintf("facts[%d]: title must be valid UTF-8", i), RequestId: uuid.NewString(),
				}, nil
			}
			if trunc != *item.Data.Title {
				item.Data.Title = &trunc
				dataJSON, err = marshalFactData(item.Data)
				if err != nil {
					return agentapi.PostFacts400JSONResponse{
						Code: "bad_request", Message: fmt.Sprintf("facts[%d]: data must be JSON", i), RequestId: uuid.NewString(),
					}, nil
				}
			}
		}
		ts := item.Ts.UTC()
		tsServer := usage.TSServer(ts, sentAt, now)
		userSID := ""
		if item.UserSid != nil {
			userSID = *item.UserSid
		}
		facts = append(facts, store.Fact{
			TenantID:  tenantID,
			MachineID: machineID,
			StreamID:  streamID,
			Seq:       body.Seq,
			Idx:       int32(i),
			TS:        ts,
			TSServer:  tsServer,
			Kind:      string(item.Kind),
			Plugin:    item.Plugin,
			UserSID:   userSID,
			DurS:      item.DurS,
			Data:      dataJSON,
			TimeZone:  body.TimeZone,
			SentAt:    sentAt,
		})

		sample := usage.SampleInput{
			Kind:     string(item.Kind),
			DurS:     item.DurS,
			TSServer: tsServer,
			TimeZone: body.TimeZone,
		}
		if item.Data.Exes != nil {
			sample.Exes = append([]string(nil), (*item.Data.Exes)...)
		}
		if item.Data.Exe != nil {
			sample.Exe = *item.Data.Exe
		}
		samples = append(samples, sample)
	}

	deltas, err := usage.BuildDeltas(samples)
	if err != nil {
		return agentapi.PostFacts400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	storeDeltas := make([]store.UsageDelta, len(deltas))
	for i, d := range deltas {
		storeDeltas[i] = store.UsageDelta{
			Date: d.Date, TimeZone: d.TimeZone, Bucket: d.Bucket, Seconds: d.Seconds,
		}
	}

	inserted, err := a.Store.Facts().InsertBatch(ctx, facts)
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return agentapi.PostFacts400JSONResponse{
				Code: "bad_request", Message: "invalid fact batch", RequestId: uuid.NewString(),
			}, nil
		}
		return agentapi.PostFacts503JSONResponse{
			Code: "unavailable", Message: "fact ingest failed", RequestId: uuid.NewString(),
		}, nil
	}

	updated, err := a.Store.Usage().Apply(ctx, tenantID, machineID, streamID, body.Seq, storeDeltas)
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return agentapi.PostFacts400JSONResponse{
				Code: "bad_request", Message: "invalid usage delta", RequestId: uuid.NewString(),
			}, nil
		}
		return agentapi.PostFacts503JSONResponse{
			Code: "unavailable", Message: "usage aggregate failed", RequestId: uuid.NewString(),
		}, nil
	}

	duplicate := inserted == 0 && updated == 0
	return agentapi.PostFacts200JSONResponse{
		AcceptedSeq: body.Seq,
		Duplicate:   duplicate,
	}, nil
}

// GetMachineUsage returns per-app usage for one machine-local calendar day.
func (c *ControlAPI) GetMachineUsage(ctx context.Context, req controlapi.GetMachineUsageRequestObject) (controlapi.GetMachineUsageResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.GetMachineUsage401JSONResponse{
			Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString(),
		}, nil
	}
	if err := validateUsageDate(req.Params.Date); err != nil {
		return controlapi.GetMachineUsage400JSONResponse{
			Code: "bad_request", Message: err.Error(), RequestId: uuid.NewString(),
		}, nil
	}
	machineID := req.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return controlapi.GetMachineUsage400JSONResponse{
			Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString(),
		}, nil
	}

	mach, err := c.Store.Machines().Get(ctx, tenantID, machineID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return controlapi.GetMachineUsage404JSONResponse{
				Code: "not_found", Message: "machine not found", RequestId: uuid.NewString(),
			}, nil
		}
		return controlapi.GetMachineUsage503JSONResponse{
			Code: "unavailable", Message: "machine lookup failed", RequestId: uuid.NewString(),
		}, nil
	}

	date := req.Params.Date.Time.UTC().Format("2006-01-02")
	rows, err := c.Store.Usage().ListByMachine(ctx, tenantID, machineID, date)
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return controlapi.GetMachineUsage400JSONResponse{
				Code: "bad_request", Message: "invalid query", RequestId: uuid.NewString(),
			}, nil
		}
		return controlapi.GetMachineUsage503JSONResponse{
			Code: "unavailable", Message: "list usage failed", RequestId: uuid.NewString(),
		}, nil
	}

	buckets := make([]struct {
		Bucket  string
		Seconds int64
	}, 0, len(rows))
	tz := mach.TimeZone
	for _, r := range rows {
		buckets = append(buckets, struct {
			Bucket  string
			Seconds int64
		}{Bucket: r.Bucket, Seconds: r.Seconds})
		if r.TimeZone != "" {
			tz = r.TimeZone // latest / any day's stored zone
		}
	}
	apps := usage.MergeApps(buckets)
	outApps := make([]controlapi.AppUsage, 0, len(apps))
	for _, a := range apps {
		outApps = append(outApps, controlapi.AppUsage{
			Exe: a.Exe, Seconds: a.Seconds, FgSeconds: a.FgSeconds,
		})
	}
	return controlapi.GetMachineUsage200JSONResponse{
		Date:     req.Params.Date,
		TimeZone: tz,
		Apps:     outApps,
	}, nil
}

func marshalFactData(data agentapi.FactData) (json.RawMessage, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
