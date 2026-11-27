# Package release artifacts for a Windows amd64 build.
# Produces:
#   dad-joke-windows-amd64.exe
#   dad-joke-windows-amd64.exe.sha256   (format: "<hex>  <filename>")
#   manifest.json                         (version, filename, asset, sha256, url)
#
# Usage:
#   .\scripts\package-release.ps1 -Version 0.0.1 -DistDir dist [-Commit <sha>] [-Tag v0.0.1]

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [string]$Commit = "",
    [string]$DistDir = "dist",
    [string]$Tag = ""
)

$ErrorActionPreference = "Stop"

if ($Version -match '^v(.+)$') {
    $Version = $Matches[1]
}
if ([string]::IsNullOrWhiteSpace($Tag)) {
    $Tag = "v$Version"
}

New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

$exeName = "dad-joke-windows-amd64.exe"
$exePath = Join-Path $DistDir $exeName

& "$PSScriptRoot\build.ps1" -Version $Version -Commit $Commit -Output $exePath
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

$hash = (Get-FileHash -Algorithm SHA256 -Path $exePath).Hash.ToLowerInvariant()
$shaPath = "$exePath.sha256"
# GNU coreutils style: two spaces between hash and filename.
Set-Content -Path $shaPath -Value "$hash  $exeName" -NoNewline
Add-Content -Path $shaPath -Value ""

$url = "https://github.com/barzoj/dad-joke/releases/download/$Tag/$exeName"
$manifest = [ordered]@{
    version  = $Version
    filename = $exeName
    asset    = $exeName
    sha256   = $hash
    url      = $url
}
$manifestPath = Join-Path $DistDir "manifest.json"
$manifest | ConvertTo-Json -Compress | Set-Content -Path $manifestPath -Encoding utf8

Write-Host "Packaged: $exePath"
Write-Host "SHA256:   $shaPath ($hash)"
Write-Host "Manifest: $manifestPath"
