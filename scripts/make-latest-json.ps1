<#
.SYNOPSIS
  Writes latest.json, the file the window's updater reads, for an installer
  that has been built and signed.

.DESCRIPTION
  The updater asks
  https://github.com/MadBl1ss/strelka-app/releases/latest/download/latest.json
  (plugins.updater in app\src-tauri\tauri.conf.json) and gets the version,
  the notes, the date, and for Windows the address of the installer with its
  signature. The signature is the content of <installer>.sig, which the
  build writes next to the installer when TAURI_SIGNING_PRIVATE_KEY is set.

  The notes are the section of CHANGELOG.md for this version: the lines
  between "## <version>" and the next "## ".

  -BaseUrl is where the installer will be downloaded from; by default the
  GitHub release v<version>. A test feed passes its own address.

.EXAMPLE
  .\scripts\make-latest-json.ps1 -Installer app\src-tauri\target\release\bundle\nsis\Strelka_0.1.0_x64-setup.exe
.EXAMPLE
  .\scripts\make-latest-json.ps1 -Installer .\Strelka_0.1.1_x64-setup.exe -BaseUrl http://127.0.0.1:8765 -Out .\feed\latest.json
#>
param(
    [Parameter(Mandatory)][string]$Installer,
    [string]$Version,
    [string]$BaseUrl,
    [string]$Out,
    # Instead of CHANGELOG.md: for a test feed of made-up versions.
    [string]$Notes
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

$Installer = (Resolve-Path -LiteralPath $Installer).Path
$sigPath = "$Installer.sig"
if (-not (Test-Path -LiteralPath $sigPath -PathType Leaf)) {
    Write-Error "Нет подписи $sigPath. Установщик собирают с TAURI_SIGNING_PRIVATE_KEY и TAURI_SIGNING_PRIVATE_KEY_PASSWORD."
}
if (-not $Version) {
    $Version = (Get-Content -Raw -Encoding UTF8 (Join-Path $root 'app\package.json') | ConvertFrom-Json).version
}
$name = Split-Path -Leaf $Installer
if (-not $BaseUrl) { $BaseUrl = "https://github.com/MadBl1ss/strelka-app/releases/download/v$Version" }
if (-not $Out) { $Out = Join-Path (Split-Path -Parent $Installer) 'latest.json' }

# The notes of this version from CHANGELOG.md.
$notes = $Notes
$changelog = Join-Path $root 'CHANGELOG.md'
if (-not $notes -and (Test-Path -LiteralPath $changelog)) {
    $inside = $false
    $taken = foreach ($line in Get-Content -Encoding UTF8 -LiteralPath $changelog) {
        if ($line -match '^##\s+v?([0-9][^\s]*)') {
            $inside = ($Matches[1] -eq $Version)
            continue
        }
        if ($inside) { $line }
    }
    $notes = (($taken -join "`n").Trim())
}
if (-not $notes) { Write-Warning "В CHANGELOG.md нет раздела «## $Version»: описание выпуска будет пустым." }

$manifest = [ordered]@{
    version   = $Version
    notes     = $notes
    pub_date  = (Get-Date).ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'")
    platforms = [ordered]@{
        'windows-x86_64' = [ordered]@{
            signature = (Get-Content -Raw -LiteralPath $sigPath).Trim()
            url       = "$($BaseUrl.TrimEnd('/'))/$([Uri]::EscapeDataString($name))"
        }
    }
}
$json = $manifest | ConvertTo-Json -Depth 5
[IO.File]::WriteAllText($Out, $json, [Text.UTF8Encoding]::new($false))
Write-Host "latest.json: $Out ($Version)"
