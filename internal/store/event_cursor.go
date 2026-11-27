package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// eventCursorPayload is the opaque ControlAPI event list cursor.
type eventCursorPayload struct {
	TS      time.Time `json:"ts"`
	EventID string    `json:"event_id"`
}

// EncodeEventCursor builds an opaque cursor from (ts, event_id).
func EncodeEventCursor(ts time.Time, eventID string) string {
	raw, err := json.Marshal(eventCursorPayload{TS: ts.UTC(), EventID: eventID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeEventCursor parses an opaque event list cursor.
func DecodeEventCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", fmt.Errorf("%w: empty cursor", ErrInvalid)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: cursor encoding", ErrInvalid)
	}
	var p eventCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, "", fmt.Errorf("%w: cursor payload", ErrInvalid)
	}
	if p.EventID == "" || p.TS.IsZero() {
		return time.Time{}, "", fmt.Errorf("%w: cursor fields", ErrInvalid)
	}
	return p.TS.UTC(), p.EventID, nil
}

// EventAfterCursor reports whether ev is strictly after the cursor position
// in the deterministic (ts, event_id) order.
func EventAfterCursor(ev Event, cursorTS time.Time, cursorEventID string) bool {
	if ev.TS.After(cursorTS) {
		return true
	}
	if ev.TS.Equal(cursorTS) && ev.EventID > cursorEventID {
		return true
	}
	return false
}

// CompareEvents orders by ts ascending, then event_id ascending.
func CompareEvents(a, b Event) int {
	if a.TS.Before(b.TS) {
		return -1
	}
	if a.TS.After(b.TS) {
		return 1
	}
	switch {
	case a.EventID < b.EventID:
		return -1
	case a.EventID > b.EventID:
		return 1
	default:
		return 0
	}
}
