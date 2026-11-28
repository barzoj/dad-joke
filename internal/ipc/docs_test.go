package ipc

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDocsProtocolVersionAgrees(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	doc := filepath.Join(root, "docs", "ARCHITECTURE.md")
	raw, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	want := "ProtocolVersion"
	if !strings.Contains(body, want) {
		t.Fatalf("ARCHITECTURE.md must mention %s", want)
	}
	ver := strconv.Itoa(ProtocolVersion)
	if !strings.Contains(body, "ProtocolVersion = "+ver) && !strings.Contains(body, "ProtocolVersion="+ver) &&
		!strings.Contains(body, "protocol version **"+ver+"**") && !strings.Contains(body, "protocol version "+ver) &&
		!strings.Contains(body, "`ProtocolVersion` **"+ver+"**") && !strings.Contains(body, "ProtocolVersion` is **"+ver) {
		// Accept several phrasings; require the digit near ProtocolVersion.
		idx := strings.Index(body, "ProtocolVersion")
		end := idx + 80
		if end > len(body) {
			end = len(body)
		}
		window := body[idx:end]
		if !strings.Contains(window, ver) {
			t.Fatalf("ARCHITECTURE.md ProtocolVersion window %q missing %s", window, ver)
		}
	}
	if !strings.Contains(body, "64 KiB") && !strings.Contains(body, "64KiB") && !strings.Contains(body, "65536") {
		t.Fatal("ARCHITECTURE.md must mention 64 KiB frame limit")
	}
	sec := filepath.Join(root, "docs", "SECURITY_MODEL.md")
	sraw, err := os.ReadFile(sec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sraw), "untrusted") {
		t.Fatal("SECURITY_MODEL.md must describe session agent as untrusted")
	}
}
