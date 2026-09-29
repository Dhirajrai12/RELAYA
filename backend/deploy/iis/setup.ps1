<#
.SYNOPSIS
  Installs the platform on a Windows Server behind IIS: Go binaries as Windows
  services, an IIS site that reverse-proxies to them via URL Rewrite + ARR.

.DESCRIPTION
  Safe to re-run. Run from an elevated PowerShell in the backend folder:

    go build -trimpath -o dist\ ./cmd/...
    .\deploy\iis\setup.ps1 -HostName in.example.com

  Before the first run, create <InstallDir>\bin\.env from .env.example
  (DATABASE_URL, MASTER_KEY, INGEST_BASE_URL=https://<HostName>, TRUST_PROXY_HEADERS=true).

  Server-wide changes (all additive): enables the ARR proxy if it is off, and
  adds HTTP_X_REAL_IP to URL Rewrite's allowed server variables.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)] [string] $HostName,
  [string] $InstallDir = 'C:\relaya',
  [string] $SiteName = 'relaya',
  [string] $DistDir = (Join-Path $PSScriptRoot '..\..\dist'),
  [string] $WebDist = (Join-Path $PSScriptRoot '..\..\..\web\dist'),  # dashboard build (npm run build); skipped if missing
  [string] $CertThumbprint = '',  # optional: adds an https binding with this cert
  [string] $CertStore = 'My'       # LocalMachine store holding it; win-acme uses WebHosting
)

$ErrorActionPreference = 'Stop'
Import-Module WebAdministration

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

# ---- preflight -----------------------------------------------------------------
$principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw 'Run this script from an elevated PowerShell.'
}
if (-not (Test-Path "$env:windir\System32\inetsrv\rewrite.dll")) { throw 'IIS URL Rewrite is not installed.' }
if (-not (Test-Path "$env:ProgramFiles\IIS\Application Request Routing\requestRouter.dll")) {
  throw 'IIS Application Request Routing (ARR) is not installed.'
}
foreach ($exe in 'api.exe', 'ingest.exe', 'worker.exe', 'migrate.exe') {
  if (-not (Test-Path (Join-Path $DistDir $exe))) { throw "Missing $DistDir\$exe. Build first: go build -trimpath -o dist\ ./cmd/..." }
}

$bin = Join-Path $InstallDir 'bin'
$site = Join-Path $InstallDir 'site'
$envFile = Join-Path $bin '.env'
New-Item -ItemType Directory -Force $bin, $site | Out-Null
if (-not (Test-Path $envFile)) {
  throw "Create $envFile first (copy .env.example; set DATABASE_URL, MASTER_KEY, INGEST_BASE_URL=https://$HostName, TRUST_PROXY_HEADERS=true)."
}

# ---- server-wide IIS settings --------------------------------------------------
Step 'ARR proxy'
$apphost = 'MACHINE/WEBROOT/APPHOST'
if (-not (Get-WebConfigurationProperty -PSPath $apphost -Filter system.webServer/proxy -Name enabled).Value) {
  Set-WebConfigurationProperty -PSPath $apphost -Filter system.webServer/proxy -Name enabled -Value $true
  Write-Host '    enabled'
}

Step 'Allow HTTP_X_REAL_IP and HTTP_X_FORWARDED_HOST server variables'
foreach ($var in 'HTTP_X_REAL_IP', 'HTTP_X_FORWARDED_HOST') {
  $allowed = Get-WebConfiguration -PSPath $apphost -Filter 'system.webServer/rewrite/allowedServerVariables/add' |
    Where-Object { $_.name -eq $var }
  if (-not $allowed) {
    Add-WebConfigurationProperty -PSPath $apphost -Filter system.webServer/rewrite/allowedServerVariables -Name '.' -Value @{ name = $var }
  }
}

# ---- services ------------------------------------------------------------------
$services = @(
  @{ Name = 'relaya-ingest'; Exe = 'ingest.exe'; Display = 'Relaya webhook ingest' },
  @{ Name = 'relaya-api';    Exe = 'api.exe';    Display = 'Relaya API' },
  @{ Name = 'relaya-worker'; Exe = 'worker.exe'; Display = 'Relaya delivery worker' }
)

Step 'Stop services for upgrade'
foreach ($s in $services) {
  if (Get-Service $s.Name -ErrorAction SilentlyContinue) { Stop-Service $s.Name -Force }
}

