//go:build e2ediag

package sessionagent

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/ipc"
)

// e2ediag build tag enables test-only hooks:
//   - DAD_JOKE_IPC_PROTOCOL / ipc-control.json force_protocol for version mismatch
//   - %TEMP%\dadjoke-ipc-cmd\oversize triggers an oversized frame after hello
//   - Supervisor writes ProgramData\DadJoke\ipc-status.json (see diag_status.go)

func forcedProtocol() int {
	if v := os.Getenv("DAD_JOKE_IPC_PROTOCOL"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	path := filepath.Join(installer.DefaultPaths().DataDir, "ipc-control.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var ctrl struct {
		ForceProtocol int `json:"force_protocol"`
	}
	if json.Unmarshal(raw, &ctrl) != nil {
		return 0
	}
	if ctrl.ForceProtocol > 0 {
		return ctrl.ForceProtocol
	}
	return 0
}

func e2eAfterHello(c *ipc.Conn) error {
	dir := filepath.Join(os.TempDir(), "dadjoke-ipc-cmd")
	marker := filepath.Join(dir, "oversize")
	if _, err := os.Stat(marker); err != nil {
		return nil
	}
	_ = os.Remove(marker)
	// Send a frame header declaring MaxFrameBytes+1 without allocating that size.
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(ipc.MaxFrameBytes+1))
	raw := c.Raw()
	_, err := raw.Write(hdr[:])
	// Give the server a moment to observe the header before we continue.
	time.Sleep(50 * time.Millisecond)
	return err
}
