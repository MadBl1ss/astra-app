<#
.SYNOPSIS
  Looks for traffic that goes around the VPN while the engine is down, and
  writes a report to .dev\leak-test.txt.

.DESCRIPTION
  Start the daemon first, in an administrator console and WITHOUT --no-tun:

      .\bin\strelkad.exe run --data .dev --engines engines

  The other VPN on the machine has to be off: two tunnels do not mix, and the
  kill switch blocks a VPN it does not know.

  The script asks a "what is my IP" service five times a second. Three
  seconds in it breaks the VPN the way -Action says, and keeps asking. An
  answer with the home address is a leak; no answer, or the server's
  address, is not.

  Run it twice. First with the kill switch off, to see the leak this is all
  about - a test that has never shown a leak proves nothing when it shows
  none:

      .\bin\strelkactl.exe set guard=false
      .\scripts\leak-test.ps1 -Action kill

  Then with it on:

      .\bin\strelkactl.exe set guard=true
      .\scripts\leak-test.ps1 -Action kill
      .\scripts\leak-test.ps1 -Action restart
      .\scripts\leak-test.ps1 -Action subs

  The trap this script is built around: in "list only" mode curl.exe is not
  on any list and goes out directly, so it answers with the home address
  while the VPN is perfectly healthy. The script therefore switches to
  "all traffic" for its run, checks that the answer became the server's
  address before it breaks anything, and puts the mode back at the end.

.PARAMETER Action
  kill     end our sing-box.exe (the one from the engines folder, by PID)
  restart  strelkactl restart
  subs     strelkactl subs update
  none     only watch
#>
param(
    [ValidateSet('kill', 'restart', 'subs', 'none')]
    [string]$Action = 'kill',
    [int]$Seconds = 20,
    [string]$Service = 'api.ipify.org'
)
$ErrorActionPreference = 'Stop'
# strelkactl prints UTF-8; without this its Russian text comes out garbled.
[Console]::OutputEncoding = [Text.Encoding]::UTF8

$root = Split-Path -Parent $PSScriptRoot
$ctl = Join-Path $root 'bin\strelkactl.exe'
$engines = Join-Path $root 'engines'
$report = Join-Path $root '.dev\leak-test.txt'
$lines = New-Object System.Collections.Generic.List[string]

function Say([string]$text) {
    Write-Host $text
    $lines.Add($text)
}

# Runs strelkactl and returns its output as lines; the exit code stays in $LASTEXITCODE.
function Ctl {
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { & $ctl @args 2>&1 | ForEach-Object { "$_" } }
    finally { $ErrorActionPreference = $prev }
}

# One request, by address: with the engine down a name would not resolve,
# and the test would be timing DNS instead of looking for a leak. Short
# timeouts keep the pace: a leaking request answers fast, and those are the
# ones that must not be missed.
function Ask([string]$addr) {
    $out = & curl.exe -4 -s --connect-timeout 0.4 --max-time 0.7 --resolve "${Service}:443:$addr" "https://$Service" 2>$null
    if ($LASTEXITCODE -eq 0 -and "$out" -match '^\d+\.\d+\.\d+\.\d+$') { return "$out" }
    return ''
}

function State { (Ctl call state.get | Out-String | ConvertFrom-Json) }

function Stop-Here([string]$why) {
    Say "ОСТАНОВ: $why"
    $lines | Set-Content -Encoding UTF8 $report
    exit 2
}

if (-not (Test-Path $ctl)) { Stop-Here "нет $ctl. Собери: cd core; go build -o ..\bin\ .\cmd\..." }

Say "Проверка утечек, действие: $Action, $(Get-Date -Format 'dd.MM.yyyy HH:mm:ss')"
$st = State
if ($LASTEXITCODE -ne 0 -or -not $st) { Stop-Here 'служба не отвечает. Запусти strelkad run без --no-tun в консоли администратора.' }
Ctl guard | ForEach-Object { Say "  $_" }
$modeBefore = $st.vpn.mode
$wasOn = $st.vpn.phase -eq 'on'

# 1. The home address: what a leak looks like. Measured with the VPN off.
if ($st.vpn.phase -ne 'off') {
    Ctl stop | Out-Null
}
$addr = ([System.Net.Dns]::GetHostAddresses($Service) | Where-Object { $_.AddressFamily -eq 'InterNetwork' } | Select-Object -First 1).IPAddressToString
if (-not $addr) { Stop-Here "не удалось узнать адрес $Service" }
$home4 = ''
foreach ($i in 1..5) { $home4 = Ask $addr; if ($home4) { break }; Start-Sleep -Milliseconds 300 }
if (-not $home4) { Stop-Here "без VPN $Service не отвечает: проверять нечем" }
Say "Домашний адрес: $home4"

