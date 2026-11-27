//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows/svc"
)

func isWindowsServiceSession() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type scmHandler struct {
	agent Agent
}

func (h *scmHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown

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
				<-done
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			default:
				// Ignore unsupported controls.
			}
		}
	}
}

func runWindowsService(name string, agent Agent) error {
	if err := svc.Run(name, &scmHandler{agent: agent}); err != nil {
		return fmt.Errorf("service run: %w", err)
	}
	return nil
}
