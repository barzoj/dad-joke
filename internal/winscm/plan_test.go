package winscm

import (
	"fmt"
	"syscall"
	"testing"
)

func TestPlanStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state uint32
		want  StartPlan
	}{
		{Running, StartNop},
		{StartPending, StartNop},
		{StopPending, StartAfterStop},
		{Stopped, StartNow},
		{Paused, StartNow},
		{0, StartNow},
	}
	for _, tc := range cases {
		if got := PlanStart(tc.state); got != tc.want {
			t.Fatalf("PlanStart(%d) = %d, want %d", tc.state, got, tc.want)
		}
	}
}

func TestStopNoop(t *testing.T) {
	t.Parallel()
	if !StopNoop(Stopped) || !StopNoop(StopPending) {
		t.Fatal("Stopped/StopPending must be stop no-ops")
	}
	if StopNoop(Running) || StopNoop(StartPending) {
		t.Fatal("Running/StartPending must not be stop no-ops")
	}
}

func TestIsAlreadyRunning(t *testing.T) {
	t.Parallel()
	if !IsAlreadyRunning(ErrAlreadyRunning) {
		t.Fatal("expected ErrAlreadyRunning match")
	}
	if !IsAlreadyRunning(fmt.Errorf("wrap: %w", ErrAlreadyRunning)) {
		t.Fatal("expected wrapped ErrAlreadyRunning match")
	}
	if IsAlreadyRunning(ErrServiceNotActive) {
		t.Fatal("not-active must not match already-running")
	}
}

func TestIsBenignStopError(t *testing.T) {
	t.Parallel()
	if !IsBenignStopError(ErrServiceNotActive) || !IsBenignStopError(ErrCannotAcceptCtrl) {
		t.Fatal("expected benign stop errors")
	}
	if IsBenignStopError(ErrAlreadyRunning) {
		t.Fatal("already-running is not a stop error")
	}
	// 1055 is ERROR_SERVICE_DATABASE_LOCKED — must never be treated as benign.
	const errDatabaseLocked syscall.Errno = 1055
	if IsBenignStopError(errDatabaseLocked) {
		t.Fatal("ERROR_SERVICE_DATABASE_LOCKED (1055) must not be benign")
	}
	if IsAlreadyRunning(errDatabaseLocked) {
		t.Fatal("ERROR_SERVICE_DATABASE_LOCKED (1055) must not match already-running")
	}
}
