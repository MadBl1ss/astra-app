<#
.SYNOPSIS
  Runs the TUN checks of stage 02 and writes a report to .dev\tun-check.txt.

.DESCRIPTION
  Start the daemon first, in an administrator console and WITHOUT --no-tun:

      .\bin\strelkad.exe run --data .dev --engines engines

  Then run this script from any console. It turns the VPN on, looks at the
  exit IP of curl.exe before and after putting it on the VPN list, switches
  the server and the mode, and checks that the engines were never restarted.
  Everything it changes it puts back. The VPN is left on at the end.

  It refuses to start a second TUN next to a VPN that is already running.
#>
$ErrorActionPreference = 'Stop'
# strelkactl prints UTF-8; without this its Russian text and the server names
# come out garbled when captured.
[Console]::OutputEncoding = [Text.Encoding]::UTF8

$root = Split-Path -Parent $PSScriptRoot
$ctl = Join-Path $root 'bin\strelkactl.exe'
$data = Join-Path $root '.dev'
$report = Join-Path $data 'tun-check.txt'
$lines = New-Object System.Collections.Generic.List[string]
$script:failed = 0

function Say([string]$text) {
    Write-Host $text
    $lines.Add($text)
}

function Check([string]$name, [bool]$ok, [string]$detail) {
    if ($ok) { Say "  OK    ${name}: $detail" }
    else { Say "  НЕТ   ${name}: $detail"; $script:failed++ }
}

# Runs strelkactl and returns its output as lines; the exit code stays in $LASTEXITCODE.
function Ctl {
    $old = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $out = & $ctl @args 2>&1 | ForEach-Object { "$_" }
    $ErrorActionPreference = $old
    return $out
}

function Get-State { (Ctl call state.get | Out-String) | ConvertFrom-Json }
function Get-Engines { ((Ctl call engine.status | Out-String) | ConvertFrom-Json).engines }

function Get-ExitIP {
    $ip = & curl.exe -s -m 15 https://api.ipify.org
    if ($ip -match '^\d+\.\d+\.\d+\.\d+$') { return $ip }
    return ''
}

# The report keeps the first half of an address: enough to tell them apart.
function Mask([string]$ip) {
    if (-not $ip) { return 'нет ответа' }
    return ($ip -replace '^(\d+\.\d+)\.\d+\.\d+$', '$1.x.x')
}

function Save-Report {
    Set-Content -LiteralPath $report -Value $lines -Encoding UTF8
    Write-Host ''
    Write-Host "Отчёт: $report"
}

Say "Проверка Astra с TUN, $(Get-Date -Format 'dd.MM.yyyy HH:mm:ss')"
Say ''

if (-not (Test-Path -LiteralPath $ctl)) {
    Say "Нет $ctl. Собери: cd core; go build -o ..\bin\ .\cmd\..."
    exit 1
}
$status = Ctl status
if ($LASTEXITCODE -ne 0) {
    Say 'Служба не отвечает. Запусти её в консоли администратора:'
    Say '    .\bin\strelkad.exe run --data .dev --engines engines'
    Say ($status -join "`n")
    Save-Report
    exit 1
}
$state = Get-State

# Two TUNs at once fight over the routes. If our VPN is off and some sing-box
# is running, that is the old Strelka.
if ($state.vpn.phase -eq 'off') {
    $foreign = @(Get-Process -Name 'sing-box', 'xray' -ErrorAction SilentlyContinue)
    if ($foreign.Count -gt 0) {
        Say 'Похоже, старая Стрелка ещё работает:'
        $foreign | ForEach-Object { Say ("    {0} (pid {1})" -f $_.ProcessName, $_.Id) }
        Say 'Сначала выключи её (шаг 1 в docs\stages\02-tun-check.md), потом запусти проверку снова.'
        Save-Report
        exit 1
    }
}
if ($state.servers.Count -eq 0) {
    Say 'В профиле нет серверов. Перенеси их: .\bin\strelkactl.exe import-legacy <папка старой Стрелки>'
    Save-Report
    exit 1
}

