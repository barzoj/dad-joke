# Shared Windows build helper for CI and Release.
# Documents the canonical -ldflags injection for compile-time metadata:
#   Version, Commit, ReleaseAPIURL in package internal/buildinfo.
#
# Usage:
#   .\scripts\build.ps1 [-Version <ver>] [-Commit <sha>] [-Output <path>] [-ReleaseAPIURL <url>]
#
# Defaults: Version=0.0.1-dev, Commit=unknown (or current HEAD short SHA when available).

[CmdletBinding()]
param(
    [string]$Version = "0.0.1-dev",
    [string]$Commit = "",
    [string]$Output = "dad-joke.exe",
    [string]$ReleaseAPIURL = "https://api.github.com/repos/barzoj/dad-joke/releases/latest",
    # Optional build tags (e.g. e2ediag for Windows E2E IPC diagnostics).
    [string]$Tags = ""
)

$ErrorActionPreference = "Stop"

$pkg = "github.com/barzoj/dad-joke/internal/buildinfo"

if ([string]::IsNullOrWhiteSpace($Commit)) {
    try {
        $Commit = (git rev-parse --short HEAD 2>$null).Trim()
    } catch {
        $Commit = "unknown"
    }
    if ([string]::IsNullOrWhiteSpace($Commit)) {
        $Commit = "unknown"
    }
}

# Strip a leading v from tags when callers pass v0.0.1.
if ($Version -match '^v(.+)$') {
    $Version = $Matches[1]
}

$ldflags = @(
    "-X", "$pkg.Version=$Version",
    "-X", "$pkg.Commit=$Commit",
    "-X", "$pkg.ReleaseAPIURL=$ReleaseAPIURL"
) -join " "

$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

$tagArgs = @()
if (-not [string]::IsNullOrWhiteSpace($Tags)) {
    $tagArgs = @("-tags", $Tags)
    Write-Host "Building $Output (Version=$Version Commit=$Commit Tags=$Tags)"
} else {
    Write-Host "Building $Output (Version=$Version Commit=$Commit)"
}
& go build -trimpath -ldflags $ldflags @tagArgs -o $Output ./cmd/dad-joke
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

Write-Host "Built $Output"
