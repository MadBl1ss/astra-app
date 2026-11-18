<#
.SYNOPSIS
  Builds the Windows installer: the daemon, the pinned engines, the window,
  and NSIS around them; with the signing key, also the update files.

.DESCRIPTION
  1. strelkad.exe and strelkactl.exe into bin\, stamped with the app's
     version (app\package.json, or -Version).
  2. The engines into engines\ (scripts\fetch-engines.ps1), unless the ones
     already there match the pinned sums.
  3. pnpm tauri build with app\src-tauri\tauri.installer.conf.json, which adds
     bin\ and engines\ as resources and asks for updater artifacts. They are
     kept out of tauri.conf.json: tauri-build copies resources on every
     build, and `pnpm tauri dev` would then fail on a machine that has not
     built the daemon.
  4. With TAURI_SIGNING_PRIVATE_KEY (the key or its path) and
     TAURI_SIGNING_PRIVATE_KEY_PASSWORD set, the installer is signed for the
     updater (<installer>.sig) and latest.json is written next to it
     (scripts\make-latest-json.ps1). Without them the installer is built
     unsigned, and an installed copy of it can still update itself from
     signed releases.

  -Version builds a test version without editing package.json: the window,
  the daemon and the file name all carry it. -UpdateFeed points the build's
  updater at another latest.json, e.g. http://127.0.0.1:8765/latest.json for
  a local test of updating; plain http is then allowed. Neither is for a
  release.

  The installer lands in app\src-tauri\target\release\bundle\nsis\.

  Rust is whatever toolchain is current. On a machine without the MSVC
  build tools set RUSTUP_TOOLCHAIN=stable-x86_64-pc-windows-gnu first; the
  release is built by CI with MSVC.

.EXAMPLE
  .\scripts\build-installer.ps1
.EXAMPLE
  .\scripts\build-installer.ps1 -Version 0.1.1 -UpdateFeed http://127.0.0.1:8765/latest.json
#>
param(
    [string]$Version,
    [string]$UpdateFeed
)
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
if (-not $Version) {
    $Version = (Get-Content -Raw -Encoding UTF8 (Join-Path $root 'app\package.json') | ConvertFrom-Json).version
}
Write-Host "Стрелка $Version"

# 1. The daemon.
$bin = Join-Path $root 'bin'
New-Item -ItemType Directory -Force -Path $bin | Out-Null
Push-Location (Join-Path $root 'core')
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    # strelkactl of an install talks to the installed service's channel.
    $flags = "-s -w -X strelka/internal/daemon.Version=$Version -X main.installedPipe=strelka"
    go build -trimpath -ldflags $flags -o $bin ./cmd/strelkad ./cmd/strelkactl
    if ($LASTEXITCODE -ne 0) { throw 'go build не прошёл' }
} finally {
    Pop-Location
    Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}
Write-Host "Служба: $bin"

# 2. The engines.
$fetch = Join-Path $PSScriptRoot 'fetch-engines.ps1'
& $fetch -Verify
if ($LASTEXITCODE -ne 0) {
    # A mismatch inside is a terminating error and stops this script too;
    # $LASTEXITCODE is not set by a script that does not call exit.
    & $fetch
}

# 3. The window and the installer. Extra settings go in files: quoting JSON
# on a command line differs between PowerShell versions.
$extra = Join-Path ([IO.Path]::GetTempPath()) ("strelka-build-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $extra | Out-Null
$configs = @('src-tauri/tauri.installer.conf.json')
function Add-Config([string]$Name, [hashtable]$Value) {
    $path = Join-Path $extra $Name
    [IO.File]::WriteAllText($path, ($Value | ConvertTo-Json -Depth 6), [Text.UTF8Encoding]::new($false))
    $script:configs += $path
}
Add-Config 'version.json' @{ version = $Version }
$signed = [bool]$env:TAURI_SIGNING_PRIVATE_KEY
if (-not $signed) {
    Write-Warning 'TAURI_SIGNING_PRIVATE_KEY не задан: установщик соберётся без подписи обновлений и без latest.json.'
    Add-Config 'unsigned.json' @{ bundle = @{ createUpdaterArtifacts = $false } }
}
if ($UpdateFeed) {
    Write-Warning "Тестовая сборка: обновления из $UpdateFeed"
    Add-Config 'feed.json' @{ plugins = @{ updater = @{ endpoints = @($UpdateFeed); dangerousInsecureTransportProtocol = $UpdateFeed.StartsWith('http:') } } }
}

Push-Location (Join-Path $root 'app')
try {
    $tauriArgs = @('tauri', 'build')
    foreach ($c in $configs) { $tauriArgs += @('--config', $c) }
    pnpm @tauriArgs
    if ($LASTEXITCODE -ne 0) { throw 'tauri build не прошёл' }
} finally {
    Pop-Location
    Remove-Item -Recurse -Force -LiteralPath $extra -ErrorAction SilentlyContinue
}

$nsis = Join-Path $root 'app\src-tauri\target\release\bundle\nsis'
$installer = Join-Path $nsis "Strelka_${Version}_x64-setup.exe"
if (-not (Test-Path -LiteralPath $installer)) { throw "Нет установщика $installer" }
Write-Host ("Установщик: {0} ({1:N1} МБ)" -f $installer, ((Get-Item -LiteralPath $installer).Length / 1MB))

# 4. What the updater reads.
if ($signed) {
    $base = $null
    if ($UpdateFeed) { $base = $UpdateFeed.Substring(0, $UpdateFeed.LastIndexOf('/')) }
    & (Join-Path $PSScriptRoot 'make-latest-json.ps1') -Installer $installer -Version $Version -BaseUrl $base
}
