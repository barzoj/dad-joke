package ipc

import "sync/atomic"

// Stats holds process-wide IPC counters for tests and diagnostics.
type Stats struct {
	HandshakeOK     atomic.Uint64
	RefusedClient   atomic.Uint64 // PID/session mismatch
	VersionMismatch atomic.Uint64
	Oversize        atomic.Uint64
	BadFrame        atomic.Uint64
	UnknownType     atomic.Uint64
	DuplicateHello  atomic.Uint64
	HelloTimeout    atomic.Uint64
	Squatter        atomic.Uint64
	Disconnects     atomic.Uint64
	ReplacedConn    atomic.Uint64
}

// Snapshot is a plain copy of counters.
type Snapshot struct {
	HandshakeOK     uint64 `json:"handshake_ok"`
	RefusedClient   uint64 `json:"refused_client"`
	VersionMismatch uint64 `json:"version_mismatch"`
	Oversize        uint64 `json:"oversize"`
	BadFrame        uint64 `json:"bad_frame"`
	UnknownType     uint64 `json:"unknown_type"`
	DuplicateHello  uint64 `json:"duplicate_hello"`
	HelloTimeout    uint64 `json:"hello_timeout"`
	Squatter        uint64 `json:"squatter"`
	Disconnects     uint64 `json:"disconnects"`
	ReplacedConn    uint64 `json:"replaced_conn"`
}

// Snap returns a consistent snapshot.
func (s *Stats) Snap() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	return Snapshot{
		HandshakeOK:     s.HandshakeOK.Load(),
		RefusedClient:   s.RefusedClient.Load(),
		VersionMismatch: s.VersionMismatch.Load(),
		Oversize:        s.Oversize.Load(),
		BadFrame:        s.BadFrame.Load(),
		UnknownType:     s.UnknownType.Load(),
		DuplicateHello:  s.DuplicateHello.Load(),
		HelloTimeout:    s.HelloTimeout.Load(),
		Squatter:        s.Squatter.Load(),
		Disconnects:     s.Disconnects.Load(),
		ReplacedConn:    s.ReplacedConn.Load(),
	}
}
