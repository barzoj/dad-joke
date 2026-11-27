// Package winscm holds portable Windows SCM state planning helpers.
//
// Raw Win32 calls stay in platform files; this package decides how Start/Stop
// should behave for a given SERVICE_STATUS.dwCurrentState so callers can be
// idempotent under StartPending / StopPending races.
package winscm

import (
	"errors"
	"syscall"
)

// SERVICE_STATUS.dwCurrentState values (winnt.h / winsvc.h).
const (
	Stopped      uint32 = 1
	StartPending uint32 = 2
	StopPending  uint32 = 3
	Running      uint32 = 4
	ContinuePending uint32 = 5
	PausePending    uint32 = 6
	Paused          uint32 = 7
)

// Win32 errors relevant to idempotent Start/Stop.
const (
	ErrAlreadyRunning   syscall.Errno = 1056 // ERROR_SERVICE_ALREADY_RUNNING
	ErrCannotAcceptCtrl syscall.Errno = 1061 // ERROR_SERVICE_CANNOT_ACCEPT_CTRL
	ErrServiceNotActive syscall.Errno = 1062 // ERROR_SERVICE_NOT_ACTIVE
)

// StartPlan is how StartService should proceed after Query.
type StartPlan int

const (
	// StartNop: already Running or StartPending — do not call StartServiceW.
	StartNop StartPlan = iota
	// StartNow: service is Stopped (or equivalent) — call StartServiceW.
	StartNow
	// StartAfterStop: StopPending — wait for Stopped, then StartServiceW.
	StartAfterStop
)

// PlanStart decides StartService behavior from an SCM dwCurrentState value.
func PlanStart(state uint32) StartPlan {
	switch state {
	case Running, StartPending:
		return StartNop
	case StopPending:
		return StartAfterStop
	default:
		return StartNow
	}
}

// StopNoop reports whether Stop should return success without Control(Stop).
func StopNoop(state uint32) bool {
	return state == Stopped || state == StopPending
}

// IsAlreadyRunning reports ERROR_SERVICE_ALREADY_RUNNING (idempotent Start).
func IsAlreadyRunning(err error) bool {
	return errors.Is(err, ErrAlreadyRunning)
}

// IsBenignStopError reports SCM errors that mean the service is already
// stopping or not running — safe to treat as success before Wait(Stopped).
func IsBenignStopError(err error) bool {
	return errors.Is(err, ErrServiceNotActive) || errors.Is(err, ErrCannotAcceptCtrl)
}
