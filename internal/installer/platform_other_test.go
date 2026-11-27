//go:build !windows

package installer

import (
	"context"
	"errors"
	"testing"
)

func TestNewReturnsUnsupportedOffWindows(t *testing.T) {
	t.Parallel()
	inst := New()
	if _, err := inst.Install(context.Background(), InstallOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Install = %v, want ErrUnsupported", err)
	}
	if err := inst.Uninstall(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Uninstall = %v, want ErrUnsupported", err)
	}
}
