//go:build windows

package updater

import (
	"github.com/barzoj/dad-joke/internal/acl"
)

func applyOSACL(path string) error {
	if err := requirePath(path); err != nil {
		return err
	}
	return acl.Apply(path)
}
