<#
.SYNOPSIS
  Puts the pinned engines into engines\ (ignored by git): downloads the
  official release archives and checks them against scripts\engines.json.

.DESCRIPTION
  Versions, addresses and SHA-256 sums live in scripts\engines.json. The
  archive is checked against the digest GitHub publishes for it, and every
  binary taken out of it against its own sum, before anything lands in the
  target folder. A mismatch stops the script with exit code 1 and leaves the
  target as it was.

  -From copies from a folder that already has the binaries (the old Strelka,
  say) instead of downloading; the same sums are checked, so a different
  version is refused rather than quietly used. The source is only read:
  nothing in it is stopped, changed or deleted.

  Licenses go to <target>\licenses, with the full GPL-3.0 text (sing-box's
  own LICENSE only points at it) and SOURCES.txt: which version of which
  engine is shipped, under what license, and where its source is. The
  installer ships them with the engines.

.EXAMPLE
  .\scripts\fetch-engines.ps1
.EXAMPLE
  .\scripts\fetch-engines.ps1 -Target C:\build\engines
.EXAMPLE
  .\scripts\fetch-engines.ps1 -From 'C:\path\to\old-strelka'
#>
param(
    [string]$Target,
    [string]$From,
    [string]$Platform = 'windows-amd64',
    # Only check what is already in the target: exit 0 if every pinned
    # binary is there with its sum, 1 otherwise. Nothing is downloaded.
    [switch]$Verify
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is many times slower with the bar
Add-Type -AssemblyName System.IO.Compression.FileSystem

$root = Split-Path -Parent $PSScriptRoot
if (-not $Target) { $Target = Join-Path $root 'engines' }
$pins = (Get-Content -Raw -Encoding UTF8 (Join-Path $PSScriptRoot 'engines.json') | ConvertFrom-Json).$Platform
if (-not $pins) { Write-Error "В engines.json нет платформы $Platform" }

function Get-Sha256([string]$Path) {
    (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
}

if ($Verify) {
    $bad = 0
    foreach ($e in $pins) {
        foreach ($f in $e.files) {
            $path = Join-Path $Target $f.to
            if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
                Write-Host "  нет $($f.to)"; $bad++; continue
            }
            if ($f.sha256 -and (Get-Sha256 $path) -ne $f.sha256) {
                Write-Host "  $($f.to) не совпал с закреплённым ($($e.name) $($e.version))"; $bad++
            }
        }
    }
    foreach ($leaf in 'GPL-3.0.txt', 'SOURCES.txt') {
        if (-not (Test-Path -LiteralPath (Join-Path $Target "licenses\$leaf") -PathType Leaf)) {
            Write-Host "  нет licenses\$leaf"; $bad++
        }
    }
    if ($bad -gt 0) { exit 1 }
    Write-Host "Движки в $Target — закреплённых версий."
    exit 0
}

# Everything is assembled in a staging folder first and moved into the
# target only when every sum has matched.
$stage = Join-Path ([IO.Path]::GetTempPath()) ("strelka-engines-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path (Join-Path $stage 'out\licenses') | Out-Null

try {
    foreach ($e in $pins) {
        if ($From) {
            foreach ($f in $e.files) {
                $leaf = Split-Path -Leaf $f.to
                $src = Join-Path $From $leaf
                if (-not $f.sha256) { continue }   # licenses are not in an installed folder
                if (-not (Test-Path -LiteralPath $src -PathType Leaf)) {
                    Write-Error "В папке $From нет $leaf"
                }
                Copy-Item -LiteralPath $src -Destination (Join-Path $stage "out\$($f.to)")
            }
        } else {
            $zip = Join-Path $stage "$($e.name).zip"
            Write-Host "$($e.name) $($e.version): $($e.archive.url)"
            Invoke-WebRequest -UseBasicParsing -Uri $e.archive.url -OutFile $zip
            $got = Get-Sha256 $zip
            if ($got -ne $e.archive.sha256) {
                Write-Error "Архив $($e.name) не совпал с закреплённым: $got вместо $($e.archive.sha256)"
            }
            $archive = [IO.Compression.ZipFile]::OpenRead($zip)
            try {
                foreach ($f in $e.files) {
                    $entry = $archive.GetEntry($f.from)
                    if (-not $entry) { Write-Error "В архиве $($e.name) нет $($f.from)" }
                    $out = Join-Path $stage "out\$($f.to)"
                    [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $out, $true)
                }
            } finally {
                $archive.Dispose()
            }
        }
        foreach ($f in $e.files) {
            if (-not $f.sha256) { continue }
            $out = Join-Path $stage "out\$($f.to)"
            $got = Get-Sha256 $out
            if ($got -ne $f.sha256) {
                Write-Error "$($f.to) не совпал с закреплённым ($($e.name) $($e.version)): $got"
            }
            Write-Host ("  {0,-14} {1,6:N1} МБ  sha256 сошёлся" -f $f.to, ((Get-Item -LiteralPath $out).Length / 1MB))
        }
    }

    # sing-box ships a short LICENSE that points at the GPL; the full text
    # and where the sources of the very versions shipped live go along.
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'licenses\GPL-3.0.txt') -Destination (Join-Path $stage 'out\licenses\GPL-3.0.txt')
    $lines = @(
        'Движки в составе Astra — отдельные программы со своими лицензиями.'
        'Astra запускает их как есть, без изменений.'
        ''
    )
    foreach ($e in $pins) {
        $text = ($e.files | Where-Object { $_.to -like 'licenses/*' } | ForEach-Object { Split-Path -Leaf $_.to }) -join ', '
        if ($e.license -like 'GPL-3.0*') { $text = "$text, полный текст — GPL-3.0.txt" }
        $lines += "$($e.name) $($e.version) — $($e.license) ($text)"
        $lines += "  исходный код: $($e.source)"
        $lines += "  выпуск: $($e.archive.url)"
        $lines += ''
    }
    $lines += 'Сама Astra — MIT (astra-LICENSE.txt), исходный код: https://github.com/MadBl1ss/astra-app'
    [IO.File]::WriteAllLines((Join-Path $stage 'out\licenses\SOURCES.txt'), [string[]]$lines, [Text.UTF8Encoding]::new($true))

    New-Item -ItemType Directory -Force -Path $Target | Out-Null
    Copy-Item -Path (Join-Path $stage 'out\*') -Destination $Target -Recurse -Force
    Write-Host "Готово: $Target"
} finally {
    Remove-Item -Recurse -Force -LiteralPath $stage -ErrorAction SilentlyContinue
}
