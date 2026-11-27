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
	"github.com/google/uuid"
)

// PostEvents ingests a compact telemetry batch (AgentAPI).
//
// Validation is all-or-nothing: every event is checked before any write.
// Duplicate event_id values (within the batch or against existing rows) are
// skipped idempotently. Hard limits: EventBatchMaxCount (400) and
// EventBatchMaxBytes enforced at the HTTP layer (413).
func (a *AgentAPI) PostEvents(ctx context.Context, req agentapi.PostEventsRequestObject) (agentapi.PostEventsResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return agentapi.PostEvents401JSONResponse{
			Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString(),
		}, nil
	}
	if req.Body == nil {
		return agentapi.PostEvents400JSONResponse{
			Code: "bad_request", Message: "missing body", RequestId: uuid.NewString(),
		}, nil
	}
	machineID := req.Body.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return agentapi.PostEvents400JSONResponse{
			Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString(),
		}, nil
	}

	items := req.Body.Events
	if len(items) > store.EventBatchMaxCount {
		return agentapi.PostEvents400JSONResponse{
			Code:      "bad_request",
			Message:   fmt.Sprintf("batch exceeds max of %d events", store.EventBatchMaxCount),
			RequestId: uuid.NewString(),
		}, nil
	}

	now := a.now()
	maxFuture := now.Add(store.EventMaxFutureSkew)
	prepared := make([]store.Event, 0, len(items))
	seen := make(map[string]struct{}, len(items))

	for i, item := range items {
		if item.EventId == "" {
			return agentapi.PostEvents400JSONResponse{
				Code:      "bad_request",
				Message:   fmt.Sprintf("events[%d]: event_id is required", i),
				RequestId: uuid.NewString(),
			}, nil
		}
		if item.Type == "" {
			return agentapi.PostEvents400JSONResponse{
				Code:      "bad_request",
				Message:   fmt.Sprintf("events[%d]: type is required", i),
				RequestId: uuid.NewString(),
			}, nil
		}
		ts := item.Ts.UTC()
		if ts.IsZero() {
			return agentapi.PostEvents400JSONResponse{
				Code:      "bad_request",
				Message:   fmt.Sprintf("events[%d]: ts is required", i),
				RequestId: uuid.NewString(),
			}, nil
		}
		if ts.After(maxFuture) {
			return agentapi.PostEvents400JSONResponse{
				Code:      "bad_request",
				Message:   fmt.Sprintf("events[%d]: ts is more than %s ahead of server clock", i, store.EventMaxFutureSkew),
				RequestId: uuid.NewString(),
			}, nil
		}

		payload, err := normalizeEventPayload(item.Payload)
		if err != nil {
			return agentapi.PostEvents400JSONResponse{
				Code:      "bad_request",
				Message:   fmt.Sprintf("events[%d]: payload must be JSON", i),
				RequestId: uuid.NewString(),
			}, nil
		}

		if _, ok := seen[item.EventId]; ok {
			// Within-batch duplicate: keep first, skip subsequent (idempotent).
			continue
		}
		seen[item.EventId] = struct{}{}

		prepared = append(prepared, store.Event{
			TenantID:  tenantID,
			MachineID: machineID,
			EventID:   item.EventId,
			Type:      item.Type,
			TS:        ts,
			Payload:   payload,
		})
	}

	if err := a.Store.Events().InsertBatch(ctx, prepared); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return agentapi.PostEvents400JSONResponse{
				Code: "bad_request", Message: "invalid event batch", RequestId: uuid.NewString(),
			}, nil
		}
		return agentapi.PostEvents500JSONResponse{
			Code: "internal_error", Message: "event ingest failed", RequestId: uuid.NewString(),
		}, nil
	}
	return agentapi.PostEvents204Response{}, nil
}

// ListMachineEvents returns tenant-scoped telemetry for a machine (ControlAPI).
func (c *ControlAPI) ListMachineEvents(ctx context.Context, req controlapi.ListMachineEventsRequestObject) (controlapi.ListMachineEventsResponseObject, error) {
	tenantID, err := authctx.TenantID(ctx)
	if err != nil {
		return controlapi.ListMachineEvents401JSONResponse{
			Code: "unauthorized", Message: "missing auth context", RequestId: uuid.NewString(),
		}, nil
	}
	machineID := req.MachineId.String()
	if err := config.ValidateMachineID(machineID); err != nil {
		return controlapi.ListMachineEvents400JSONResponse{
			Code: "bad_request", Message: "machine_id must be a UUIDv4", RequestId: uuid.NewString(),
		}, nil
	}

	limit := store.EventListDefaultLimit
	if req.Params.Limit != nil {
		lim := int(*req.Params.Limit)
		if lim < store.EventListMinLimit || lim > store.EventListMaxLimit {
			return controlapi.ListMachineEvents400JSONResponse{
				Code:      "bad_request",
				Message:   fmt.Sprintf("limit must be between %d and %d", store.EventListMinLimit, store.EventListMaxLimit),
				RequestId: uuid.NewString(),
			}, nil
		}
		limit = lim
	}

	var cursor string
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
		if cursor != "" {
			if _, _, err := store.DecodeEventCursor(cursor); err != nil {
				return controlapi.ListMachineEvents400JSONResponse{
					Code: "bad_request", Message: "invalid cursor", RequestId: uuid.NewString(),
				}, nil
			}
		}
	}

	q := store.EventQuery{
		TenantID:  tenantID,
		MachineID: machineID,
		Limit:     limit,
		Cursor:    cursor,
	}
	if req.Params.Type != nil {
		q.Type = *req.Params.Type
	}
	if req.Params.From != nil {
		t := req.Params.From.UTC()
		q.From = &t
	}
	if req.Params.To != nil {
		t := req.Params.To.UTC()
		q.To = &t
	}

	page, err := c.Store.Events().List(ctx, q)
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return controlapi.ListMachineEvents400JSONResponse{
				Code: "bad_request", Message: "invalid query", RequestId: uuid.NewString(),
			}, nil
		}
		return controlapi.ListMachineEvents500JSONResponse{
			Code: "internal_error", Message: "list events failed", RequestId: uuid.NewString(),
		}, nil
	}

	out := controlapi.EventListResponse{
		Events: make([]controlapi.MachineEvent, 0, len(page.Events)),
	}
	for i := range page.Events {
		ev := page.Events[i]
		var payload any
		if len(ev.Payload) > 0 {
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				payload = ev.Payload
			}
		}
		out.Events = append(out.Events, controlapi.MachineEvent{
			EventId: ev.EventID,
			Ts:      ev.TS,
			Type:    ev.Type,
			Payload: payload,
		})
	}
	if page.NextCursor != "" {
		c := page.NextCursor
		out.NextCursor = &c
	}
	return controlapi.ListMachineEvents200JSONResponse(out), nil
}

func normalizeEventPayload(v any) (json.RawMessage, error) {
	if v == nil {
		return json.RawMessage(`null`), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
