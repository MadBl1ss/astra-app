<#
.SYNOPSIS
  Copies the engines for development into engines\ (ignored by git).

.DESCRIPTION
  The source is a folder that already has sing-box.exe, libcronet.dll and
  xray.exe - usually the installed old Strelka. The source is only read:
  nothing in it is stopped, changed or deleted, so a VPN running from it
  keeps working.

.EXAMPLE
  .\scripts\fetch-engines.ps1 -From 'C:\path\to\old-strelka'
#>
param(
    [Parameter(Mandatory = $true)]
    [string]$From
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $From -PathType Container)) {
    Write-Error "Папка не найдена: $From"
}

$target = Join-Path (Split-Path -Parent $PSScriptRoot) 'engines'
New-Item -ItemType Directory -Force -Path $target | Out-Null

# sing-box.exe is required; libcronet.dll is what its naive outbound loads,
# and xray.exe serves the servers sing-box cannot reach on its own.
$files = @(
    @{ Name = 'sing-box.exe';  Required = $true },
    @{ Name = 'libcronet.dll'; Required = $false },
    @{ Name = 'xray.exe';      Required = $false }
)

foreach ($f in $files) {
    $src = Join-Path $From $f.Name
    if (-not (Test-Path -LiteralPath $src -PathType Leaf)) {
        if ($f.Required) {
            Write-Error "В папке $From нет $($f.Name)"
        }
        Write-Warning "$($f.Name) не найден, пропускаю"
        continue
    }
    Copy-Item -LiteralPath $src -Destination (Join-Path $target $f.Name) -Force
    $size = [math]::Round((Get-Item -LiteralPath $src).Length / 1MB, 1)
    Write-Host ("{0,-14} {1,6} МБ" -f $f.Name, $size)
}

$version = & (Join-Path $target 'sing-box.exe') version | Select-Object -First 1
Write-Host "Готово: $target ($version)"