Say '1. Включение'
if ($state.vpn.phase -ne 'on') {
    $out = Ctl start
    Say (($out | ForEach-Object { "    $_" }) -join "`n")
}
$state = Get-State
Check 'VPN включён' ($state.vpn.phase -eq 'on') "фаза $($state.vpn.phase)"
if ($state.vpn.phase -ne 'on') {
    Say ''
    Say 'VPN не включился. Причина:'
    Say "    $($state.vpn.failText) ($($state.vpn.failReason))"
    Say 'Последние строки лога движка:'
    $log = Join-Path $data 'logs\sing-box.log'
    if (Test-Path -LiteralPath $log) { Get-Content -LiteralPath $log -Tail 25 -Encoding UTF8 | ForEach-Object { Say "    $_" } }
    Save-Report
    exit 1
}
$adapter = Get-NetAdapter -Name 'Strelka' -ErrorAction SilentlyContinue
Check 'Сетевой адаптер Strelka' ($null -ne $adapter -and $adapter.Status -eq 'Up') $(if ($adapter) { "состояние $($adapter.Status)" } else { 'не найден' })
$before = Get-Engines
$before | ForEach-Object { Say ("        {0}: pid {1}" -f $_.name, $_.pid) }
$original = $state.server.selected
Say "        сервер $original, режим $($state.vpn.mode)"
Say ''

Say '2. Раздельное туннелирование'
# curl.exe must start outside the VPN list for the comparison to mean anything.
Ctl rules rm vpn process curl.exe | Out-Null
Start-Sleep -Seconds 2
$homeIP = Get-ExitIP
Check 'curl.exe не в списке: выходит напрямую' ($homeIP -ne '') "адрес $(Mask $homeIP) (должен быть домашний)"

# A site from the auto list, fetched by a program that is on no list: the
# route is decided by the site alone. The download is slowed down so that the
# connection is still open when the daemon is asked about it.
$why = Ctl rules check instagram.com
Say "        $($why -join ' ')"
$slow = Start-Process -FilePath 'curl.exe' -WindowStyle Hidden -PassThru -ArgumentList @(
    '-s', '-m', '12', '--limit-rate', '2k', '-o', 'NUL', 'https://www.instagram.com/')
Start-Sleep -Seconds 3
$seen = @(Ctl conns | Where-Object { $_ -match 'instagram' })
$slow | Wait-Process -Timeout 15 -ErrorAction SilentlyContinue
if ($seen.Count -eq 0) {
    Check 'Сайт из автосписка идёт через VPN' $false 'соединение с instagram.com не появилось в списке службы'
} else {
    Check 'Сайт из автосписка идёт через VPN' ($seen[0] -match '^vpn\s') (($seen[0] -replace '\s+', ' ').Trim())
}

Ctl rules add vpn curl.exe | Out-Null
Start-Sleep -Seconds 3
$viaVPN = Get-ExitIP
Check 'curl.exe в списке «через VPN»: выходит с сервера' ($viaVPN -ne '' -and $viaVPN -ne $homeIP) "адрес $(Mask $viaVPN)"
Say ''

Say '3. Смена сервера без перезапуска'
Say '        проверяю серверы, это до полуминуты...'
Ctl test | Out-Null
$state = Get-State
$alive = @($state.servers | Where-Object { $_.alive -and $_.id -ne $original } | Sort-Object ping)
Say "        отвечают $(@($state.servers | Where-Object { $_.alive }).Count) из $($state.servers.Count)"
if ($alive.Count -eq 0) {
    Check 'Есть второй живой сервер' $false 'переключаться не на что'
} else {
    $other = $alive[0].id
    Ctl select $other | Out-Null
    Start-Sleep -Seconds 3
    $viaOther = Get-ExitIP
    Check "Переключение на $other" ($viaOther -ne '' -and $viaOther -ne $homeIP) "адрес $(Mask $viaOther) (был $(Mask $viaVPN))"
    Ctl select $original | Out-Null
    Start-Sleep -Seconds 2
}
Say ''

Say '4. Правило убрано — снова напрямую'
Ctl rules rm vpn process curl.exe | Out-Null
Start-Sleep -Seconds 3
$back = Get-ExitIP
Check 'curl.exe убран из списка' ($back -ne '' -and $back -eq $homeIP) "адрес $(Mask $back)"
Say ''

