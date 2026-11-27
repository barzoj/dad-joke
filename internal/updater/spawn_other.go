//go:build !windows

package updater

import (
	"context"
	"fmt"
	"os/exec"
)

// OSSpawner launches a detached helper (best-effort on non-Windows).
type OSSpawner struct{}

// StartDetached implements Spawner.
func (OSSpawner) StartDetached(ctx context.Context, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// #nosec G204 -- name is the installed helper path; args are fixed apply-update flags from TriggerApply
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached: %w", err)
	}
	// Detach from wait; process continues independently.
	go func() { _ = cmd.Wait() }()
	return nil
}
