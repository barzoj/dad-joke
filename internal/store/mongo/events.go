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
	for i := range events {
		ev := events[i]
		if ev.TenantID == "" || ev.MachineID == "" || ev.EventID == "" {
			return store.ErrInvalid
		}
		if ev.ID == "" {
			ev.ID = uuid.NewString()
		}
		events[i] = ev
		docs = append(docs, eventToDoc(&ev))
	}
	_, err := e.s.coll(collEvents).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil {
		// Duplicate key on (tenant, machine, event_id) is idempotent success.
		if mongo.IsDuplicateKeyError(err) {
			return nil
		}
		var bwe mongo.BulkWriteException
		if errors.As(err, &bwe) {
			for _, we := range bwe.WriteErrors {
				if !mongo.IsDuplicateKeyError(we) {
					return err
				}
			}
			return nil
		}
		return err
	}
	return nil
}

func (e eventStore) List(ctx context.Context, q store.EventQuery) ([]store.Event, error) {
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
	opts := options.Find().SetSort(bson.D{{Key: "ts", Value: 1}})
	if q.Limit > 0 {
		opts.SetLimit(int64(q.Limit))
	}
	cur, err := e.s.coll(collEvents).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.Event, 0)
	for cur.Next(ctx) {
		var d eventDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, *eventFromDoc(d))
	}
	return out, cur.Err()
}
