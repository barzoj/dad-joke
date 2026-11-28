package ipc

import (
	"fmt"
	"strconv"
)

// PipePrefix is the Windows named-pipe path prefix (without session id).
const PipePrefix = `\\.\pipe\DadJoke.session.`

// PipeName returns the per-session named pipe path.
func PipeName(sessionID uint32) string {
	return PipePrefix + strconv.FormatUint(uint64(sessionID), 10)
}

// ParsePipeSessionID extracts the session id from a pipe name produced by PipeName.
func ParsePipeSessionID(name string) (uint32, error) {
	const p = PipePrefix
	if len(name) <= len(p) || name[:len(p)] != p {
		return 0, fmt.Errorf("ipc: not a DadJoke session pipe: %q", name)
	}
	v, err := strconv.ParseUint(name[len(p):], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("ipc: bad session id in pipe name: %w", err)
	}
	return uint32(v), nil
}
