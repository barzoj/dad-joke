package mongo

import (
	"context"
	"errors"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type eventStore struct{ s *Store }

func (e eventStore) InsertBatch(ctx context.Context, events []store.Event) error {
	if len(events) == 0 {
		return nil
	}
	docs := make([]any, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for i := range events {
		ev := events[i]
		if ev.TenantID == "" || ev.MachineID == "" || ev.EventID == "" {
			return store.ErrInvalid
		}
		key := ev.TenantID + "|" + ev.MachineID + "|" + ev.EventID
		if _, ok := seen[key]; ok {
			continue // within-batch idempotent skip
		}
		seen[key] = struct{}{}
		if ev.ID == "" {
			ev.ID = uuid.NewString()
		}
		events[i] = ev
		docs = append(docs, eventToDoc(&ev))
	}
	if len(docs) == 0 {
		return nil
	}
	_, err := e.s.coll(collEvents).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil {
		// Only treat pure duplicate-key outcomes as idempotent success.
		// mongo.IsDuplicateKeyError(BulkWriteException) is true when *any*
		// write error is a duplicate — that would mask mixed failures.
		if isOnlyDuplicateKeyBulkError(err) {
			return nil
		}
		return err
	}
	return nil
}

// isOnlyDuplicateKeyBulkError reports whether err is an idempotent duplicate-key
// outcome suitable to ignore on InsertMany.
//
// For BulkWriteException: every WriteError must be a duplicate-key error, and
// WriteConcernError must be nil. An empty WriteErrors slice is not success
// (fail closed). For non-bulk errors, falls back to mongo.IsDuplicateKeyError.
func isOnlyDuplicateKeyBulkError(err error) bool {
	if err == nil {
		return false
	}
	var bwe mongo.BulkWriteException
	if errors.As(err, &bwe) {
		if bwe.WriteConcernError != nil || len(bwe.WriteErrors) == 0 {
			return false
		}
		for _, we := range bwe.WriteErrors {
			if !mongo.IsDuplicateKeyError(we) {
				return false
			}
		}
		return true
	}
	return mongo.IsDuplicateKeyError(err)
}

func countBulkDupErrors(err error) int {
	var bwe mongo.BulkWriteException
	if errors.As(err, &bwe) {
		return len(bwe.WriteErrors)
	}
	if mongo.IsDuplicateKeyError(err) {
		return 1
	}
	return 0
}

func (e eventStore) List(ctx context.Context, q store.EventQuery) (store.EventPage, error) {
	filter := bson.D{
		{Key: "tenant_id", Value: q.TenantID},
		{Key: "machine_id", Value: q.MachineID},
	}
	if q.Type != "" {
		filter = append(filter, bson.E{Key: "type", Value: q.Type})
	}
	if q.From != nil || q.To != nil {
		rangeDoc := bson.D{}
		if q.From != nil {
			rangeDoc = append(rangeDoc, bson.E{Key: "$gte", Value: q.From.UTC()})
		}
		if q.To != nil {
			rangeDoc = append(rangeDoc, bson.E{Key: "$lt", Value: q.To.UTC()})
		}
		filter = append(filter, bson.E{Key: "ts", Value: rangeDoc})
	}
	if q.Cursor != "" {
		cursorTS, cursorEventID, err := store.DecodeEventCursor(q.Cursor)
		if err != nil {
			return store.EventPage{}, err
		}
		// Strictly after (ts, event_id) in ascending order.
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "ts", Value: bson.D{{Key: "$gt", Value: cursorTS}}}},
			bson.D{
				{Key: "ts", Value: cursorTS},
				{Key: "event_id", Value: bson.D{{Key: "$gt", Value: cursorEventID}}},
			},
		}})
	}

	limit := q.Limit
	if limit <= 0 {
		limit = store.EventListDefaultLimit
	}
	// Fetch one extra to detect a following page.
	fetch := int64(limit) + 1
	opts := options.Find().
		SetSort(bson.D{{Key: "ts", Value: 1}, {Key: "event_id", Value: 1}}).
		SetLimit(fetch)

	cur, err := e.s.coll(collEvents).Find(ctx, filter, opts)
	if err != nil {
		return store.EventPage{}, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.Event, 0, limit)
	for cur.Next(ctx) {
		var d eventDoc
		if err := cur.Decode(&d); err != nil {
			return store.EventPage{}, err
		}
		out = append(out, *eventFromDoc(d))
	}
	if err := cur.Err(); err != nil {
		return store.EventPage{}, err
	}

	page := store.EventPage{Events: out}
	if len(out) > limit {
		last := out[limit-1]
		page.Events = out[:limit]
		page.NextCursor = store.EncodeEventCursor(last.TS, last.EventID)
	}
	return page, nil
}