Step "Copy binaries to $bin"
Copy-Item (Join-Path $DistDir '*.exe') $bin -Force
if (Test-Path (Join-Path $WebDist 'index.html')) {
  # Old hashed assets are kept so browser tabs opened before the deploy keep working.
  Copy-Item (Join-Path $WebDist '*') $site -Recurse -Force
  Write-Host "    dashboard copied from $WebDist"
} else {
  Write-Host "    no dashboard build at $WebDist (run npm run build in web\) - skipped" -ForegroundColor Yellow
}
# The dashboard (DASHBOARD_URL) and the API/webhooks (INGEST_BASE_URL) can each have their own
# domain. Every host bound to the site keeps serving ingest and the API, so URLs already given
# out keep working; dashboard pages on any host but DASHBOARD_URL's redirect there.
function EnvHost($name) {
  $line = Get-Content $envFile | Where-Object { $_ -match "^\s*$name\s*=" } | Select-Object -Last 1
  if (-not $line) { return $HostName }
  $u = ($line -replace "^\s*$name\s*=\s*", '').Trim().Trim('"', "'")
  try { return ([Uri]$u).Host } catch { throw "$name in $envFile is not a URL: $u" }
}
$dashHost = EnvHost 'DASHBOARD_URL'
$apiHost = EnvHost 'INGEST_BASE_URL'
$config = Get-Content (Join-Path $PSScriptRoot 'web.config') -Raw
if ($apiHost -eq $dashHost) {
  $config = $config -replace '(?s)\s*<!--API-HOST.*?<!--/API-HOST-->', ''
} else {
  $config = $config.Replace('__API_HOST__', $apiHost).Replace('__API_HOST_RE__', [regex]::Escape($apiHost))
  Write-Host "    API and webhooks at https://$apiHost/v1/..."
}
if ($dashHost -eq $HostName) {
  $config = $config -replace '(?s)\s*<!--DASHBOARD-MOVED.*?<!--/DASHBOARD-MOVED-->', ''
  $wss = "wss://$HostName"
} else {
  $config = $config.Replace('__DASHBOARD_HOST_RE__', [regex]::Escape($dashHost)).Replace('__DASHBOARD_HOST__', $dashHost)
  $wss = "wss://$HostName wss://$dashHost"
  Write-Host "    dashboard at https://$dashHost (its pages on other hosts redirect there)"
}
# The dashboard calls the API's own domain (fetch and the live-update WebSocket).
if ($apiHost -ne $dashHost) { $wss = (@($wss -split ' ') + "https://$apiHost" + "wss://$apiHost" | Select-Object -Unique) -join ' ' }
$config.Replace('wss://__HOST__', $wss) | Set-Content (Join-Path $site 'web.config') -Encoding UTF8 -NoNewline

Step 'Run migrations'
& (Join-Path $bin 'migrate.exe')
if ($LASTEXITCODE -ne 0) { throw 'Migrations failed.' }

