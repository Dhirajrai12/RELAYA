<#
Restore drill: proves the latest backup can actually be restored, and how long
it takes (the target is under an hour). It restores into a temporary database
on the same server, compares row counts with the live database, and drops the
temporary database. The live database is only read.

  powershell -ExecutionPolicy Bypass -File C:\relaya\bin\restore-drill.ps1

Needs the Postgres superuser password from C:\relaya\secrets.txt (read, never printed).

To restore for real (after an incident), stop the services, then:
  pg_restore --clean --if-exists --no-owner --dbname relaya <backup file>
and start the services again.
#>
param(
  [string] $InstallDir = 'C:\relaya',
  [string] $Backup = ''  # default: the newest backup
)

$ErrorActionPreference = 'Stop'
$pg = Join-Path $InstallDir 'pgsql\bin'
$dir = Join-Path $InstallDir 'backups'
$drillDb = 'relaya_restore_drill'
$tables = 'organizations', 'users', 'webhooks', 'destinations', 'events', 'deliveries', 'delivery_attempts',
  'contracts', 'incidents', 'repair_rules', 'alert_channels', 'audit_logs'

if (-not $Backup) {
  $Backup = (Get-ChildItem $dir -Filter 'relaya-*.dump' | Sort-Object Name -Descending | Select-Object -First 1).FullName
}
if (-not $Backup -or -not (Test-Path $Backup)) { throw "No backup found in $dir. Run backup.ps1 first." }
$sumFile = "$Backup.sha256"
if (Test-Path $sumFile) {
  $want = (Get-Content $sumFile).Split(' ')[0]
  $got = (Get-FileHash $Backup -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { throw "Checksum mismatch for ${Backup}: the file is damaged." }
  Write-Host "checksum ok"
}

$line = Get-Content (Join-Path $InstallDir 'secrets.txt') | Where-Object { $_ -like 'postgres superuser password:*' } | Select-Object -First 1
if (-not $line) { throw 'Superuser password not found in secrets.txt' }
$env:PGPASSWORD = ($line -split ':\s*', 2)[1].Trim()
$conn = @('--host', '127.0.0.1', '--port', '5432', '--username', 'postgres', '--no-password')

function Psql($db, $sql) {
  $out = & (Join-Path $pg 'psql.exe') @conn --dbname $db --tuples-only --no-align --quiet --command $sql
  if ($LASTEXITCODE -ne 0) { throw "psql failed: $sql" }
  return $out
}
function Counts($db) {
  $h = [ordered]@{}
  foreach ($t in $tables) { $h[$t] = [int64](Psql $db "SELECT count(*) FROM $t") }
  return $h
}

try {
  Write-Host "backup: $(Split-Path $Backup -Leaf) ($([math]::Round((Get-Item $Backup).Length / 1MB, 1)) MB)"
  Psql 'postgres' "DROP DATABASE IF EXISTS $drillDb" | Out-Null
  $started = Get-Date
  Psql 'postgres' "CREATE DATABASE $drillDb" | Out-Null
  & (Join-Path $pg 'pg_restore.exe') @conn --dbname $drillDb --no-owner --exit-on-error --jobs 4 $Backup
  if ($LASTEXITCODE -ne 0) { throw "pg_restore exited with $LASTEXITCODE" }
  $took = (Get-Date) - $started

  # Every table must exist and be readable (Counts throws otherwise). Counts
  # differ from live by whatever changed since the backup was taken.
  $restored = Counts $drillDb
  $live = Counts 'relaya'
  foreach ($t in $tables) {
    $note = if ($restored[$t] -eq $live[$t]) { 'same' } else { 'changed since the backup' }
    '{0,-18} restored {1,8}   live {2,8}   {3}' -f $t, $restored[$t], $live[$t], $note | Write-Host
  }
  $migrations = Psql $drillDb 'SELECT max(name) FROM schema_migrations'
  Write-Host ("latest migration {0}; restore took {1:N1}s (target: under 1 hour)" -f $migrations, $took.TotalSeconds)
  if ($took.TotalHours -ge 1) { throw 'Restore took over an hour.' }
  Write-Host 'RESTORE DRILL PASSED' -ForegroundColor Green
} finally {
  Psql 'postgres' "DROP DATABASE IF EXISTS $drillDb" | Out-Null
  Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue
}
