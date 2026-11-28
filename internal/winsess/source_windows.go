//go:build windows

package winsess

import (
	"context"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wtsCurrentServerHandle = 0
	wtsActive              = 0
	wtsConnected           = 1
	wtsConnectQuery        = 2
	wtsShadow              = 3
	wtsDisconnected        = 4
	wtsIdle                = 5
	wtsListen              = 6
	wtsReset               = 7
	wtsDown                = 8
	wtsInit                = 9

	wtsUserName      = 5
	wtsSessionInfoEx = 25
)

type wtsSessionInfoW struct {
	SessionID      uint32
	WinStationName *uint16
	State          uint32
}

var (
	modWtsapi32                 = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSEnumerateSessionsW   = modWtsapi32.NewProc("WTSEnumerateSessionsW")
	procWTSQuerySessionInformationW = modWtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory           = modWtsapi32.NewProc("WTSFreeMemory")
)

type platformSource struct{}

func (platformSource) Sessions(ctx context.Context) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var pInfo *wtsSessionInfoW
	var count uint32
	r1, _, err := procWTSEnumerateSessionsW.Call(
		wtsCurrentServerHandle,
		0,
		1,
		uintptr(unsafe.Pointer(&pInfo)),
		uintptr(unsafe.Pointer(&count)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("WTSEnumerateSessions: %w", err)
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(pInfo)))

	infos := unsafe.Slice(pInfo, count)
	out := make([]Session, 0, count)
	for _, info := range infos {
		if info.SessionID == 0 {
			continue // services session
		}
		state := mapWTSState(info.SessionID, info.State)
		if state == "" {
			continue
		}
		user, sid := queryUser(info.SessionID)
		if user == "" && sid == "" && (info.State == wtsListen || info.State == wtsInit) {
			continue
		}
		out = append(out, Session{
			ID:       info.SessionID,
			UserName: user,
			UserSID:  sid,
			State:    state,
		})
	}
	return out, nil
}

func (platformSource) Processes(ctx context.Context) ([]Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, fmt.Errorf("Process32First: %w", err)
	}
	var out []Process
	for {
		exe := windows.UTF16ToString(pe.ExeFile[:])
		owner, sessID := processOwnerAndSession(pe.ProcessID)
		out = append(out, Process{
			SessionID: sessID,
			PID:       pe.ProcessID,
			Exe:       filepath.Base(exe),
			OwnerSID:  owner,
		})
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return out, nil
}

func mapWTSState(sessionID, connectState uint32) State {
	switch connectState {
	case wtsDisconnected:
		return StateDisconnected
	case wtsListen, wtsInit, wtsDown, wtsReset:
		return ""
	case wtsActive, wtsConnected, wtsConnectQuery, wtsShadow, wtsIdle:
		if sessionLocked(sessionID) {
			return StateLocked
		}
		if connectState == wtsIdle {
			return StateLocked
		}
		return StateActive
	default:
		return ""
	}
}

func sessionLocked(sessionID uint32) bool {
	var buf *byte
	var nbytes uint32
	r1, _, _ := procWTSQuerySessionInformationW.Call(
		wtsCurrentServerHandle,
		uintptr(sessionID),
		uintptr(wtsSessionInfoEx),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&nbytes)),
	)
	if r1 == 0 || buf == nil || int(nbytes) < minInfoExBytes {
		return false
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	raw := unsafe.Slice(buf, nbytes)
	locked, ok := parseSessionLockedFromInfoEx(raw)
	return ok && locked
}

func queryUser(sessionID uint32) (userName, userSID string) {
	userName = querySessionString(sessionID, wtsUserName)
	// Resolve SID via LookupAccountName when we have a username.
	if userName == "" {
		return "", ""
	}
	sid, _, _, err := windows.LookupSID("", userName)
	if err != nil {
		return userName, ""
	}
	userSID = sid.String()
	return userName, userSID
}

func querySessionString(sessionID uint32, infoClass uint32) string {
	var buf *uint16
	var bytes uint32
	r1, _, _ := procWTSQuerySessionInformationW.Call(
		wtsCurrentServerHandle,
		uintptr(sessionID),
		uintptr(infoClass),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if r1 == 0 || buf == nil {
		return ""
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf)
}

func processOwnerAndSession(pid uint32) (ownerSID string, sessionID uint32) {
	_ = windows.ProcessIdToSessionId(pid, &sessionID)
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", sessionID
	}
	defer windows.CloseHandle(h)

	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", sessionID
	}
	defer token.Close()

	tokUser, err := token.GetTokenUser()
	if err != nil || tokUser == nil || tokUser.User.Sid == nil {
		return "", sessionID
	}
	return tokUser.User.Sid.String(), sessionID
}
