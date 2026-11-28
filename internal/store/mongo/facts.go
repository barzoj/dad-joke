package mongo

import (
	"context"

	"github.com/barzoj/dad-joke/internal/store"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type factStore struct{ s *Store }

func (f factStore) InsertBatch(ctx context.Context, facts []store.Fact) (int, error) {
	if len(facts) == 0 {
		return 0, nil
	}
	docs := make([]any, 0, len(facts))
	for i := range facts {
		ft := facts[i]
		if ft.TenantID == "" || ft.MachineID == "" || ft.StreamID == "" {
			return 0, store.ErrInvalid
		}
		if ft.ID == "" {
			ft.ID = uuid.NewString()
		}
		facts[i] = ft
		docs = append(docs, factToDoc(&ft))
	}
	res, err := f.s.coll(collFacts).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil {
		if isOnlyDuplicateKeyBulkError(err) {
			// Count how many were actually inserted (total - dup failures).
			inserted := 0
			if res != nil {
				inserted = len(res.InsertedIDs)
			} else {
				// Driver may leave res nil; derive from write errors.
				inserted = len(docs) - countBulkDupErrors(err)
				if inserted < 0 {
					inserted = 0
				}
			}
			return inserted, nil
		}
		return 0, err
	}
	return len(res.InsertedIDs), nil
}
