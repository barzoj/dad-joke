//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

func isWindowsServiceSession() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type scmHandler struct {
	agent  Agent
	events chan<- Event
}

func (h *scmHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptSessionChange | svc.AcceptPowerEvent

	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- h.agent.Run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepts}

	for {
		select {
		case err := <-done:
			changes <- svc.Status{State: svc.StopPending}
			changes <- svc.Status{State: svc.Stopped}
			if err != nil && !errors.Is(err, context.Canceled) {
				return true, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				// Bound stop wait so a stuck job pipe/CreateProcess cannot leave
				// the service in RUNNING forever (uninstall Access denied / SCM
				// timeout waiting for state 1 while last=4).
				select {
				case <-done:
				case <-time.After(45 * time.Second):
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			case svc.SessionChange:
				h.forwardSessionChange(req)
			case svc.PowerEvent:
				h.forwardPowerEvent(req)
			default:
				// Ignore unsupported controls.
			}
		}
	}
}

func (h *scmHandler) forwardSessionChange(req svc.ChangeRequest) {
	if req.EventData == 0 {
		return
	}
	// SCM passes EventData as a pointer-sized integer. unsafe.Add is the
	// supported single-expression conversion (go vet rejects unsafe.Pointer(uintptr)).
	notif := (*windows.WTSSESSION_NOTIFICATION)(unsafe.Add(unsafe.Pointer(nil), req.EventData))
	ev, ok := TranslateSessionChange(req.EventType, notif.SessionID)
	if !ok {
		return
	}
	EmitEvent(h.events, ev)
}

func (h *scmHandler) forwardPowerEvent(req svc.ChangeRequest) {
	ev, ok := TranslatePowerEvent(req.EventType)
	if !ok {
		return
	}
	EmitEvent(h.events, ev)
}

func runWindowsService(name string, agent Agent, events chan<- Event) error {
	if err := svc.Run(name, &scmHandler{agent: agent, events: events}); err != nil {
		return fmt.Errorf("service run: %w", err)
	}
	return nil
}
