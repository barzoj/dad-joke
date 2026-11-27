//go:build windows

package updater

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// OSService implements ServiceControl via the Windows SCM.
type OSService struct{}

// Stop implements ServiceControl.
func (OSService) Stop(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil
		}
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	_, err = s.Control(svc.Stop)
	return err
}

// Start implements ServiceControl.
func (OSService) Start(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Running {
		return nil
	}
	return s.Start()
}

// Wait implements ServiceControl.
func (OSService) Wait(name string, state ServiceState, timeout time.Duration) error {
	want, err := toSvcState(state)
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	deadline := time.Now().Add(timeout)
	for {
		status, err := s.Query()
		if err != nil {
			return err
		}
		if status.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s to become %s (last=%d)", name, state, status.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Query implements ServiceControl.
func (OSService) Query(name string) (ServiceState, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return "", err
	}
	defer s.Close()
	status, err := s.Query()
	if err != nil {
		return "", err
	}
	switch status.State {
	case svc.Stopped:
		return StateStopped, nil
	case svc.Running:
		return StateRunning, nil
	case svc.StartPending:
		return "StartPending", nil
	case svc.StopPending:
		return "StopPending", nil
	default:
		return ServiceState(fmt.Sprintf("state-%d", status.State)), nil
	}
}

func toSvcState(state ServiceState) (svc.State, error) {
	switch state {
	case StateRunning:
		return svc.Running, nil
	case StateStopped:
		return svc.Stopped, nil
	default:
		return 0, fmt.Errorf("unknown service state %q", state)
	}
}
