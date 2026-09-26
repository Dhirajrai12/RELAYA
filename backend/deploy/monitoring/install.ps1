<#
Installs monitoring for Relaya on this server: Prometheus (scrapes and stores
metrics, evaluates alerts.yml), Grafana (the "Relaya overview" dashboard) and
windows_exporter (CPU, memory, disk, service state, backup status). Everything
listens on 127.0.0.1 only: open http://localhost:3000 on the server.

  .\install.ps1 -Downloads <folder with prometheus.zip, grafana.zip, windows_exporter.exe, WinSW-x64.exe>

Safe to re-run: configs and the dashboard are refreshed, stored metrics and the
Grafana database are kept, and the Grafana admin password is only set once
(it is added to C:\relaya\secrets.txt).
#>
param(
  [Parameter(Mandatory)] [string] $Downloads,
  [string] $InstallDir = 'C:\relaya',
  # Set when IIS publishes Grafana (web.config "grafana" rule), e.g. https://server.aegonassett.com/grafana/
  [string] $PublicUrl = ''
)

$ErrorActionPreference = 'Stop'
function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run this script from an elevated PowerShell.' }
foreach ($f in 'prometheus.zip', 'grafana.zip', 'windows_exporter.exe', 'WinSW-x64.exe') {
  if (-not (Test-Path (Join-Path $Downloads $f))) { throw "Missing $Downloads\$f" }
}

$mon = Join-Path $InstallDir 'monitoring'
$prom = Join-Path $mon 'prometheus'
$graf = Join-Path $mon 'grafana'
$textfile = Join-Path $mon 'textfile'
$src = $PSScriptRoot
New-Item -ItemType Directory -Force $mon, $prom, $graf, $textfile, "$prom\data", "$prom\logs", "$graf\data", "$graf\logs" | Out-Null

function Expand-Flat($zip, $dest) {
  # Unpack an archive whose files sit in one top folder, into $dest (keeping data folders).
  $tmp = Join-Path $env:TEMP ("relaya-mon-" + [guid]::NewGuid())
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $top = Get-ChildItem $tmp | Select-Object -First 1
  Get-ChildItem $top.FullName | ForEach-Object {
    if ($_.Name -in 'data') { return }
    Copy-Item $_.FullName $dest -Recurse -Force
  }
  Remove-Item $tmp -Recurse -Force
}

function Stop-IfRunning($name) {
  $s = Get-Service $name -ErrorAction SilentlyContinue
  if ($s -and $s.Status -ne 'Stopped') { Stop-Service $name -Force; $s.WaitForStatus('Stopped', '00:00:30') }
}

function Install-WinSW($id, $display, $description, $dir, $exe, $arguments) {
  # WinSW turns a console program into a Windows service (it restarts it if it dies).
  $wrapper = Join-Path $dir "$id.exe"
  Copy-Item (Join-Path $Downloads 'WinSW-x64.exe') $wrapper -Force
  @"
<service>
  <id>$id</id>
  <name>$display</name>
  <description>$description</description>
  <executable>$exe</executable>
  <arguments>$arguments</arguments>
  <workingdirectory>$dir</workingdirectory>
  <logpath>$dir\logs</logpath>
  <log mode="roll-by-size"><sizeThreshold>10240</sizeThreshold><keepFiles>5</keepFiles></log>
  <onfailure action="restart" delay="10 sec"/>
  <onfailure action="restart" delay="30 sec"/>
  <resetfailure>1 hour</resetfailure>
  <startmode>Automatic</startmode>
  <stoptimeout>20 sec</stoptimeout>
</service>
"@ | Set-Content (Join-Path $dir "$id.xml") -Encoding UTF8
  if (-not (Get-Service $id -ErrorAction SilentlyContinue)) {
    & $wrapper install | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "installing $id failed" }
  }
  # Its own virtual account, like the Relaya services: no LocalSystem.
  sc.exe config $id obj= "NT SERVICE\$id" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "sc config $id failed" }
}

foreach ($s in 'relaya-grafana', 'relaya-prometheus', 'relaya-windows-exporter') { Stop-IfRunning $s }

# ---- windows_exporter ------------------------------------------------------------
Step 'windows_exporter (host metrics, service state, backup status)'
$we = Join-Path $mon 'windows_exporter.exe'
Copy-Item (Join-Path $Downloads 'windows_exporter.exe') $we -Force
$weArgs = "--web.listen-address=127.0.0.1:9182 --collectors.enabled=cpu,memory,logical_disk,os,service,system,textfile " +
  "--collector.service.include=`"relaya.*|postgresql-relaya`" --collector.textfile.directories=`"$textfile`""
