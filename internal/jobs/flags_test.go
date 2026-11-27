package jobs

import "testing"

func TestProcessCreationFlagBits_Session0Safe(t *testing.T) {
	t.Parallel()
	got := processCreationFlagBits()

	required := []struct {
		name string
		bit  uint32
	}{
		{"CREATE_SUSPENDED", flagCreateSuspended},
		{"CREATE_NO_WINDOW", flagCreateNoWindow},
		{"CREATE_UNICODE_ENVIRONMENT", flagCreateUnicodeEnvironment},
		{"EXTENDED_STARTUPINFO_PRESENT", flagExtendedStartupInfoPresent},
	}
	for _, tc := range required {
		if got&tc.bit == 0 {
			t.Fatalf("missing %s in %#x", tc.name, got)
		}
	}
	if got&flagCreateNewProcessGroup != 0 {
		t.Fatalf("CREATE_NEW_PROCESS_GROUP must not be set (Job Object owns tree kill); flags=%#x", got)
	}
}
