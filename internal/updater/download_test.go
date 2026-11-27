package updater

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type memStore struct {
	mu       sync.Mutex
	files    map[string][]byte
	dirs     map[string]bool
	aclErr   error
	aclCalls int
	copyErr  error
}

func newMemStore() *memStore {
	return &memStore{files: map[string][]byte{}, dirs: map[string]bool{}}
}

func (m *memStore) MkdirAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[path] = true
	return nil
}
func (m *memStore) WriteFile(path string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := append([]byte(nil), data...)
	m.files[path] = cp
	m.dirs[filepath.Dir(path)] = true
	return nil
}
func (m *memStore) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("not found: %s", path)
	}
	return append([]byte(nil), b...), nil
}
func (m *memStore) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.dirs, path)
	for k := range m.files {
		if k == path || strings.HasPrefix(k, path+string(filepath.Separator)) || strings.HasPrefix(k, path+"/") || strings.HasPrefix(k, path+"\\") {
			delete(m.files, k)
		}
	}
	return nil
}
func (m *memStore) Rename(oldpath, newpath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[oldpath]
	if !ok {
		return fmt.Errorf("rename missing %s", oldpath)
	}
	m.files[newpath] = b
	delete(m.files, oldpath)
	return nil
}
func (m *memStore) CopyFile(src, dst string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.copyErr != nil {
		return m.copyErr
	}
	b, ok := m.files[src]
	if !ok {
		return fmt.Errorf("copy missing %s", src)
	}
	m.files[dst] = append([]byte(nil), b...)
	return nil
}
func (m *memStore) Exists(path string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[path]
	return ok, nil
}
func (m *memStore) ApplyBinaryACL(string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aclCalls++
	return m.aclErr
}
func (m *memStore) ApplyDataACL(string) error { return nil }

type fakeClient struct {
	meta    ReleaseMeta
	metaErr error
	blobs   map[string][]byte
	dlErr   map[string]error
	calls   []string
}

func (f *fakeClient) FetchLatest(ctx context.Context, apiURL, agentVersion string) (ReleaseMeta, error) {
	f.calls = append(f.calls, "fetch:"+agentVersion)
	if f.metaErr != nil {
		return ReleaseMeta{}, f.metaErr
	}
	return f.meta, nil
}
func (f *fakeClient) Download(ctx context.Context, downloadURL, agentVersion string, maxBytes int64) ([]byte, error) {
	f.calls = append(f.calls, "dl:"+downloadURL)
	if err := f.dlErr[downloadURL]; err != nil {
		return nil, err
	}
	b, ok := f.blobs[downloadURL]
	if !ok {
		return nil, fmt.Errorf("missing blob %s", downloadURL)
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d byte limit", maxBytes)
	}
	return append([]byte(nil), b...), nil
}

func TestDownloadAndVerifyHappyPath(t *testing.T) {
	t.Parallel()
	exe := []byte("new-binary-bytes")
	sum := HashBytes(exe)
	sha := []byte(sum + "  " + AssetEXE + "\n")

	fc := &fakeClient{
		meta: ReleaseMeta{
			TagName: "v0.0.2",
			Assets: []AssetMeta{
				{Name: AssetEXE, BrowserDownloadURL: "http://127.0.0.1/exe"},
				{Name: AssetSHA256, BrowserDownloadURL: "http://127.0.0.1/sha"},
			},
		},
		blobs: map[string][]byte{
			"http://127.0.0.1/exe": exe,
			"http://127.0.0.1/sha": sha,
		},
		dlErr: map[string]error{},
	}
	store := newMemStore()
	dl := Downloader{Client: fc, Store: store, Log: NopLogger{}}
	res, err := dl.DownloadAndVerify(context.Background(), fc.meta, "0.0.1", `C:\ProgramData\DadJoke\updates\1`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "0.0.2" {
		t.Fatalf("version = %q", res.Version)
	}
	got, err := store.ReadFile(res.ExePath)
	if err != nil || string(got) != string(exe) {
		t.Fatalf("staged exe = %q err=%v", got, err)
	}
}

func TestDownloadAndVerifyMismatchCleansUp(t *testing.T) {
	t.Parallel()
	exe := []byte("new-binary-bytes")
	badSHA := []byte(strings.Repeat("0", 64) + "  " + AssetEXE + "\n")
	fc := &fakeClient{
		meta: ReleaseMeta{
			TagName: "v0.0.3",
			Assets: []AssetMeta{
				{Name: AssetEXE, BrowserDownloadURL: "http://127.0.0.1/exe"},
				{Name: AssetSHA256, BrowserDownloadURL: "http://127.0.0.1/sha"},
			},
		},
		blobs: map[string][]byte{
			"http://127.0.0.1/exe": exe,
			"http://127.0.0.1/sha": badSHA,
		},
		dlErr: map[string]error{},
	}
	store := newMemStore()
	staging := `C:\ProgramData\DadJoke\updates\bad`
	dl := Downloader{Client: fc, Store: store, Log: NopLogger{}}
	_, err := dl.DownloadAndVerify(context.Background(), fc.meta, "0.0.2", staging)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := store.Exists(filepath.Join(staging, AssetEXE)); ok {
		t.Fatal("staged exe should be cleaned up")
	}
}

func TestDownloadAndVerifyMissingAsset(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{
		meta: ReleaseMeta{TagName: "v0.0.2", Assets: nil},
	}
	store := newMemStore()
	dl := Downloader{Client: fc, Store: store}
	_, err := dl.DownloadAndVerify(context.Background(), fc.meta, "0.0.1", `/tmp/stage`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadAndVerifyCorruptSHA(t *testing.T) {
	t.Parallel()
	fc := &fakeClient{
		meta: ReleaseMeta{
			TagName: "v0.0.2",
			Assets: []AssetMeta{
				{Name: AssetEXE, BrowserDownloadURL: "http://127.0.0.1/exe"},
				{Name: AssetSHA256, BrowserDownloadURL: "http://127.0.0.1/sha"},
			},
		},
		blobs: map[string][]byte{
			"http://127.0.0.1/exe": []byte("x"),
			"http://127.0.0.1/sha": []byte("not-a-hash"),
		},
		dlErr: map[string]error{},
	}
	dl := Downloader{Client: fc, Store: newMemStore()}
	_, err := dl.DownloadAndVerify(context.Background(), fc.meta, "0.0.1", `/tmp/stage2`)
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
}
