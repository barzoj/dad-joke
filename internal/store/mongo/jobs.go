package mongo

import (
	"context"
	"errors"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type jobStore struct{ s *Store }

func (j jobStore) Create(ctx context.Context, job *store.Job) error {
	if job == nil || job.TenantID == "" || job.MachineID == "" {
		return store.ErrInvalid
	}
	cp := *job
	if cp.ID == "" {
		cp.ID = uuid.NewString()
	}
	if cp.Status == "" {
		cp.Status = store.JobPending
	}
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	_, err := j.s.coll(collJobs).InsertOne(ctx, jobToDoc(&cp))
	if err != nil {
		return mapWriteError(err)
	}
	*job = cp
	return nil
}

func (j jobStore) Get(ctx context.Context, tenantID, id string) (*store.Job, error) {
	var d jobDoc
	err := j.s.coll(collJobs).FindOne(ctx, bson.D{
		{Key: "_id", Value: id},
		{Key: "tenant_id", Value: tenantID},
	}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return jobFromDoc(d), nil
}

func (j jobStore) ListByMachine(ctx context.Context, tenantID, machineID string) ([]store.Job, error) {
	cur, err := j.s.coll(collJobs).Find(ctx, bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "machine_id", Value: machineID},
	}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]store.Job, 0)
	for cur.Next(ctx) {
		var d jobDoc
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, *jobFromDoc(d))
	}
	return out, cur.Err()
}

func (j jobStore) Claim(ctx context.Context, tenantID, machineID, id string, at time.Time) (*store.Job, error) {
	res := j.s.coll(collJobs).FindOneAndUpdate(ctx,
		bson.D{
			{Key: "_id", Value: id},
			{Key: "tenant_id", Value: tenantID},
			{Key: "machine_id", Value: machineID},
			{Key: "status", Value: string(store.JobPending)},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: string(store.JobRunning)},
			{Key: "claimed_at", Value: at.UTC()},
		}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)
	var d jobDoc
	if err := res.Decode(&d); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Distinguish missing vs already claimed.
			_, getErr := j.Get(ctx, tenantID, id)
			if errors.Is(getErr, store.ErrNotFound) {
				return nil, store.ErrNotFound
			}
			return nil, store.ErrConflict
		}
		return nil, err
	}
	return jobFromDoc(d), nil
}

func (j jobStore) Complete(ctx context.Context, tenantID, id string, result store.JobResult) error {
	res, err := j.s.coll(collJobs).UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: id},
			{Key: "tenant_id", Value: tenantID},
			{Key: "status", Value: string(store.JobRunning)},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: string(result.Status)},
			{Key: "exit_code", Value: result.ExitCode},
			{Key: "stdout", Value: result.Stdout},
			{Key: "stderr", Value: result.Stderr},
			{Key: "duration_ms", Value: result.DurationMS},
			{Key: "finished_at", Value: result.FinishedAt.UTC()},
		}}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		_, getErr := j.Get(ctx, tenantID, id)
		if errors.Is(getErr, store.ErrNotFound) {
			return store.ErrNotFound
		}
		return store.ErrConflict
	}
	return nil
}

func (j jobStore) Cancel(ctx context.Context, tenantID, id string, at time.Time) error {
	res, err := j.s.coll(collJobs).UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: id},
			{Key: "tenant_id", Value: tenantID},
			{Key: "status", Value: string(store.JobPending)},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: string(store.JobCanceled)},
			{Key: "finished_at", Value: at.UTC()},
		}}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		_, getErr := j.Get(ctx, tenantID, id)
		if errors.Is(getErr, store.ErrNotFound) {
			return store.ErrNotFound
		}
		return store.ErrConflict
	}
	return nil
}

func (j jobStore) ListPendingIDs(ctx context.Context, tenantID, machineID string, now time.Time) ([]string, error) {
	cur, err := j.s.coll(collJobs).Find(ctx, bson.D{
		{Key: "tenant_id", Value: tenantID},
		{Key: "machine_id", Value: machineID},
		{Key: "status", Value: string(store.JobPending)},
		{Key: "expires_at", Value: bson.D{{Key: "$gt", Value: now.UTC()}}},
	}, options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}).SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer closeCursor(ctx, cur)

	out := make([]string, 0)
	for cur.Next(ctx) {
		var d struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&d); err != nil {
			return nil, err
		}
		out = append(out, d.ID)
	}
	return out, cur.Err()
}
