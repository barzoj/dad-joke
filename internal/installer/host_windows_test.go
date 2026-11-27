//go:build windows

package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCopyFileReplacesLockedDestination(t *testing.T) {
	h := NewWindowsHost()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.exe")
	dst := filepath.Join(dir, "dst.exe")

	if err := os.WriteFile(src, []byte("new-binary-contents"), 0o755); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old-binary-contents"), 0o755); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	// Simulate a running Windows image mapping: share READ|DELETE so rename-aside
	// succeeds (Go's os.Open uses SHARE_READ|SHARE_WRITE without SHARE_DELETE).
	dstUTF16, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		t.Fatalf("utf16: %v", err)
	}
	handle, err := windows.CreateFile(
		dstUTF16,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile share-delete lock: %v", err)
	}
	defer windows.CloseHandle(handle)

	if err := h.CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile locked destination: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "new-binary-contents" {
		t.Fatalf("dst = %q, want new-binary-contents", got)
	}
}

func TestBinaryAndDataACLSDDLMatchArchitecture(t *testing.T) {
	if binaryACLSDDL != dataACLSDDL {
		t.Fatalf("binary ACL must match data ACL (SYSTEM+Admins only); binary=%q data=%q", binaryACLSDDL, dataACLSDDL)
	}
	if strings.Contains(binaryACLSDDL, ";;;BU)") || strings.Contains(binaryACLSDDL, ";BU)") {
		t.Fatalf("binary ACL must not grant Users (BU): %s", binaryACLSDDL)
	}
}
