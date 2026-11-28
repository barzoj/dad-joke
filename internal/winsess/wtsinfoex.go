package winsess

import (
	"encoding/binary"
	"unsafe"
)

// WTSINFOEX / WTSINFOEX_LEVEL1 layout (Windows). Level is followed by 4 bytes of
// padding so Data is 8-byte aligned; SessionFlags sits at offset 16.
//
//	typedef struct _WTSINFOEXW {
//	  DWORD Level;
//	  union { WTSINFOEX_LEVEL1_W WTSInfoExLevel1; } Data;
//	} WTSINFOEXW;
type wtsInfoExW struct {
	Level uint32
	_     uint32 // padding to align Data on 8 bytes
	Data  wtsInfoExLevel1W
}

type wtsInfoExLevel1W struct {
	SessionId    uint32
	SessionState uint32
	SessionFlags int32
}

const (
	wtsSessionStateLock   = 0x00000000
	wtsSessionStateUnlock = 0x00000001
)

// sessionFlagsOffset is the byte offset of Data.SessionFlags within wtsInfoExW.
// Compose outer Data offset + inner SessionFlags offset: some Go toolchains
// return a nested-only Offsetof for `wtsInfoExW{}.Data.SessionFlags`.
var sessionFlagsOffset = int(unsafe.Offsetof(wtsInfoExW{}.Data) + unsafe.Offsetof(wtsInfoExLevel1W{}.SessionFlags))

// minInfoExBytes is the minimum buffer size that includes SessionFlags.
var minInfoExBytes = sessionFlagsOffset + int(unsafe.Sizeof(int32(0)))

// parseSessionLockedFromInfoEx inspects a WTSINFOEX buffer and reports whether
// SessionFlags indicates a locked session. ok is false when the buffer is too
// short or Level != 1.
func parseSessionLockedFromInfoEx(raw []byte) (locked bool, ok bool) {
	if len(raw) < minInfoExBytes {
		return false, false
	}
	level := binary.LittleEndian.Uint32(raw[0:4])
	if level != 1 {
		return false, false
	}
	flags := binary.LittleEndian.Uint32(raw[sessionFlagsOffset : sessionFlagsOffset+4])
	return flags == wtsSessionStateLock, true
}
