//go:build e2ediag

package sessionagent

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/barzoj/dad-joke/internal/ipc"
)

// e2ediag build tag enables test-only hooks under %TEMP%\dadjoke-ipc-cmd\
// (user-writable; no ProgramData / admin required for the session agent):
//   - force_protocol  — integer protocol advertised in hello
//   - oversize        — send an oversized frame header after hello
// Supervisor still writes ProgramData\DadJoke\ipc-status.json / ipc-control.json
// for drop commands and counters (admin E2E process).

func e2eCmdDir() string {
	return filepath.Join(os.TempDir(), "dadjoke-ipc-cmd")
}

func forcedProtocol() int {
	if v := os.Getenv("DAD_JOKE_IPC_PROTOCOL"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	raw, err := os.ReadFile(filepath.Join(e2eCmdDir(), "force_protocol"))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func e2eAfterHello(c *ipc.Conn) error {
	marker := filepath.Join(e2eCmdDir(), "oversize")
	if _, err := os.Stat(marker); err != nil {
		return nil
	}
	_ = os.Remove(marker)
	// Send a frame header declaring MaxFrameBytes+1 without allocating that size.
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(ipc.MaxFrameBytes+1))
	_, err := c.Raw().Write(hdr[:])
	time.Sleep(50 * time.Millisecond)
	return err
}
