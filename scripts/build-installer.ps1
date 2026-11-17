<#
.SYNOPSIS
  Builds the Windows installer: the daemon, the pinned engines, the window,
  and NSIS around them.

.DESCRIPTION
  1. strelkad.exe and strelkactl.exe into bin\, stamped with the app's
     version from app\src-tauri\tauri.conf.json.
  2. The engines into engines\ (scripts\fetch-engines.ps1), unless the ones
     already there match the pinned sums.
  3. pnpm tauri build with app\src-tauri\tauri.installer.conf.json, which adds
     bin\ and engines\ as resources. They are kept out of tauri.conf.json:
     tauri-build copies resources on every build, and `pnpm tauri dev` would
     then fail on a machine that has not built the daemon.

  The installer lands in app\src-tauri\target\release\bundle\nsis\.

  Rust is whatever toolchain is current. On a machine without the MSVC
  build tools set RUSTUP_TOOLCHAIN=stable-x86_64-pc-windows-gnu first; the
  release is built by CI with MSVC.

.EXAMPLE
  .\scripts\build-installer.ps1
#>
$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$conf = Get-Content -Raw -Encoding UTF8 (Join-Path $root 'app\src-tauri\tauri.conf.json') | ConvertFrom-Json
$version = $conf.version
Write-Host "Стрелка $version"

# 1. The daemon.
$bin = Join-Path $root 'bin'
New-Item -ItemType Directory -Force -Path $bin | Out-Null
Push-Location (Join-Path $root 'core')
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    # strelkactl of an install talks to the installed service's channel.
    $flags = "-s -w -X strelka/internal/daemon.Version=$version -X main.installedPipe=strelka"
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

# 3. The window and the installer.
Push-Location (Join-Path $root 'app')
try {
    pnpm tauri build --config src-tauri/tauri.installer.conf.json
    if ($LASTEXITCODE -ne 0) { throw 'tauri build не прошёл' }
} finally {
    Pop-Location
}
Get-ChildItem (Join-Path $root 'app\src-tauri\target\release\bundle\nsis') -Filter *.exe |
    ForEach-Object { Write-Host ("Установщик: {0} ({1:N1} МБ)" -f $_.FullName, ($_.Length / 1MB)) }