# 2. The VPN on, everything through it.
Ctl mode global | Out-Null
Ctl start | Out-Null
if ($LASTEXITCODE -ne 0) { Ctl mode $modeBefore | Out-Null; Stop-Here 'VPN не включился' }
$via = ''
foreach ($i in 1..20) { $via = Ask $addr; if ($via -and $via -ne $home4) { break }; Start-Sleep -Milliseconds 500 }
if (-not $via -or $via -eq $home4) {
    Ctl mode $modeBefore | Out-Null
    Stop-Here "при включённом VPN ответ «$via», а не адрес сервера. Такая проверка ничего не докажет."
}
Say "Через VPN:      $via"

# 3. Ask, break, keep asking.
$breakAt = 3
$log = New-Object System.Collections.Generic.List[object]
$broken = $false
$sw = [Diagnostics.Stopwatch]::StartNew()
while ($sw.Elapsed.TotalSeconds -lt $Seconds) {
    if (-not $broken -and $sw.Elapsed.TotalSeconds -ge $breakAt) {
        $broken = $true
        switch ($Action) {
            'kill' {
                $box = (Ctl call engine.status | Out-String | ConvertFrom-Json).engines | Where-Object { $_.name -eq 'sing-box' }
                # Ours and only ours: by PID, and the path is checked first.
                if (-not $box.running -or -not $box.path.StartsWith($engines, [StringComparison]::OrdinalIgnoreCase)) {
                    Ctl mode $modeBefore | Out-Null
                    Stop-Here "движок не наш или не запущен: $($box.path)"
                }
                Stop-Process -Id $box.pid -Force
                Say ("{0,6:N2} с  -- убит sing-box.exe, pid {1}" -f $sw.Elapsed.TotalSeconds, $box.pid)
            }
            'restart' {
                Start-Process -FilePath $ctl -ArgumentList 'restart', '--no-wait' -WindowStyle Hidden
                Say ("{0,6:N2} с  -- strelkactl restart" -f $sw.Elapsed.TotalSeconds)
            }
            'subs' {
                Start-Process -FilePath $ctl -ArgumentList 'subs', 'update' -WindowStyle Hidden
                Say ("{0,6:N2} с  -- strelkactl subs update" -f $sw.Elapsed.TotalSeconds)
            }
        }
    }
    $at = $sw.Elapsed.TotalSeconds
    $ip = Ask $addr
    $what = if (-not $ip) { 'нет ответа' } elseif ($ip -eq $home4) { 'ДОМАШНИЙ' } else { 'сервер' }
    $log.Add([pscustomobject]@{ At = $at; IP = $ip; What = $what })
    $rest = 200 - [int](($sw.Elapsed.TotalSeconds - $at) * 1000)
    if ($rest -gt 0) { Start-Sleep -Milliseconds $rest }
}

# 4. The verdict. Runs of the same answer are folded into one line.
$prev = ''
$from = 0.0
$n = 0
foreach ($row in $log) {
    if ($row.What -ne $prev) {
        if ($prev) { Say ("{0,6:N2} с  {1} x{2}" -f $from, $prev, $n) }
        $prev = $row.What; $from = $row.At; $n = 0
    }
    $n++
}
if ($prev) { Say ("{0,6:N2} с  {1} x{2}" -f $from, $prev, $n) }

$leaks = @($log | Where-Object { $_.At -ge $breakAt -and $_.What -eq 'ДОМАШНИЙ' })
$early = @($log | Where-Object { $_.At -lt $breakAt -and $_.What -eq 'ДОМАШНИЙ' })
$silent = @($log | Where-Object { $_.What -eq 'нет ответа' })
$back = @($log | Where-Object { $_.At -ge $breakAt -and $_.What -eq 'сервер' })
Say ''
Say "Запросов: $($log.Count), без ответа: $($silent.Count), через сервер после сбоя: $($back.Count)"
if ($early.Count) { Say "ВНИМАНИЕ: $($early.Count) ответов с домашним адресом ещё до сбоя — VPN не держал трафик и без него." }
if ($leaks.Count) {
    Say ("УТЕЧКА: {0} ответов с домашним адресом, первый на {1:N2} с, последний на {2:N2} с." -f $leaks.Count, $leaks[0].At, $leaks[-1].At)
} else {
    Say 'Утечек нет: после сбоя ни одного ответа с домашним адресом.'
}
if ($Action -ne 'none' -and -not $back.Count) { Say 'VPN за время проверки не восстановился.' }

# 5. Back to how it was.
Ctl mode $modeBefore | Out-Null
if (-not $wasOn) { Ctl stop | Out-Null }
Ctl guard | ForEach-Object { Say "  $_" }
$lines | Set-Content -Encoding UTF8 $report
Say "Отчёт: $report"
if ($leaks.Count) { exit 1 }
