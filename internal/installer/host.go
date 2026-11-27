package installer

import (
	"time"

	"github.com/barzoj/dad-joke/internal/config"
)

// ServiceState is a portable representation of SCM service states.
type ServiceState string

// Service states used by WaitForState.
const (
	StateStopped ServiceState = "Stopped"
	StateRunning ServiceState = "Running"
)

// RecoveryAction describes an SCM failure recovery action.
type RecoveryAction struct {
	// Type is "restart", "reboot", "run_command", or "none".
	Type  string
	Delay time.Duration
}

// ServiceSpec is the desired Windows Service registration (stateless desired state).
type ServiceSpec struct {
	Name             string
	DisplayName      string
	Description      string
	BinaryPath       string
	Args             []string
	Account          string // "LocalSystem"
	StartType        string // "automatic"
	DelayedAutoStart bool
	Recovery         []RecoveryAction
	ResetPeriodSec   uint32
	// SecurityDescriptorSDDL restricts service control to SYSTEM + Administrators.
	SecurityDescriptorSDDL string
}

// FirewallRuleSpec is the desired outbound allow rule for the agent binary.
type FirewallRuleSpec struct {
	Name    string
	Program string
}

// Host isolates all Windows OS calls so Installer orchestration stays unit-testable.
type Host interface {
	IsElevated() (bool, error)
	ExecutablePath() (string, error)

	MkdirAll(path string) error
	CopyFile(src, dst string) error
	WriteFileIfAbsent(path string, data []byte) (created bool, err error)
	// ReadFile returns the contents of path.
	ReadFile(path string) ([]byte, error)
	// WriteFile creates or overwrites path with data.
	WriteFile(path string, data []byte) error
	FileExists(path string) (bool, error)
	RemoveAll(path string) error
	SameFile(a, b string) (bool, error)

	// ApplyDataACL locks path (dir or file) to SYSTEM + Administrators only.
	ApplyDataACL(path string) error
	// ApplyBinaryACL locks path to SYSTEM + Administrators full control only.
	ApplyBinaryACL(path string) error

	EnsureService(spec ServiceSpec) error
	StartService(name string) error
	StopService(name string) error
	DeleteService(name string) error
	ServiceExists(name string) (bool, error)
	WaitForState(name string, state ServiceState, timeout time.Duration) error

	EnsureFirewallRule(rule FirewallRuleSpec) error
	RemoveFirewallRule(name string) error

	Log(format string, args ...any)
}

// DefaultServiceSpec builds the desired DadJoke service registration.
func DefaultServiceSpec(binaryPath string) ServiceSpec {
	return ServiceSpec{
		Name:             ServiceName,
		DisplayName:      ServiceDisplayName,
		Description:      ServiceDescription,
		BinaryPath:       binaryPath,
		Args:             []string{"run"},
		Account:          "LocalSystem",
		StartType:        "automatic",
		DelayedAutoStart: true,
		Recovery: []RecoveryAction{
			{Type: "restart", Delay: 5 * time.Second},
			{Type: "restart", Delay: 5 * time.Second},
			{Type: "restart", Delay: 5 * time.Second},
		},
		ResetPeriodSec: 86400,
		// SYSTEM + Administrators only — Standard Users cannot stop/reconfigure.
		SecurityDescriptorSDDL: "D:(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)",
	}
}

// DefaultFirewallRule builds the outbound allow rule for the installed binary.
func DefaultFirewallRule(binaryPath string) FirewallRuleSpec {
	return FirewallRuleSpec{
		Name:    FirewallRuleName,
		Program: binaryPath,
	}
}

// DefaultConfigBytes returns JSON for a fresh install with new identity.
// Prefer Service.ensureConfig in the install path; this helper exists for tests.
func DefaultConfigBytes() ([]byte, error) {
	cfg := config.Default()
	id, err := config.NewMachineID()
	if err != nil {
		return nil, err
	}
	tok, err := config.NewToken()
	if err != nil {
		return nil, err
	}
	cfg.MachineID = id
	cfg.Token = tok
	return config.MarshalJSONBytes(cfg)
}
