package server

import (
	"fmt"

	"github.com/barzoj/dad-joke/internal/config"
)

// validateBatchMachineID is shared by AgentAPI batch ingest handlers (events, facts).
func validateBatchMachineID(machineID string) error {
	if err := config.ValidateMachineID(machineID); err != nil {
		return fmt.Errorf("machine_id must be a UUIDv4")
	}
	return nil
}

// validateEventBatchCount preserves the historical events error wording.
func validateEventBatchCount(n, max int) error {
	if n > max {
		return fmt.Errorf("batch exceeds max of %d events", max)
	}
	return nil
}

// validateFactBatchCount rejects oversized facts batches (400).
func validateFactBatchCount(n, max int) error {
	if n > max {
		return fmt.Errorf("facts exceeds max of %d items", max)
	}
	return nil
}
