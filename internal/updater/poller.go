package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/barzoj/dad-joke/internal/buildinfo"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/installer"
)

// Poller is the service Agent that polls GitHub Releases and triggers updates.
type Poller struct {
	Version    string
	ConfigPath string
	DataDir    string
	BinaryPath string
	Service    string

	Client  ReleaseClient
	Store   FileStore
	Spawner Spawner
	Clock   Clock
	Log     Logger

	// Optional: override loaded config (tests).
	Config *config.Config

	// HelperExe defaults to os.Executable().
	HelperExe string

	// UpdateInFlightTTL overrides DefaultUpdateInFlightTTL (tests).
	UpdateInFlightTTL time.Duration

	// stagingID is used only for staging dir names in tests.
	stagingID func() string

	// updateInFlight prevents overlapping apply-update helpers.
	updateInFlight atomic.Bool
	// updateInFlightSince is unix-nano when the flag was set; 0 when clear.
	updateInFlightSince atomic.Int64
}

// NewPoller returns a production Poller wired to default paths and HTTP client.
func NewPoller() *Poller {
	paths := installer.DefaultPaths()
	return &Poller{
		Version:    buildinfo.Version,
		ConfigPath: paths.ConfigPath,
		DataDir:    paths.DataDir,
		BinaryPath: paths.BinaryPath,
		Service:    installer.ServiceName,
		Client:     NewHTTPReleaseClient(),
		Store:      OSStore{},
		Spawner:    OSSpawner{},
		Clock:      RealClock{},
		Log:        WriterLogger{W: os.Stderr},
	}
}

// Run implements service.Agent: poll until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	if p.Log == nil {
		p.Log = NopLogger{}
	}
	if p.Clock == nil {
		p.Clock = RealClock{}
	}
	if p.Client == nil {
		p.Client = NewHTTPReleaseClient()
	}
	if p.Store == nil {
		p.Store = OSStore{}
	}
	if p.Spawner == nil {
		p.Spawner = OSSpawner{}
	}
	if p.Version == "" {
		p.Version = buildinfo.Version
	}
	if p.Service == "" {
		p.Service = installer.ServiceName
	}
	if p.DataDir == "" {
		p.DataDir = installer.DefaultPaths().DataDir
	}
	if p.BinaryPath == "" {
		p.BinaryPath = installer.DefaultPaths().BinaryPath
	}
	if p.ConfigPath == "" {
		p.ConfigPath = installer.DefaultPaths().ConfigPath
	}
	if p.stagingID == nil {
		p.stagingID = func() string {
			return fmt.Sprintf("%d", time.Now().UnixNano())
		}
	}

	cfg, err := p.loadConfig()
	if err != nil {
		return fmt.Errorf("service config: %w", err)
	}
	interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	// Log identity safely: machine_id is OK; never log raw token (Config.String redacts).
	p.Log.Log("updater: poll loop starting (url=%s interval=%s version=%s machine_id=%s)",
		cfg.ReleaseAPIURL, interval, NormalizeVersion(p.Version), cfg.MachineID)

	// First check promptly, then on interval.
	p.safeCheck(ctx, cfg)

	for {
		timer := p.Clock.After(interval)
		select {
		case <-ctx.Done():
			p.Log.Log("updater: poll loop stopping")
			return ctx.Err()
		case <-timer:
			// Reload config each tick so tests can adjust interval/url without reinstall.
			cfg, err = p.loadConfig()
			if err != nil {
				p.Log.Log("updater: reload config failed (continuing with previous): %v", err)
			} else {
				interval = time.Duration(cfg.PollIntervalSeconds) * time.Second
			}
			p.safeCheck(ctx, cfg)
		}
	}
}

