package service

// EventKind identifies an SCM notification forwarded to agents (T-21 contract).
// Values are stable for this process; string names are for logs/tests only.
type EventKind int

const (
	// EventSessionLogon is WTS_SESSION_LOGON.
	EventSessionLogon EventKind = iota + 1
	// EventSessionLogoff is WTS_SESSION_LOGOFF.
	EventSessionLogoff
	// EventSessionLock is WTS_SESSION_LOCK.
	EventSessionLock
	// EventSessionUnlock is WTS_SESSION_UNLOCK.
	EventSessionUnlock
	// EventConsoleConnect is WTS_CONSOLE_CONNECT.
	EventConsoleConnect
	// EventConsoleDisconnect is WTS_CONSOLE_DISCONNECT.
	EventConsoleDisconnect
	// EventRemoteConnect is WTS_REMOTE_CONNECT.
	EventRemoteConnect
	// EventRemoteDisconnect is WTS_REMOTE_DISCONNECT.
	EventRemoteDisconnect
	// EventPowerResume is PBT_APMRESUMEAUTOMATIC.
	EventPowerResume
)

// Event is one SCM session/power notification. SessionID is set for session
// kinds and zero for power resume. Contract version: session-events/v1.
type Event struct {
	Kind      EventKind
	SessionID uint32
}

// WTS / power event type constants (winuser / wtsapi32). Duplicated here so
// Translate* helpers are testable on non-Windows builders.
const (
	wtsConsoleConnect    = 0x1
	wtsConsoleDisconnect = 0x2
	wtsRemoteConnect     = 0x3
	wtsRemoteDisconnect  = 0x4
	wtsSessionLogon      = 0x5
	wtsSessionLogoff     = 0x6
	wtsSessionLock       = 0x7
	wtsSessionUnlock     = 0x8

	// pbtAPMResumeAutomatic is PBT_APMRESUMEAUTOMATIC (winuser.h).
	pbtAPMResumeAutomatic = 0x0012
)

// TranslateSessionChange maps a SERVICE_CONTROL_SESSIONCHANGE EventType and
// session id into an Event. Unknown types return ok=false (ignored).
func TranslateSessionChange(eventType, sessionID uint32) (Event, bool) {
	var kind EventKind
	switch eventType {
	case wtsSessionLogon:
		kind = EventSessionLogon
	case wtsSessionLogoff:
		kind = EventSessionLogoff
	case wtsSessionLock:
		kind = EventSessionLock
	case wtsSessionUnlock:
		kind = EventSessionUnlock
	case wtsConsoleConnect:
		kind = EventConsoleConnect
	case wtsConsoleDisconnect:
		kind = EventConsoleDisconnect
	case wtsRemoteConnect:
		kind = EventRemoteConnect
	case wtsRemoteDisconnect:
		kind = EventRemoteDisconnect
	default:
		return Event{}, false
	}
	return Event{Kind: kind, SessionID: sessionID}, true
}

// TranslatePowerEvent maps a SERVICE_CONTROL_POWEREVENT EventType into an Event.
func TranslatePowerEvent(eventType uint32) (Event, bool) {
	if eventType == pbtAPMResumeAutomatic {
		return Event{Kind: EventPowerResume}, true
	}
	return Event{}, false
}

// EmitEvent sends ev on ch without blocking. A nil or full channel drops the
// event; level-triggered reconcile still converges on the next notification.
func EmitEvent(ch chan<- Event, ev Event) {
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}
