package mongo_test

import (
	"context"
	"testing"

	mongostore "github.com/barzoj/dad-joke/internal/store/mongo"
)

func TestOpen_RequiresURI(t *testing.T) {
	t.Parallel()
	_, err := mongostore.Open(context.Background(), mongostore.Config{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPing_NilStore(t *testing.T) {
	t.Parallel()
	var st *mongostore.Store
	if err := st.Ping(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("Close nil: %v", err)
	}
}
