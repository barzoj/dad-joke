package installer

import "path/filepath"

// Well-known install and data locations (Windows paths; used as contract strings).
const (
	ServiceName        = "DadJoke"
	ServiceDisplayName = "Dad Joke Parental Control Agent"
	ServiceDescription = "Dad Joke Windows Service — parental-control agent under LocalSystem."
	FirewallRuleName   = "DadJoke"

	InstallDirName = "DadJoke"
	BinaryName     = "dad-joke.exe"
	ConfigFileName = "config.json"
	DataDirName    = "DadJoke"

	// Well-known SIDs used in ACL documentation and Windows implementations.
	SIDLocalSystem    = "S-1-5-18"
	SIDAdministrators = "S-1-5-32-544"
	SIDUsers          = "S-1-5-32-545"
)

// Paths holds absolute install/data locations. Defaults match production Windows layout.
type Paths struct {
	InstallDir string
	BinaryPath string
	DataDir    string
	ConfigPath string
}

// DefaultPaths returns the production Program Files / ProgramData layout.
func DefaultPaths() Paths {
	installDir := filepath.Join(`C:\Program Files`, InstallDirName)
	dataDir := filepath.Join(`C:\ProgramData`, DataDirName)
	return Paths{
		InstallDir: installDir,
		BinaryPath: filepath.Join(installDir, BinaryName),
		DataDir:    dataDir,
		ConfigPath: filepath.Join(dataDir, ConfigFileName),
	}
}
