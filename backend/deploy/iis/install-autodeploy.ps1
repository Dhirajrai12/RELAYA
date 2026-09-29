<#
.SYNOPSIS
  One-time setup of automatic deploys on the Windows/IIS server. Run elevated.

.DESCRIPTION
  Copies autodeploy.ps1 to <InstallDir>\bin and registers the scheduled task
  "Relaya auto-deploy" (runs as SYSTEM every 5 minutes), then turns it on by creating
  <InstallDir>\autodeploy.enabled.

  Pause deploys:   Remove-Item C:\relaya\autodeploy.enabled
  Resume deploys:  New-Item C:\relaya\autodeploy.enabled
  Remove:          .\install-autodeploy.ps1 -Uninstall
  Log:             C:\relaya\logs\autodeploy.log

.EXAMPLE
  .\install-autodeploy.ps1 -HostName server.aegonassett.com -CertThumbprint <thumbprint> -CertStore WebHosting
#>
[CmdletBinding()]
param(
  [string] $HostName,
  [string] $InstallDir = 'C:\relaya',
  [string] $Repo = 'relayaa/RELAYA',
  [string] $CertThumbprint = '',
  [string] $CertStore = 'My',
  [int] $EveryMinutes = 5,
  [switch] $Uninstall
)

$ErrorActionPreference = 'Stop'
$task = 'Relaya auto-deploy'

$admin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $admin) { throw 'Run this from an elevated (Administrator) PowerShell.' }

if ($Uninstall) {
  Unregister-ScheduledTask -TaskName $task -Confirm:$false -ErrorAction SilentlyContinue
  Remove-Item (Join-Path $InstallDir 'autodeploy.enabled') -ErrorAction SilentlyContinue
  Write-Host "Removed the '$task' task. Deployed files are left as they are."
  exit 0
}
if (-not $HostName) { throw '-HostName is required (e.g. server.aegonassett.com).' }

$bin = Join-Path $InstallDir 'bin'
New-Item -ItemType Directory -Force $bin | Out-Null
$script = Join-Path $bin 'autodeploy.ps1'
Copy-Item (Join-Path $PSScriptRoot 'autodeploy.ps1') $script -Force

$taskArgs = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `"$script`" -HostName `"$HostName`" -Repo `"$Repo`" -InstallDir `"$InstallDir`" -CertStore `"$CertStore`""
if ($CertThumbprint) { $taskArgs += " -CertThumbprint `"$CertThumbprint`"" }

$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $taskArgs
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) -RepetitionInterval (New-TimeSpan -Minutes $EveryMinutes)
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 30) -StartWhenAvailable
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Principal $principal -Force | Out-Null

New-Item -ItemType File -Force (Join-Path $InstallDir 'autodeploy.enabled') | Out-Null
Write-Host "Installed '$task': checks https://github.com/$Repo/releases/tag/production every $EveryMinutes minutes."
Write-Host "Log: $(Join-Path $InstallDir 'logs\autodeploy.log')"
