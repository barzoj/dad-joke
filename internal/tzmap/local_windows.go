//go:build windows

package tzmap

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// DYNAMIC_TIME_ZONE_INFORMATION (subset) — TimeZoneKeyName is the Windows id.
type dynamicTimeZoneInformation struct {
	Bias                        int32
	StandardName                [32]uint16
	StandardDate                windows.Systemtime
	StandardBias                int32
	DaylightName                [32]uint16
	DaylightDate                windows.Systemtime
	DaylightBias                int32
	TimeZoneKeyName             [128]uint16
	DynamicDaylightTimeDisabled uint8
	_                           [3]byte // padding to align
}

var (
	modkernel32                         = windows.NewLazySystemDLL("kernel32.dll")
	procGetDynamicTimeZoneInformation   = modkernel32.NewProc("GetDynamicTimeZoneInformation")
)

func windowsZoneID() (string, bool) {
	var info dynamicTimeZoneInformation
	r1, _, _ := procGetDynamicTimeZoneInformation.Call(uintptr(unsafe.Pointer(&info)))
	// TIME_ZONE_ID_INVALID == 0xffffffff
	if r1 == 0xffffffff {
		return "", false
	}
	name := windows.UTF16ToString(info.TimeZoneKeyName[:])
	if name == "" {
		return "", false
	}
	return name, true
}

func localIANAName() string { return "" }
