package mongo

import (
	"context"
	"fmt"
	"math"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/barzoj/dad-joke/internal/usage"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type usageStore struct{ s *Store }

func (u usageStore) Apply(ctx context.Context, tenantID, machineID, streamID string, seq int64, deltas []store.UsageDelta) (int, error) {
	if tenantID == "" || machineID == "" || streamID == "" {
		return 0, store.ErrInvalid
	}
	if len(deltas) == 0 {
		return 0, nil
	}
	// Sum in memory per _id (caller may already have summed; re-sum is safe).
	summed := make(map[string]store.UsageDelta)
	for _, d := range deltas {
		if d.Date == "" || d.Bucket == "" || d.Seconds == 0 {
			continue
		}
		id := usage.DailyID(tenantID, machineID, d.Date, d.Bucket)
		cur := summed[id]
		cur.Date = d.Date
		cur.Bucket = d.Bucket
		cur.TimeZone = d.TimeZone
		cur.Seconds += d.Seconds
		summed[id] = cur
	}

	hwPath := "hw." + streamID
	updated := 0
	for id, d := range summed {
		filter := bson.D{
			{Key: "_id", Value: id},
			{Key: "$or", Value: bson.A{
				bson.D{{Key: hwPath, Value: bson.D{{Key: "$exists", Value: false}}}},
				bson.D{{Key: hwPath, Value: bson.D{{Key: "$lt", Value: seq}}}},
			}},
		}
		update := bson.D{
			{Key: "$inc", Value: bson.D{{Key: "seconds", Value: d.Seconds}}},
			{Key: "$set", Value: bson.D{
				{Key: hwPath, Value: seq},
				{Key: "time_zone", Value: d.TimeZone},
				{Key: "tenant_id", Value: tenantID},
				{Key: "machine_id", Value: machineID},
				{Key: "date", Value: d.Date},
				{Key: "bucket", Value: d.Bucket},
			}},
		}
		opts := options.UpdateOne().SetUpsert(true)
		res, err := u.s.coll(collUsageDaily).UpdateOne(ctx, filter, update, opts)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				// Concurrent upsert raced; the other writer already applied
				// (or holds a higher/equal hw). Treat as already applied.
				continue
			}
			return updated, err
		}
		if res.ModifiedCount > 0 || res.UpsertedCount > 0 {
			updated++
		}
	}
	return updated, nil
}

func (u usageStore) ListByMachine(ctx context.Context, tenantID, machineID, date string) ([]store.UsageDaily, error) {
	if tenantID == "" || machineID == "" || date == "" {
		return nil, store.ErrInvalid
	}
	cur, err := u.s.coll(collUsageDaily).Find(ctx,
		bson.D{
			{Key: "tenant_id", Value: tenantID},
			{Key: "machine_id", Value: machineID},
			{Key: "date", Value: date},
		},
		options.Find().SetSort(bson.D{{Key: "bucket", Value: 1}}),
	)
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.UsageDaily, 0)
	for cur.Next(ctx) {
		var d usageDailyDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, usageFromDoc(d))
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

const factsTTLIndexName = "ttl_facts_ts_server"

// ensureFactsTTL creates the facts TTL index or reconciles expireAfterSeconds
// via collMod when an existing index has a different value.
func (s *Store) ensureFactsTTL(ctx context.Context, expireSecs int32) error {
	cur, err := s.coll(collFacts).Indexes().List(ctx)
	if err != nil {
		return err
	}
	defer closeCursor(ctx, cur)

	found := false
	var currentExpire int32
	for cur.Next(ctx) {
		var idx bson.M
		if err := cur.Decode(&idx); err != nil {
			return err
		}
		name, _ := idx["name"].(string)
		if name != factsTTLIndexName {
			continue
		}
		found = true
		currentExpire = expireAfterSecondsFromBSON(idx["expireAfterSeconds"])
	}
	if err := cur.Err(); err != nil {
		return err
	}
	if !found {
		_, err := s.coll(collFacts).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{Key: "ts_server", Value: 1}},
			Options: options.Index().
				SetExpireAfterSeconds(expireSecs).
				SetName(factsTTLIndexName),
		})
		if err != nil {
			return fmt.Errorf("mongo: create facts TTL: %w", err)
		}
		return nil
	}
	if currentExpire == expireSecs {
		return nil
	}
	cmd := bson.D{
		{Key: "collMod", Value: collFacts},
		{Key: "index", Value: bson.D{
			{Key: "name", Value: factsTTLIndexName},
			{Key: "expireAfterSeconds", Value: expireSecs},
		}},
	}
	if err := s.db.RunCommand(ctx, cmd).Err(); err != nil {
		return fmt.Errorf("mongo: collMod facts TTL: %w", err)
	}
	return nil
}

// expireAfterSecondsFromBSON narrows BSON numeric TTL values to int32.
// Values outside int32 are clamped (TTL seconds for our retention are ≤ 365*86400).
func expireAfterSecondsFromBSON(v any) int32 {
	var n int64
	switch x := v.(type) {
	case int32:
		return x
	case int64:
		n = x
	case float64:
		if x > float64(math.MaxInt64) {
			return math.MaxInt32
		}
		if x < float64(math.MinInt64) {
			return 0
		}
		n = int64(x)
	case int:
		n = int64(x)
	default:
		return 0
	}
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < 0 {
		return 0
	}
	return int32(n) // #nosec G115 -- clamped to [0, MaxInt32] above
}