Step 'Register services'
foreach ($s in $services) {
  $exe = Join-Path $bin $s.Exe
  if (-not (Get-Service $s.Name -ErrorAction SilentlyContinue)) {
    sc.exe create $s.Name binPath= "`"$exe`"" start= delayed-auto DisplayName= $s.Display | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "sc create $($s.Name) failed" }
  }
  # Run as the service's own virtual account (exists once the service does), not LocalSystem.
  sc.exe config $s.Name obj= "NT SERVICE\$($s.Name)" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "sc config $($s.Name) failed" }
  sc.exe failure $s.Name reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
}

Step 'Lock down permissions'
# Services run as virtual accounts: read-only on bin, and .env readable only by them + admins.
icacls $bin /inheritance:r /grant:r 'Administrators:(OI)(CI)F' 'SYSTEM:(OI)(CI)F' `
  'NT SERVICE\relaya-api:(OI)(CI)RX' 'NT SERVICE\relaya-ingest:(OI)(CI)RX' 'NT SERVICE\relaya-worker:(OI)(CI)RX' | Out-Null
icacls $envFile /inheritance:r /grant:r 'Administrators:F' 'SYSTEM:F' `
  'NT SERVICE\relaya-api:R' 'NT SERVICE\relaya-ingest:R' 'NT SERVICE\relaya-worker:R' | Out-Null

foreach ($s in $services) { Start-Service $s.Name }

Step 'Nightly database backup'
# backup.ps1 and restore-drill.ps1 live next to the services; backups hold customer data,
# so the folder is for admins and SYSTEM only.
Copy-Item (Join-Path $PSScriptRoot 'backup.ps1'), (Join-Path $PSScriptRoot 'restore-drill.ps1') $bin -Force
$backups = Join-Path $InstallDir 'backups'
New-Item -ItemType Directory -Force $backups | Out-Null
icacls $backups /inheritance:r /grant:r 'Administrators:(OI)(CI)F' 'SYSTEM:(OI)(CI)F' | Out-Null
$taskName = 'Relaya database backup'
$action = New-ScheduledTaskAction -Execute 'powershell.exe' `
  -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$(Join-Path $bin 'backup.ps1')`" -InstallDir `"$InstallDir`""
$trigger = New-ScheduledTaskTrigger -Daily -At '02:30'
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit (New-TimeSpan -Hours 2) -RestartCount 2 -RestartInterval (New-TimeSpan -Minutes 15)
Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings `
  -User 'SYSTEM' -RunLevel Highest -Description 'pg_dump of the Relaya database into C:\relaya\backups (keeps 14).' -Force | Out-Null

# ---- IIS site ------------------------------------------------------------------
Step "IIS site $SiteName ($HostName)"
if (-not (Test-Path "IIS:\AppPools\$SiteName")) {
  New-WebAppPool $SiteName | Out-Null
}
Set-ItemProperty "IIS:\AppPools\$SiteName" managedRuntimeVersion ''   # No Managed Code
if (-not (Get-Website $SiteName -ErrorAction SilentlyContinue)) {
  New-Website -Name $SiteName -PhysicalPath $site -ApplicationPool $SiteName -HostHeader $HostName -Port 80 | Out-Null
}
if ($CertThumbprint -and -not (Get-WebBinding -Name $SiteName -Protocol https -HostHeader $HostName)) {
  New-WebBinding -Name $SiteName -Protocol https -Port 443 -HostHeader $HostName -SslFlags 1
  (Get-WebBinding -Name $SiteName -Protocol https -HostHeader $HostName).AddSslCertificate($CertThumbprint, $CertStore)
}
# Plain HTTP for the extra domains (and the dashboard's www), so their certificate can be
# issued and renewed (win-acme adds the https bindings).
$extra = @()
if ($dashHost -ne $HostName) { $extra += $dashHost, "www.$dashHost" }
if ($apiHost -ne $HostName -and $apiHost -ne $dashHost) { $extra += $apiHost }
if ($extra) {
  foreach ($h in $extra) {
    if (-not (Get-WebBinding -Name $SiteName -Protocol http -Port 80 -HostHeader $h)) {
      New-WebBinding -Name $SiteName -Protocol http -Port 80 -HostHeader $h
      Write-Host "    added http binding for $h"
    }
  }
}

Step 'Compress static files on the first request'
# IIS only compresses a file after it is requested twice within 10s by default,
# so first visits got the full ~1 MB bundle. Our assets are cache-busted, so compress always.
Set-WebConfigurationProperty -PSPath $apphost -Location $SiteName `
  -Filter system.webServer/serverRuntime -Name frequentHitThreshold -Value 1

Step 'Keep ingest tokens out of IIS logs'
# Ingest URLs contain the webhook token, so IIS must not log that path.
Set-WebConfigurationProperty -PSPath $apphost -Location "$SiteName/v1/in" `
  -Filter system.webServer/httpLogging -Name dontLog -Value $true

# ---- verify --------------------------------------------------------------------
Step 'Health checks'
Start-Sleep -Seconds 2
$checks = @(
  @{ Name = 'ingest direct'; Url = 'http://127.0.0.1:8081/healthz'; Host = $null },
  @{ Name = 'api direct';    Url = 'http://127.0.0.1:8080/readyz';  Host = $null },
  @{ Name = 'ingest via IIS'; Url = 'http://127.0.0.1/healthz';     Host = $HostName },
  @{ Name = 'api via IIS';    Url = 'http://127.0.0.1/api/readyz';  Host = $HostName }
)
if ($dashHost -ne $HostName) { $checks += @{ Name = 'dashboard API'; Url = 'http://127.0.0.1/api/readyz'; Host = $dashHost } }
if ($apiHost -ne $dashHost) { $checks += @{ Name = 'API domain'; Url = 'http://127.0.0.1/readyz'; Host = $apiHost } }
foreach ($c in $checks) {
  # curl.exe ships with Server 2019+; Invoke-WebRequest in PS 5.1 cannot set the Host header.
  $curlArgs = @('-s', '-o', 'NUL', '-w', '%{http_code}', '--max-time', '5', $c.Url)
  if ($c.Host) { $curlArgs += @('-H', "Host: $($c.Host)") }
  $code = & curl.exe @curlArgs
  $color = if ($code -eq '200') { 'Green' } else { 'Red' }
  Write-Host ("    {0,-15} {1}" -f $c.Name, $code) -ForegroundColor $color
}
$apiURL = if ($apiHost -eq $dashHost) { "https://$apiHost/api/v1/..." } else { "https://$apiHost/v1/..." }
Write-Host "Done. Webhook URLs will look like https://$apiHost/v1/in/<token>; API at $apiURL; dashboard at https://$dashHost/"
