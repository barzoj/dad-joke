package mongo

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestIsOnlyDuplicateKeyBulkError(t *testing.T) {
	t.Parallel()

	dup := mongo.BulkWriteError{WriteError: mongo.WriteError{Code: 11000, Message: "E11000 duplicate key"}}
	other := mongo.BulkWriteError{WriteError: mongo.WriteError{Code: 50, Message: "ExceededTimeLimit"}}

	t.Run("all_duplicate_bulk", func(t *testing.T) {
		t.Parallel()
		err := mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{dup, dup}}
		if !isOnlyDuplicateKeyBulkError(err) {
			t.Fatal("want true for all-duplicate BulkWriteException")
		}
		// Pointer / wrapped forms must also match via errors.As.
		if !isOnlyDuplicateKeyBulkError(&err) {
			t.Fatal("want true for *BulkWriteException")
		}
		if !isOnlyDuplicateKeyBulkError(errors.Join(errors.New("wrap"), err)) {
			t.Fatal("want true for wrapped BulkWriteException")
		}
	})

	t.Run("mixed_duplicate_and_other", func(t *testing.T) {
		t.Parallel()
		err := mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{dup, other}}
		if isOnlyDuplicateKeyBulkError(err) {
			t.Fatal("want false for mixed BulkWriteException")
		}
		// Regression: driver IsDuplicateKeyError is true when *any* write is a dup.
		if !mongo.IsDuplicateKeyError(err) {
			t.Fatal("precondition: driver IsDuplicateKeyError should be true for mixed dups")
		}
	})

	t.Run("duplicate_plus_write_concern", func(t *testing.T) {
		t.Parallel()
		err := mongo.BulkWriteException{
			WriteConcernError: &mongo.WriteConcernError{Code: 64, Message: "waiting for replication timed out"},
			WriteErrors:       []mongo.BulkWriteError{dup},
		}
		if isOnlyDuplicateKeyBulkError(err) {
			t.Fatal("want false when WriteConcernError is set")
		}
	})

	t.Run("non_duplicate", func(t *testing.T) {
		t.Parallel()
		if isOnlyDuplicateKeyBulkError(errors.New("boom")) {
			t.Fatal("want false for plain error")
		}
		err := mongo.BulkWriteException{WriteErrors: []mongo.BulkWriteError{other}}
		if isOnlyDuplicateKeyBulkError(err) {
			t.Fatal("want false for non-duplicate BulkWriteException")
		}
		if isOnlyDuplicateKeyBulkError(mongo.BulkWriteException{}) {
			t.Fatal("want false for empty BulkWriteException")
		}
		if isOnlyDuplicateKeyBulkError(nil) {
			t.Fatal("want false for nil")
		}
	})
}
