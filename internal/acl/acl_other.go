//go:build !windows

package acl

import (
	"fmt"
	"os"
)

// Apply is a no-op on non-Windows (path must still exist).
func Apply(path string) error {
	if path == "" {
		return fmt.Errorf("acl: empty path")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("acl: stat %s: %w", path, err)
	}
	return nil
}
