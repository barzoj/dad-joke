package jobs

// Windows CreateProcess flag bits used by JobObjectExecutor.
// Values match golang.org/x/sys/windows (WinBase.h).
const (
	flagCreateSuspended            = 0x00000004
	flagCreateNewProcessGroup      = 0x00000200 // intentionally unused: Job Object owns tree kill
	flagCreateUnicodeEnvironment   = 0x00000400
	flagCreateNoWindow             = 0x08000000
	flagExtendedStartupInfoPresent = 0x00080000
)

// processCreationFlagBits is the CreateProcess dwCreationFlags mask for job
// scripts. Kept as plain uint32 so unit tests can assert the Session 0-safe
// combination on every GOOS.
func processCreationFlagBits() uint32 {
	return flagCreateSuspended |
		flagCreateNoWindow |
		flagCreateUnicodeEnvironment |
		flagExtendedStartupInfoPresent
}
