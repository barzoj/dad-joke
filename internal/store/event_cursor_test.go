package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/barzoj/dad-joke/internal/store"
)

func TestEventCursor_RoundTripAndOrder(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 11, 27, 12, 0, 0, 123, time.UTC)
	cur := store.EncodeEventCursor(ts, "e-1")
	gotTS, gotID, err := store.DecodeEventCursor(cur)
	if err != nil {
		t.Fatal(err)
	}
	if !gotTS.Equal(ts) || gotID != "e-1" {
		t.Fatalf("got %v %q", gotTS, gotID)
	}

	if _, _, err := store.DecodeEventCursor(""); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if _, _, err := store.DecodeEventCursor("%%%"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad b64: %v", err)
	}
	if _, _, err := store.DecodeEventCursor("e30"); !errors.Is(err, store.ErrInvalid) { // "{}"
		t.Fatalf("empty fields: %v", err)
	}

	a := store.Event{TS: ts, EventID: "a"}
	b := store.Event{TS: ts, EventID: "b"}
	c := store.Event{TS: ts.Add(time.Second), EventID: "a"}
	if store.CompareEvents(a, b) >= 0 || store.CompareEvents(b, c) >= 0 {
		t.Fatal("CompareEvents order")
	}
	if !store.EventAfterCursor(b, ts, "a") || store.EventAfterCursor(a, ts, "a") {
		t.Fatal("EventAfterCursor")
	}
}
