package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// OSStore implements FileStore with the local filesystem.
// ACL methods are no-ops on non-Windows; Windows builds override via applyACL.
type OSStore struct{}

// MkdirAll implements FileStore.
func (OSStore) MkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// WriteFile implements FileStore.
func (OSStore) WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadFile implements FileStore.
func (OSStore) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// RemoveAll implements FileStore.
func (OSStore) RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

// Rename implements FileStore.
func (OSStore) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// CopyFile implements FileStore.
func (OSStore) CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Exists implements FileStore.
func (OSStore) Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ApplyBinaryACL implements FileStore.
func (s OSStore) ApplyBinaryACL(path string) error { return applyOSACL(path) }

// ApplyDataACL implements FileStore.
func (s OSStore) ApplyDataACL(path string) error { return applyOSACL(path) }

// Ensure OSStore satisfies FileStore at compile time.
var _ FileStore = OSStore{}

// Guard against accidental empty path ACL calls.
func requirePath(path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	return nil
}
