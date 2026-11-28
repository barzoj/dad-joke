package factsq

import (
	"io/fs"
	"os"
	"path/filepath"
)

// FS is the filesystem seam used by Queue. Tests inject ENOSPC / EACCES.
type FS interface {
	MkdirAll(path string, perm os.FileMode) error
	CreateTemp(dir, pattern string) (*os.File, error)
	Rename(oldpath, newpath string) error
	Remove(path string) error
	ReadFile(path string) ([]byte, error)
	ReadDir(name string) ([]os.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
}

// OSFS is the production filesystem backed by the os package.
type OSFS struct{}

// MkdirAll implements FS.
func (OSFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }

// CreateTemp implements FS.
func (OSFS) CreateTemp(dir, pattern string) (*os.File, error) { return os.CreateTemp(dir, pattern) }

// Rename implements FS.
func (OSFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove implements FS.
func (OSFS) Remove(path string) error { return os.Remove(path) }

// ReadFile implements FS.
func (OSFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) } // #nosec G304

// ReadDir implements FS.
func (OSFS) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }

// Stat implements FS.
func (OSFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

func (q *Queue) fs() FS {
	if q.FS != nil {
		return q.FS
	}
	return OSFS{}
}

func (q *Queue) writeAtomic(path string, data []byte) error {
	fsys := q.fs()
	dir := filepath.Dir(path)
	if err := fsys.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := fsys.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = fsys.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fsys.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	if q.ApplyACL != nil {
		_ = q.ApplyACL(path)
	}
	return nil
}
