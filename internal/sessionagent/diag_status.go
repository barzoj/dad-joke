//go:build e2ediag

package sessionagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/barzoj/dad-joke/internal/acl"
	"github.com/barzoj/dad-joke/internal/installer"
	"github.com/barzoj/dad-joke/internal/ipc"
)

// StatusFile is written under ProgramData for Windows E2E (e2ediag builds only).
const StatusFile = "ipc-status.json"
const ControlFile = "ipc-control.json"

type ipcStatusFile struct {
	Connected map[string]bool `json:"connected"`
	Stats     ipc.Snapshot    `json:"stats"`
}

func (s *Supervisor) runDiagLoop(ctx context.Context) {
	paths := installer.DefaultPaths()
	statusPath := filepath.Join(paths.DataDir, StatusFile)
	controlPath := filepath.Join(paths.DataDir, ControlFile)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.writeStatus(statusPath)
			s.pollControl(controlPath)
		}
	}
}

func (s *Supervisor) writeStatus(path string) {
	s.mu.Lock()
	connected := make(map[string]bool, len(s.children))
	for id, t := range s.children {
		if t != nil && t.ipcServer != nil && t.ipcServer.Connected() {
			connected[itoa(id)] = true
		}
	}
	stats := s.IPCStats
	s.mu.Unlock()
	body, err := json.Marshal(ipcStatusFile{
		Connected: connected,
		Stats:     stats.Snap(),
	})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
	_ = acl.Apply(path)
}

func (s *Supervisor) pollControl(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var ctrl struct {
		Action        string `json:"action"`
		DropSessionID uint32 `json:"drop_session_id"`
		ForceProtocol int    `json:"force_protocol"`
	}
	if json.Unmarshal(raw, &ctrl) != nil {
		return
	}
	if ctrl.Action == "drop" {
		s.mu.Lock()
		var ids []uint32
		if ctrl.DropSessionID != 0 {
			ids = []uint32{ctrl.DropSessionID}
		} else {
			for id := range s.children {
				ids = append(ids, id)
			}
		}
		s.mu.Unlock()
		for _, id := range ids {
			s.DropIPC(id)
		}
		// Clear action so we do not loop-drop.
		ctrl.Action = ""
		out, _ := json.Marshal(ctrl)
		_ = os.WriteFile(path, out, 0o600)
		_ = acl.Apply(path)
	}
}

func itoa(v uint32) string {
	return strconv.FormatUint(uint64(v), 10)
}