if (-not (Get-Service relaya-windows-exporter -ErrorAction SilentlyContinue)) {
  sc.exe create relaya-windows-exporter binPath= "`"$we`" $weArgs" start= auto DisplayName= 'Relaya windows_exporter' | Out-Null
} else {
  sc.exe config relaya-windows-exporter binPath= "`"$we`" $weArgs" | Out-Null
}
if ($LASTEXITCODE -ne 0) { throw 'windows_exporter service setup failed' }
sc.exe description relaya-windows-exporter 'Host metrics for Relaya monitoring (127.0.0.1:9182).' | Out-Null
sc.exe failure relaya-windows-exporter reset= 86400 actions= restart/10000/restart/30000 | Out-Null

# ---- Prometheus ------------------------------------------------------------------
Step 'Prometheus (127.0.0.1:9090, 30 days of metrics)'
Expand-Flat (Join-Path $Downloads 'prometheus.zip') $prom
Copy-Item (Join-Path $src 'prometheus.yml'), (Join-Path $src 'alerts.yml') $prom -Force
$token = ((Get-Content (Join-Path $InstallDir 'bin\.env') | Where-Object { $_ -match '^METRICS_TOKEN=' }) -split '=', 2)[1]
if (-not $token) { throw 'METRICS_TOKEN is not set in bin\.env' }
[IO.File]::WriteAllText((Join-Path $prom 'metrics_token'), $token)
Remove-Variable token
& (Join-Path $prom 'promtool.exe') check config (Join-Path $prom 'prometheus.yml') | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'prometheus.yml or alerts.yml is invalid (run promtool check config)' }
Install-WinSW 'relaya-prometheus' 'Relaya Prometheus' 'Collects Relaya metrics (127.0.0.1:9090).' $prom (Join-Path $prom 'prometheus.exe') `
  "--config.file=`"$prom\prometheus.yml`" --storage.tsdb.path=`"$prom\data`" --storage.tsdb.retention.time=30d --web.listen-address=127.0.0.1:9090"

# ---- Grafana ---------------------------------------------------------------------
Step 'Grafana (127.0.0.1:3000, Relaya overview dashboard)'
Expand-Flat (Join-Path $Downloads 'grafana.zip') $graf
$prov = Join-Path $graf 'conf\provisioning'
New-Item -ItemType Directory -Force "$prov\datasources", "$prov\dashboards", "$graf\dashboards" | Out-Null
Copy-Item (Join-Path $src 'grafana\provisioning\datasources\prometheus.yml') "$prov\datasources\relaya.yml" -Force
(Get-Content (Join-Path $src 'grafana\provisioning\dashboards\relaya.yml') -Raw) -replace '__DASHBOARDS__', ("$graf\dashboards" -replace '\\', '/') |
  Set-Content "$prov\dashboards\relaya.yml" -Encoding UTF8
Copy-Item (Join-Path $src 'grafana\dashboards\*.json') "$graf\dashboards" -Force

$ini = Join-Path $graf 'conf\custom.ini'
if (-not (Test-Path $ini)) {
  $b = New-Object byte[] 18; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
  $pw = [Convert]::ToBase64String($b) -replace '[+/=]', 'x'
  Add-Content (Join-Path $InstallDir 'secrets.txt') "grafana admin (http://localhost:3000, user admin): $pw" -Encoding ascii
} else {
  $pw = $null # set once; change it later in Grafana itself
}
$gpath = $graf -replace '\\', '/'
$lines = @(
  '[server]', 'http_addr = 127.0.0.1', 'http_port = 3000',
  $(if ($PublicUrl) { "root_url = $($PublicUrl.TrimEnd('/'))/`r`nserve_from_sub_path = true`r`nenforce_domain = false" } else { '' }), '',
  '[paths]', "data = $gpath/data", "logs = $gpath/logs", "plugins = $gpath/data/plugins", "provisioning = $gpath/conf/provisioning", '',
  '[security]', 'admin_user = admin', 'disable_gravatar = true', 'cookie_samesite = strict',
  # Public: HTTPS-only cookies, Grafana's own CSP with per-page nonces, HSTS; failed logins lock accounts (default).
  # IIS forwards with Grafana's internal host, so the public origin must be trusted for POSTs.
  $(if ($PublicUrl) { "cookie_secure = true`r`ncontent_security_policy = true`r`nstrict_transport_security = true`r`ncsrf_trusted_origins = $(([Uri]$PublicUrl).Host)" } else { '' }), '',
  '[users]', 'allow_sign_up = false', 'allow_org_create = false', '',
  '[auth.anonymous]', 'enabled = false', '',
  '[analytics]', 'reporting_enabled = false', 'check_for_updates = false', 'check_for_plugin_updates = false', 'feedback_links_enabled = false', '',
  '[news]', 'news_feed_enabled = false', '',
  '[dashboards]', "default_home_dashboard_path = $gpath/dashboards/relaya-overview.json"
)
if ($pw) { $lines = $lines -replace '^admin_user = admin$', "admin_user = admin`r`nadmin_password = $pw" }
elseif (Test-Path $ini) { $existing = Select-String -Path $ini -Pattern '^admin_password = ' | Select-Object -First 1; if ($existing) { $lines = $lines -replace '^admin_user = admin$', "admin_user = admin`r`n$($existing.Line)" } }
$lines -join "`r`n" | Set-Content $ini -Encoding ascii
Install-WinSW 'relaya-grafana' 'Relaya Grafana' 'Dashboards for Relaya (127.0.0.1:3000).' $graf (Join-Path $graf 'bin\grafana.exe') `
  "server --homepath `"$graf`" --config `"$ini`""

