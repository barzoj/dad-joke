//go:build !windows

package updater

func applyOSACL(path string) error {
	if err := requirePath(path); err != nil {
		return err
	}
	return nil
}
