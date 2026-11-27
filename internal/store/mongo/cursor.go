package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

func closeCursor(ctx context.Context, cur *mongo.Cursor) {
	if cur != nil {
		_ = cur.Close(ctx)
	}
}