# ---- permissions -----------------------------------------------------------------
Step 'Lock down permissions'
icacls $mon /inheritance:r /grant:r 'Administrators:(OI)(CI)F' 'SYSTEM:(OI)(CI)F' | Out-Null
# The services may list this folder (Prometheus resolves rule-file paths through it), nothing more.
icacls $mon /grant 'NT SERVICE\relaya-prometheus:(RX)' 'NT SERVICE\relaya-grafana:(RX)' | Out-Null
icacls $prom /grant 'NT SERVICE\relaya-prometheus:(OI)(CI)RX' | Out-Null
icacls "$prom\data" /grant 'NT SERVICE\relaya-prometheus:(OI)(CI)M' | Out-Null
icacls "$prom\logs" /grant 'NT SERVICE\relaya-prometheus:(OI)(CI)M' | Out-Null
icacls $graf /grant 'NT SERVICE\relaya-grafana:(OI)(CI)RX' | Out-Null
icacls "$graf\data" /grant 'NT SERVICE\relaya-grafana:(OI)(CI)M' | Out-Null
icacls "$graf\logs" /grant 'NT SERVICE\relaya-grafana:(OI)(CI)M' | Out-Null
# The metrics token and the Grafana config (admin password) are for their service only.
icacls (Join-Path $prom 'metrics_token') /inheritance:r /grant:r 'Administrators:F' 'SYSTEM:F' 'NT SERVICE\relaya-prometheus:R' | Out-Null
icacls $ini /inheritance:r /grant:r 'Administrators:F' 'SYSTEM:F' 'NT SERVICE\relaya-grafana:R' | Out-Null
# WinSW writes its wrapper log next to the service definition.
icacls $prom /grant 'NT SERVICE\relaya-prometheus:(M)' | Out-Null
icacls $graf /grant 'NT SERVICE\relaya-grafana:(M)' | Out-Null

# ---- start and check -------------------------------------------------------------
Step 'Start and check'
foreach ($s in 'relaya-windows-exporter', 'relaya-prometheus', 'relaya-grafana') { Start-Service $s }
function WaitFor($name, $url) {
  for ($i = 0; $i -lt 60; $i++) {
    try { if ((Invoke-WebRequest $url -UseBasicParsing -TimeoutSec 3).StatusCode -eq 200) { Write-Host "    $name ready"; return } } catch { }
    Start-Sleep -Seconds 2
  }
  throw "$name did not become ready ($url); see its logs folder"
}
WaitFor 'windows_exporter' 'http://127.0.0.1:9182/metrics'
WaitFor 'Prometheus' 'http://127.0.0.1:9090/-/ready'
WaitFor 'Grafana' 'http://127.0.0.1:3000/api/health'
Start-Sleep -Seconds 20 # one scrape round
$targets = (Invoke-RestMethod 'http://127.0.0.1:9090/api/v1/targets').data.activeTargets
foreach ($t in $targets) { '    target {0,-14} {1}{2}' -f $t.labels.job, $t.health, $(if ($t.lastError) { " ($($t.lastError))" }) | Write-Host }
Write-Host 'Done. Open http://localhost:3000 on this server (user admin; password in secrets.txt).'