// loadConfig loads and validates config.json. Missing or empty machine_id / token
// fails loudly — the service must not run without persistent identity.
func (p *Poller) loadConfig() (config.Config, error) {
	if p.Config != nil {
		if err := p.Config.Validate(); err != nil {
			return config.Config{}, err
		}
		return *p.Config, nil
	}
	cfg, err := config.LoadFile(p.ConfigPath)
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func (p *Poller) safeCheck(ctx context.Context, cfg config.Config) {
	if err := ctx.Err(); err != nil {
		return
	}
	if err := p.checkOnce(ctx, cfg); err != nil {
		p.Log.Log("updater: check failed (continuing): %v", err)
	}
}

func (p *Poller) inFlightTTL() time.Duration {
	if p.UpdateInFlightTTL > 0 {
		return p.UpdateInFlightTTL
	}
	return DefaultUpdateInFlightTTL
}

func (p *Poller) clockOrReal() Clock {
	if p.Clock != nil {
		return p.Clock
	}
	return RealClock{}
}

func (p *Poller) clearInFlight() {
	p.updateInFlight.Store(false)
	p.updateInFlightSince.Store(0)
}

// tryBeginUpdate acquires the in-flight lock, clearing a sticky lock after TTL.
func (p *Poller) tryBeginUpdate() bool {
	clock := p.clockOrReal()
	if p.updateInFlight.CompareAndSwap(false, true) {
		p.updateInFlightSince.Store(clock.Now().UnixNano())
		return true
	}
	since := p.updateInFlightSince.Load()
	if since == 0 {
		// Flag set without timestamp — treat as expired so we can recover.
		p.Log.Log("updater: update-in-flight missing timestamp; clearing sticky flag")
		p.clearInFlight()
		if p.updateInFlight.CompareAndSwap(false, true) {
			p.updateInFlightSince.Store(clock.Now().UnixNano())
			return true
		}
		return false
	}
	age := clock.Now().Sub(time.Unix(0, since))
	if age < p.inFlightTTL() {
		p.Log.Log("updater: update already in flight; skipping")
		return false
	}
	p.Log.Log("updater: update-in-flight TTL expired (%s); clearing sticky flag for retry", age)
	p.clearInFlight()
	if p.updateInFlight.CompareAndSwap(false, true) {
		p.updateInFlightSince.Store(clock.Now().UnixNano())
		return true
	}
	return false
}

// checkOnce performs one poll → maybe download → trigger apply. Errors are
// returned to the caller for logging; they must never crash the service.
func (p *Poller) checkOnce(ctx context.Context, cfg config.Config) error {
	agentVer := NormalizeVersion(p.Version)
	meta, err := p.Client.FetchLatest(ctx, cfg.ReleaseAPIURL, agentVer)
	if err != nil {
		return err
	}

	newer, err := IsNewer(meta.TagName, agentVer)
	if err != nil {
		return fmt.Errorf("version compare: %w", err)
	}
	if !newer {
		p.Log.Log("updater: current %s is up to date (latest tag %s)", agentVer, meta.TagName)
		return nil
	}
	if !p.tryBeginUpdate() {
		return nil
	}
	p.Log.Log("updater: update available: %s -> %s", agentVer, meta.TagName)

	stagingDir := filepath.Join(p.DataDir, "updates", p.stagingID())
	dl := Downloader{Client: p.Client, Store: p.Store, Log: p.Log}
	staged, err := dl.DownloadAndVerify(ctx, meta, agentVer, stagingDir)
	if err != nil {
		p.clearInFlight()
		return err
	}

	helper := p.HelperExe
	if helper == "" {
		helper, err = os.Executable()
		if err != nil {
			p.clearInFlight()
			_ = p.Store.RemoveAll(stagingDir)
			return fmt.Errorf("resolve helper exe: %w", err)
		}
	}

	p.Log.Log("updater: triggering apply-update for %s", staged.ExePath)
	err = TriggerApply(ctx, p.Spawner, TriggerOptions{
		HelperExe:     helper,
		StagedExePath: staged.ExePath,
		BinaryPath:    p.BinaryPath,
		ServiceName:   p.Service,
		HealthWait:    DefaultHealthWait,
	})
	if err != nil {
		p.clearInFlight()
		_ = p.Store.RemoveAll(stagingDir)
		return fmt.Errorf("trigger apply-update: %w", err)
	}
	// Keep in-flight set: helper should stop this process. If it does not,
	// tryBeginUpdate clears the flag after UpdateInFlightTTL.
	return nil
}