Say '5. Режим «весь трафик»'
Ctl mode global | Out-Null
Start-Sleep -Seconds 3
$globalIP = Get-ExitIP
Check 'В режиме «весь трафик» curl.exe идёт через VPN' ($globalIP -ne '' -and $globalIP -ne $homeIP) "адрес $(Mask $globalIP)"
Ctl mode rule | Out-Null
Start-Sleep -Seconds 3
$rule = Get-ExitIP
Check 'В режиме «только список» снова напрямую' ($rule -ne '' -and $rule -eq $homeIP) "адрес $(Mask $rule)"
Say ''

Say '6. Сервер через Xray в режиме «весь трафик»'
# The one case where the rule that exempts our own xray.exe is all that
# stands between the helper and a loop: its connections to the server would
# otherwise be sent to the VPN, that is back into itself.
$viaXray = @($state.servers | Where-Object { $_.alive -and $_.via -eq 'xray' } | Sort-Object ping)
if ($viaXray.Count -eq 0) {
    Say '        живых серверов через Xray нет, пропускаю'
} else {
    $x = $viaXray[0].id
    Ctl select $x | Out-Null
    Ctl mode global | Out-Null
    Start-Sleep -Seconds 4
    $xrayIP = Get-ExitIP
    Check "Через $x весь трафик идёт в VPN" ($xrayIP -ne '' -and $xrayIP -ne $homeIP) "адрес $(Mask $xrayIP)"
    Ctl mode rule | Out-Null
    Ctl select $original | Out-Null
    Start-Sleep -Seconds 2
}
Say ''

Say '7. Движки не перезапускались'
$after = Get-Engines
foreach ($e in $before) {
    $now = $after | Where-Object { $_.name -eq $e.name }
    Check $e.name ($null -ne $now -and $now.pid -eq $e.pid -and $now.restarts -eq 0) "pid был $($e.pid), стал $($now.pid), падений $($now.restarts)"
}
Say ''

Say '8. Локальная сеть (strict_route)'
# The router is the default gateway of a physical adapter; the TUN has a
# default route of its own, with a better metric.
$physical = @(Get-NetAdapter -Physical -ErrorAction SilentlyContinue | Where-Object { $_.Status -eq 'Up' } | ForEach-Object { $_.ifIndex })
$gw = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue |
    Where-Object { $_.NextHop -ne '0.0.0.0' -and $physical -contains $_.ifIndex } | Sort-Object RouteMetric | Select-Object -First 1
if ($gw) {
    $ping = Test-Connection -ComputerName $gw.NextHop -Count 2 -Quiet -ErrorAction SilentlyContinue
    Check 'Роутер отвечает' ([bool]$ping) "шлюз $(Mask $gw.NextHop)"
} else {
    Say '        шлюз не найден, пропускаю'
}
Say ''

Say 'Состояние в конце:'
Ctl status | ForEach-Object { Say "    $_" }
Say ''
Say 'Журнал службы (последние записи):'
$journal = Join-Path $data 'journal.jsonl'
if (Test-Path -LiteralPath $journal) {
    Get-Content -LiteralPath $journal -Tail 30 -Encoding UTF8 | ForEach-Object {
        try {
            $e = $_ | ConvertFrom-Json
            # Windows PowerShell leaves the time a string, PowerShell 7 parses it.
            $t = ([datetime]$e.t).ToLocalTime().ToString('dd.MM HH:mm:ss')
            Say ("    {0} [{1}] {2}" -f $t, $e.level, $e.text)
        } catch { }
    }
}
# The engines log warnings and errors only, so a quiet day leaves nothing.
$today = @((Get-Date -Format 'yyyy-MM-dd'), (Get-Date -Format 'yyyy/MM/dd'))
foreach ($name in 'sing-box', 'xray') {
    $log = Join-Path $data "logs\$name.log"
    if (Test-Path -LiteralPath $log) {
        Say ''
        Say "Лог $name за сегодня (последние строки):"
        $recent = @(Get-Content -LiteralPath $log -Tail 400 -Encoding UTF8 |
            Where-Object { $_.Contains($today[0]) -or $_.Contains($today[1]) } | Select-Object -Last 25)
        if ($recent.Count -eq 0) { Say '    записей нет' }
        $recent | ForEach-Object { Say "    $_" }
    }
}
Say ''
if ($script:failed -eq 0) { Say 'ИТОГ: все проверки прошли.' }
else { Say "ИТОГ: не прошло проверок: $($script:failed)." }
Say 'VPN оставлен включённым. Выключить: .\bin\strelkactl.exe stop'
Save-Report
