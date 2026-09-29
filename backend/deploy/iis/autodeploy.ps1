<#
.SYNOPSIS
  Pull-based auto-deploy for the Windows/IIS server. Run by the "Relaya auto-deploy"
  scheduled task (install-autodeploy.ps1) as SYSTEM every few minutes.

.DESCRIPTION
  1. Stops at once unless <InstallDir>\autodeploy.enabled exists (the off switch).
  2. Reads the GitHub release "production" (published by .github/workflows/deploy.yml
     after the tests pass). Nothing to do if its commit is already deployed.
  3. Checks the commit is on main, downloads the package, and verifies its SHA-256.
  4. Deploys it with the package's own setup.ps1 (services, migrations, IIS, dashboard).
  5. Checks https://<HostName>/api/healthz. If it fails, redeploys the previous package.
  Everything is logged to <InstallDir>\logs\autodeploy.log.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)] [string] $HostName,
  [string] $Repo = 'Dhirajrai12/RELAYA',
  [string] $InstallDir = 'C:\relaya',
  [string] $CertThumbprint = '',
  [string] $CertStore = 'My',
  [int] $KeepReleases = 5
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$logDir = Join-Path $InstallDir 'logs'
$releases = Join-Path $InstallDir 'releases'
$state = Join-Path $InstallDir 'deployed-sha.txt'
New-Item -ItemType Directory -Force $logDir, $releases | Out-Null
$log = Join-Path $logDir 'autodeploy.log'
function Log($msg) { "$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')  $msg" | Add-Content -Path $log -Encoding UTF8 }

if (-not (Test-Path (Join-Path $InstallDir 'autodeploy.enabled'))) { exit 0 }

# One run at a time.
$lock = Join-Path $InstallDir 'autodeploy.lock'
try { $lockStream = [IO.File]::Open($lock, 'OpenOrCreate', 'ReadWrite', 'None') } catch { exit 0 }

try {
  $headers = @{ 'User-Agent' = 'relaya-autodeploy'; 'Accept' = 'application/vnd.github+json' }
  try {
    $rel = Invoke-RestMethod -Headers $headers -Uri "https://api.github.com/repos/$Repo/releases/tags/production"
  } catch {
    if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 404) { exit 0 } # nothing published yet
    throw
  }
  $asset = { param($pattern) $rel.assets | Where-Object { $_.name -like $pattern } | Select-Object -First 1 }
  $manifestAsset = & $asset 'manifest.json'
  $zipAsset = & $asset 'relaya-*.zip'
  $sumAsset = & $asset 'relaya-*.zip.sha256'
  if (-not ($manifestAsset -and $zipAsset -and $sumAsset)) { Log 'production release is incomplete; skipping'; exit 0 }

  # Release files come as application/octet-stream; save them and read the text.
  function Fetch($u) {
    $tmp = [IO.Path]::GetTempFileName()
    try { Invoke-WebRequest -UseBasicParsing -Headers $headers -Uri $u -OutFile $tmp; (Get-Content $tmp -Raw).Trim() } finally { Remove-Item $tmp -ErrorAction SilentlyContinue }
  }
  $manifest = Fetch $manifestAsset.browser_download_url | ConvertFrom-Json
  $sha = [string]$manifest.sha
  if ($sha -notmatch '^[0-9a-f]{40}$') { throw "bad commit in manifest: $sha" }
  $current = if (Test-Path $state) { (Get-Content $state -Raw).Trim() } else { '' }
  if ($sha -eq $current) { exit 0 }
  $failed = Join-Path $InstallDir 'autodeploy.failed-sha.txt'
  if ((Test-Path $failed) -and (Get-Content $failed -Raw).Trim() -eq $sha) { exit 0 } # already failed; wait for the next push

  # Only builds of commits on main (a release can't smuggle in another branch).
  $cmp = Invoke-RestMethod -Headers $headers -Uri "https://api.github.com/repos/$Repo/compare/$sha...main"
  if ($cmp.status -notin @('identical', 'ahead')) { throw "commit $sha is not on main ($($cmp.status))" }

  $short = $sha.Substring(0, 7)
  Log "new build $short (deployed: $(if ($current) { $current.Substring(0, 7) } else { 'none' }))"
  $dir = Join-Path $releases $sha
  $zip = Join-Path $dir $zipAsset.name
  New-Item -ItemType Directory -Force $dir | Out-Null
  Invoke-WebRequest -UseBasicParsing -Headers $headers -Uri $zipAsset.browser_download_url -OutFile $zip
  $want = (Fetch $sumAsset.browser_download_url).Split()[0].ToLower()
  $got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
  if ($got -ne $want) { throw "checksum mismatch for $($zipAsset.name): got $got, want $want" }

  $pkg = Join-Path $dir 'pkg'
  if (Test-Path $pkg) { Remove-Item -Recurse -Force $pkg }
  Expand-Archive -Path $zip -DestinationPath $pkg

  function Deploy($pkgDir) {
    & (Join-Path $pkgDir 'deploy\iis\setup.ps1') -HostName $HostName -InstallDir $InstallDir `
      -DistDir (Join-Path $pkgDir 'dist') -WebDist (Join-Path $pkgDir 'web') -CertThumbprint $CertThumbprint -CertStore $CertStore *>> $log
  }
  function Healthy {
    for ($i = 0; $i -lt 20; $i++) {
      try {
        # /api/healthz is the api service, /healthz is ingest.
        $a = Invoke-WebRequest -UseBasicParsing -Uri "https://$HostName/api/healthz" -TimeoutSec 5
        $b = Invoke-WebRequest -UseBasicParsing -Uri "https://$HostName/healthz" -TimeoutSec 5
        if ($a.StatusCode -eq 200 -and $b.StatusCode -eq 200) { return $true }
      } catch { }
      Start-Sleep -Seconds 3
    }
    return $false
  }

  Log "deploying $short"
  $ok = $true
  try { Deploy $pkg } catch { $ok = $false; Log "deploy failed: $($_.Exception.Message)" }
  if ($ok) { $ok = Healthy }

  if ($ok) {
    Set-Content -Path $state -Value $sha -Encoding ASCII
    Log "deployed $short; healthy"
    # Keep the last few packages for rollback.
    Get-ChildItem $releases -Directory | Sort-Object LastWriteTime -Descending | Select-Object -Skip $KeepReleases |
      Where-Object { $_.Name -ne $sha -and $_.Name -ne $current } | Remove-Item -Recurse -Force
  } else {
    Log "build $short is unhealthy; rolling back"
    $prev = if ($current) { Join-Path (Join-Path $releases $current) 'pkg' } else { '' }
    if ($prev -and (Test-Path $prev)) {
      try { Deploy $prev; Log "rolled back to $($current.Substring(0, 7)); healthy: $(Healthy)" } catch { Log "ROLLBACK FAILED: $($_.Exception.Message)" }
    } else {
      Log 'no previous package to roll back to; fix forward or deploy by hand'
    }
    # Don't retry the same broken build every few minutes.
    Set-Content -Path $failed -Value $sha -Encoding ASCII
  }
} catch {
  Log "error: $($_.Exception.Message)"
  exit 1
} finally {
  if ($lockStream) { $lockStream.Close() }
}
