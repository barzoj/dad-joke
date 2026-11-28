//go:build e2e && windows

package e2e_test

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func processOwnerName(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()

	tokUser, err := token.GetTokenUser()
	if err != nil || tokUser == nil || tokUser.User.Sid == nil {
		return "", fmt.Errorf("GetTokenUser: %v", err)
	}
	account, domain, _, err := tokUser.User.Sid.LookupAccount("")
	if err != nil {
		return tokUser.User.Sid.String(), nil
	}
	if domain != "" {
		return domain + `\` + account, nil
	}
	return account, nil
}

func processEnvHasDadJokePrefix(pid uint32) (bool, []string, error) {
	env, err := readProcessEnvironment(pid)
	if err != nil {
		return false, nil, err
	}
	var hits []string
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(strings.ToUpper(name), "DAD_JOKE_") {
			hits = append(hits, name)
		}
	}
	return len(hits) > 0, hits, nil
}

func readProcessEnvironment(pid uint32) ([]string, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ,
		false,
		pid,
	)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)

	var pbi windows.PROCESS_BASIC_INFORMATION
	var retLen uint32
	if err := windows.NtQueryInformationProcess(
		h,
		windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi),
		uint32(unsafe.Sizeof(pbi)),
		&retLen,
	); err != nil {
		return nil, err
	}
	if pbi.PebBaseAddress == nil {
		return nil, fmt.Errorf("nil PEB")
	}

	var peb windows.PEB
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)), unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return nil, err
	}
	if peb.ProcessParameters == nil {
		return nil, fmt.Errorf("nil ProcessParameters")
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(peb.ProcessParameters)), unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return nil, err
	}
	if params.Environment == nil {
		return nil, fmt.Errorf("nil Environment")
	}
	envSize := params.EnvironmentSize
	if envSize == 0 || envSize > 1<<20 {
		envSize = 64 << 10
	}

	buf := make([]byte, envSize)
	var read uintptr
	if err := windows.ReadProcessMemory(h, uintptr(params.Environment), &buf[0], uintptr(len(buf)), &read); err != nil && read == 0 {
		return nil, err
	}
	buf = buf[:read]
	if len(buf) < 2 {
		return nil, nil
	}
	u16 := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[0])), len(buf)/2)
	return splitEnvBlock(u16), nil
}

func readProcessMemory(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) error {
	var read uintptr
	err := windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &read)
	if err != nil {
		return err
	}
	if read != size {
		return fmt.Errorf("short read %d/%d", read, size)
	}
	return nil
}

func splitEnvBlock(u16 []uint16) []string {
	var out []string
	start := 0
	for i := 0; i < len(u16); i++ {
		if u16[i] != 0 {
			continue
		}
		if i == start {
			break
		}
		out = append(out, windows.UTF16ToString(u16[start:i]))
		start = i + 1
	}
	return out
}

func isSystemAccount(owner string) bool {
	o := strings.ToUpper(strings.TrimSpace(owner))
	return o == "SYSTEM" ||
		o == `NT AUTHORITY\SYSTEM` ||
		strings.HasSuffix(o, `\SYSTEM`)
}
