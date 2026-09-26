<#
Backs up the Relaya database with pg_dump (custom format, compressed) and keeps
the newest $Keep backups. setup.ps1 schedules it nightly as the "Relaya database
backup" task; run it by hand any time:

  powershell -ExecutionPolicy Bypass -File C:\relaya\bin\backup.ps1

Backups on this disk protect against mistakes and bad deploys, not against
losing the machine: copy C:\relaya\backups somewhere else too (S3, another server).
#>
param(
  [string] $InstallDir = 'C:\relaya',
  [int] $Keep = 14
)

$ErrorActionPreference = 'Stop'
$bin = Join-Path $InstallDir 'bin'
$dir = Join-Path $InstallDir 'backups'
$pgDump = Join-Path $InstallDir 'pgsql\bin\pg_dump.exe'
$log = Join-Path $dir 'backup.log'
New-Item -ItemType Directory -Force $dir | Out-Null

function Log($msg) {
  $line = "{0:u} {1}" -f (Get-Date), $msg
  Add-Content -Path $log -Value $line
  Write-Host $line
}

try {
  # DATABASE_URL from the services' .env (never printed).
  $url = (Get-Content (Join-Path $bin '.env') | Where-Object { $_ -match '^DATABASE_URL=' } | Select-Object -First 1) -replace '^DATABASE_URL=', ''
  if (-not $url) { throw "DATABASE_URL not found in $bin\.env" }
  $u = [Uri]$url
  $user, $pass = $u.UserInfo.Split(':', 2)
  $dbName = $u.AbsolutePath.TrimStart('/')

  $file = Join-Path $dir ("relaya-{0:yyyyMMdd-HHmmss}.dump" -f (Get-Date))
  $partial = "$file.partial"
  $started = Get-Date
  # Password via the environment, so it never appears in the process list.
  $env:PGPASSWORD = [Uri]::UnescapeDataString($pass)
  try {
    & $pgDump --host $u.Host --port $u.Port --username ([Uri]::UnescapeDataString($user)) --dbname $dbName `
      --format custom --compress 6 --no-owner --file $partial
    if ($LASTEXITCODE -ne 0) { throw "pg_dump exited with $LASTEXITCODE" }
  } finally {
    Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
  }
  Move-Item $partial $file
  $size = (Get-Item $file).Length
  if ($size -lt 1024) { throw "backup is suspiciously small ($size bytes)" }
  $hash = (Get-FileHash $file -Algorithm SHA256).Hash.ToLower()
  Set-Content -Path "$file.sha256" -Value "$hash  $(Split-Path $file -Leaf)" -Encoding ascii
  Log ("ok {0} {1:N1} MB in {2:N1}s" -f (Split-Path $file -Leaf), ($size / 1MB), ((Get-Date) - $started).TotalSeconds)

  # Tell monitoring (windows_exporter's textfile collector), if it is installed.
  $textfile = Join-Path $InstallDir 'monitoring\textfile'
  if (Test-Path $textfile) {
    $prom = @(
      '# HELP relaya_backup_last_success_timestamp_seconds When the last database backup succeeded.'
      '# TYPE relaya_backup_last_success_timestamp_seconds gauge'
      "relaya_backup_last_success_timestamp_seconds $([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())"
      '# HELP relaya_backup_size_bytes Size of the last database backup.'
      '# TYPE relaya_backup_size_bytes gauge'
      "relaya_backup_size_bytes $size"
    ) -join "`n"
    # Write then rename, so the exporter never reads a half-written file.
    [IO.File]::WriteAllText((Join-Path $textfile 'relaya_backup.prom.tmp'), "$prom`n")
    Move-Item (Join-Path $textfile 'relaya_backup.prom.tmp') (Join-Path $textfile 'relaya_backup.prom') -Force
  }

  # Keep the newest $Keep.
  Get-ChildItem $dir -Filter 'relaya-*.dump' | Sort-Object Name -Descending | Select-Object -Skip $Keep | ForEach-Object {
    Remove-Item $_.FullName, "$($_.FullName).sha256" -ErrorAction SilentlyContinue
    Log "removed old backup $($_.Name)"
  }
  Get-ChildItem $dir -Filter '*.partial' | Remove-Item -ErrorAction SilentlyContinue
} catch {
  Log "FAILED: $($_.Exception.Message)"
  exit 1
}
