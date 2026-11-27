package updater

import (
	"context"
	"fmt"
	"path/filepath"
)

// StagingResult holds verified download paths under an ACL-protected staging dir.
type StagingResult struct {
	Dir     string
	ExePath string
	SHAPath string
	TagName string
	Version string // normalized, no leading v
}

// Downloader downloads and strictly verifies release assets into staging.
type Downloader struct {
	Client ReleaseClient
	Store  FileStore
	Log    Logger
}

// DownloadAndVerify fetches AssetEXE + AssetSHA256 into stagingDir, verifies
// SHA-256, and leaves only the verified exe (sha sidecar may remain). On any
// failure, stagingDir contents are removed.
func (d *Downloader) DownloadAndVerify(ctx context.Context, meta ReleaseMeta, agentVersion, stagingDir string) (StagingResult, error) {
	if d.Log == nil {
		d.Log = NopLogger{}
	}
	cleanup := func() {
		if stagingDir != "" {
			_ = d.Store.RemoveAll(stagingDir)
		}
	}

	exeAsset, err := FindAsset(meta, AssetEXE)
	if err != nil {
		cleanup()
		return StagingResult{}, err
	}
	shaAsset, err := FindAsset(meta, AssetSHA256)
	if err != nil {
		cleanup()
		return StagingResult{}, err
	}

	if err := d.Store.MkdirAll(stagingDir); err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("create staging dir: %w", err)
	}
	_ = d.Store.ApplyDataACL(stagingDir)

	shaBytes, err := d.Client.Download(ctx, shaAsset.BrowserDownloadURL, agentVersion, 4096)
	if err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("download sha256: %w", err)
	}
	expected, _, err := ParseSHA256File(shaBytes)
	if err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("parse sha256: %w", err)
	}

	exeBytes, err := d.Client.Download(ctx, exeAsset.BrowserDownloadURL, agentVersion, MaxAssetBytes)
	if err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("download exe: %w", err)
	}
	got := HashBytes(exeBytes)
	if err := VerifySHA256(got, expected); err != nil {
		cleanup()
		d.Log.Log("updater: sha256 verification failed: %v", err)
		return StagingResult{}, err
	}

	exePath := filepath.Join(stagingDir, AssetEXE)
	shaPath := filepath.Join(stagingDir, AssetSHA256)
	if err := d.Store.WriteFile(exePath, exeBytes); err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("write staged exe: %w", err)
	}
	if err := d.Store.WriteFile(shaPath, shaBytes); err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("write staged sha256: %w", err)
	}
	_ = d.Store.ApplyDataACL(exePath)
	_ = d.Store.ApplyDataACL(shaPath)

	ver, err := ParseVersion(meta.TagName)
	if err != nil {
		cleanup()
		return StagingResult{}, fmt.Errorf("parse tag: %w", err)
	}

	return StagingResult{
		Dir:     stagingDir,
		ExePath: exePath,
		SHAPath: shaPath,
		TagName: meta.TagName,
		Version: ver.Raw,
	}, nil
}
