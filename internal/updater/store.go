package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Directory and file modes for staging. On Windows the authoritative control is
// the SYSTEM+Administrators DACL applied via Apply*ACL; Unix modes stay tight
// for local tests and non-Windows builds. CreateProcess on Windows does not
// require an execute bit when given an absolute path.
const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// OSStore implements FileStore with the local filesystem.
// ACL methods are no-ops on non-Windows; Windows builds override via applyACL.
type OSStore struct{}

// MkdirAll implements FileStore.
func (OSStore) MkdirAll(path string) error {
	path, err := cleanStorePath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(path, dirPerm)
}

// WriteFile implements FileStore.
func (OSStore) WriteFile(path string, data []byte) error {
	path, err := cleanStorePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadFile implements FileStore.
func (OSStore) ReadFile(path string) ([]byte, error) {
	path, err := cleanStorePath(path)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is filepath.Clean'd; callers pass allowlisted staging/install paths
	return os.ReadFile(path)
}

// RemoveAll implements FileStore.
func (OSStore) RemoveAll(path string) error {
	path, err := cleanStorePath(path)
	if err != nil {
		return err
	}
	err = os.RemoveAll(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

// Rename implements FileStore.
func (OSStore) Rename(oldpath, newpath string) error {
	oldpath, err := cleanStorePath(oldpath)
	if err != nil {
		return err
	}
	newpath, err = cleanStorePath(newpath)
	if err != nil {
		return err
	}
	return os.Rename(oldpath, newpath)
}

// CopyFile implements FileStore.
func (OSStore) CopyFile(src, dst string) error {
	src, err := cleanStorePath(src)
	if err != nil {
		return err
	}
	dst, err = cleanStorePath(dst)
	if err != nil {
		return err
	}
	// #nosec G304 -- src is filepath.Clean'd; apply path allowlist validates callers
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	// #nosec G304 -- tmp is derived from filepath.Clean'd dst under the apply allowlist
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, filePerm)
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
	path, err := cleanStorePath(path)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
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

func cleanStorePath(path string) (string, error) {
	if err := requirePath(path); err != nil {
		return "", err
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("empty path")
	}
	return cleaned, nil
}

// Guard against accidental empty path ACL calls.
func requirePath(path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	return nil
}
