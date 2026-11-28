package service

import (
	"testing"
	"time"
)

func TestTranslateSessionChange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		eventType uint32
		sessionID uint32
		wantKind  EventKind
		ok        bool
	}{
		{wtsSessionLogon, 2, EventSessionLogon, true},
		{wtsSessionLogoff, 3, EventSessionLogoff, true},
		{wtsSessionLock, 4, EventSessionLock, true},
		{wtsSessionUnlock, 5, EventSessionUnlock, true},
		{wtsConsoleConnect, 1, EventConsoleConnect, true},
		{wtsConsoleDisconnect, 1, EventConsoleDisconnect, true},
		{wtsRemoteConnect, 7, EventRemoteConnect, true},
		{wtsRemoteDisconnect, 7, EventRemoteDisconnect, true},
		{0x9, 1, 0, false},  // remote control — ignored
		{0xa, 1, 0, false},  // create — ignored
		{0xff, 1, 0, false}, // unknown
	}
	for _, tc := range cases {
		ev, ok := TranslateSessionChange(tc.eventType, tc.sessionID)
		if ok != tc.ok {
			t.Fatalf("type=%d ok=%v want %v", tc.eventType, ok, tc.ok)
		}
		if !ok {
			continue
		}
		if ev.Kind != tc.wantKind || ev.SessionID != tc.sessionID {
			t.Fatalf("got %+v want kind=%v sid=%d", ev, tc.wantKind, tc.sessionID)
		}
	}
}

func TestTranslatePowerEvent(t *testing.T) {
	t.Parallel()
	ev, ok := TranslatePowerEvent(pbtAPMResumeAutomatic)
	if !ok || ev.Kind != EventPowerResume {
		t.Fatalf("got %+v ok=%v", ev, ok)
	}
	if _, ok := TranslatePowerEvent(0); ok {
		t.Fatal("unexpected ok for unknown power event")
	}
}

func TestEmitEventNonBlocking(t *testing.T) {
	t.Parallel()
	EmitEvent(nil, Event{Kind: EventSessionLogon}) // must not panic

	ch := make(chan Event, 1)
	EmitEvent(ch, Event{Kind: EventSessionLogon, SessionID: 1})
	EmitEvent(ch, Event{Kind: EventSessionLogoff, SessionID: 1}) // full — dropped
	select {
	case ev := <-ch:
		if ev.SessionID != 1 || ev.Kind != EventSessionLogon {
			t.Fatalf("%+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("expected buffered event")
	}
	select {
	case ev := <-ch:
		t.Fatalf("unexpected second event %+v", ev)
	default:
	}
}
