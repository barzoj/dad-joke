package winsess

import (
	"encoding/binary"
	"testing"
	"unsafe"
)

func TestWTSInfoExLayoutOffsets(t *testing.T) {
	t.Parallel()
	if unsafe.Sizeof(wtsInfoExW{}) < 20 {
		t.Fatalf("sizeof wtsInfoExW = %d, want >= 20", unsafe.Sizeof(wtsInfoExW{}))
	}
	if unsafe.Offsetof(wtsInfoExW{}.Level) != 0 {
		t.Fatalf("Level offset = %d", unsafe.Offsetof(wtsInfoExW{}.Level))
	}
	if unsafe.Offsetof(wtsInfoExW{}.Data) != 8 {
		t.Fatalf("Data offset = %d, want 8 (8-byte aligned after Level)", unsafe.Offsetof(wtsInfoExW{}.Data))
	}
	if sessionFlagsOffset != 16 {
		t.Fatalf("SessionFlags offset = %d, want 16", sessionFlagsOffset)
	}
	if minInfoExBytes != 20 {
		t.Fatalf("minInfoExBytes = %d, want 20", minInfoExBytes)
	}
}

func TestParseSessionLockedFromInfoEx(t *testing.T) {
	t.Parallel()
	craft := func(level uint32, flags uint32) []byte {
		buf := make([]byte, 20)
		binary.LittleEndian.PutUint32(buf[0:4], level)
		// padding 4:8
		binary.LittleEndian.PutUint32(buf[8:12], 1)   // SessionId
		binary.LittleEndian.PutUint32(buf[12:16], 0)  // SessionState = Active
		binary.LittleEndian.PutUint32(buf[16:20], flags)
		return buf
	}

	locked, ok := parseSessionLockedFromInfoEx(craft(1, wtsSessionStateLock))
	if !ok || !locked {
		t.Fatalf("lock: locked=%v ok=%v", locked, ok)
	}
	locked, ok = parseSessionLockedFromInfoEx(craft(1, wtsSessionStateUnlock))
	if !ok || locked {
		t.Fatalf("unlock: locked=%v ok=%v", locked, ok)
	}
	if _, ok := parseSessionLockedFromInfoEx(craft(2, wtsSessionStateLock)); ok {
		t.Fatal("level 2 should be rejected")
	}
	if _, ok := parseSessionLockedFromInfoEx(make([]byte, 16)); ok {
		t.Fatal("short buffer (16 < 20) must fail — old bug read SessionState as flags")
	}
	// Active session with unlock flags at correct offset must not look locked.
	buf := craft(1, wtsSessionStateUnlock)
	// Poison offset 12 (SessionState) with "lock" value so a wrong offset would
	// incorrectly report locked.
	binary.LittleEndian.PutUint32(buf[12:16], wtsSessionStateLock)
	locked, ok = parseSessionLockedFromInfoEx(buf)
	if !ok || locked {
		t.Fatalf("active+unlock with poisoned SessionState: locked=%v ok=%v", locked, ok)
	}
}
