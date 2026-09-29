# Windows VPS Setup Guide - Auto-Deployment

This guide sets up your Windows VPS for automatic deployments from GitHub Actions to IIS.

## Architecture

```
GitHub Repository
    ↓
Developer Push to main
    ↓
GitHub Actions
├─ Run Tests (backend + web)
├─ Build Binaries (api, ingest, worker, migrate)
├─ Build Frontend (React distribution)
└─ Deploy via WinRM/RDP or SCP
    ├─ Copy binaries to C:\relaya\bin\
    ├─ Copy frontend to C:\relaya\site\
    ├─ Run PowerShell deployment script
    └─ IIS serves frontend + proxies API
```

## Prerequisites

- Windows Server 2019 or 2022
- IIS 10+ installed
- PostgreSQL 15+ (local or remote)
- PowerShell 5.1+
- .NET Framework 4.7.2+

## Option 1: Using the Windows Deployment Script (Recommended)

Your backend already has `/backend/deploy/iis/setup.ps1`. This is the best approach.

### Step 1: Prepare Windows Server

```powershell
# Run as Administrator
# Install IIS URL Rewrite and Application Request Routing
Add-WindowsFeature Web-Rewrite
Add-WindowsFeature Web-Arr
```

### Step 2: Create Deployment Directory

```powershell
# Create folder structure
New-Item -ItemType Directory -Path C:\relaya\bin -Force
New-Item -ItemType Directory -Path C:\relaya\site -Force
New-Item -ItemType Directory -Path C:\relaya\backup -Force

# Create .env file with your config
$env_content = @"
DATABASE_URL=Server=localhost;Database=relaya;User Id=sa;Password=YOUR_PASSWORD;
MASTER_KEY=$(openssl rand -base64 32)
APP_ENV=production
INGEST_BASE_URL=https://your-domain.com
LOG_LEVEL=info
RATE_LIMIT_INGEST_PER_SECOND=100
RATE_LIMIT_INGEST_BURST=1000
"@

Set-Content -Path "C:\relaya\.env" -Value $env_content
```

### Step 3: Setup WinRM for GitHub Actions

GitHub Actions will use WinRM (Windows Remote Management) to execute PowerShell on your server.

```powershell
# Run as Administrator on your Windows VPS

# Enable WinRM
Enable-PSRemoting -Force

# Allow GitHub Actions IP (replace with your GitHub Runner IP or use 0.0.0.0)
# In production, restrict this to specific IPs
Set-Item WSMan:\localhost\Client\TrustedHosts -Value "*" -Force

# Create local user for GitHub Actions (or use existing account)
$Username = "github-deploy"
$Password = "SuperSecurePassword123!" | ConvertTo-SecureString -AsPlainText -Force
New-LocalUser -Name $Username -Password $Password -PasswordNeverExpires -AccountNeverExpires
Add-LocalGroupMember -Group "Administrators" -Member $Username

# Verify WinRM is listening
Get-Service WinRM | Set-Service -StartupType Automatic -PassThru | Start-Service
Test-WSMan localhost
```

### Step 4: Add Windows Credentials to GitHub

1. **GitHub Secrets:**
   - `WINDOWS_HOST` = your-vps-ip or hostname
   - `WINDOWS_USER` = `github-deploy`
   - `WINDOWS_PASSWORD` = (the password you created)

2. Or use a certificate-based approach (more secure):

```powershell
# Generate certificate on Windows
$cert = New-SelfSignedCertificate -CertkeyUsage KeyEncipherment,DataEncipherment -KeyUsage KeyAgreement -Subject "CN=GitHubActions" -FriendlyName "GitHub Actions" -CertStoreLocation Cert:\LocalMachine\Root

# Export for GitHub
Export-PfxCertificate -Cert $cert -FilePath ".\github-cert.pfx" -Password $(ConvertTo-SecureString "password" -AsPlainText -Force)
```

### Step 5: Update GitHub Actions Workflow

Modify `.github/workflows/deploy.yml` to use Windows-compatible steps:

```yaml
deploy-prod-windows:
  needs: [build-backend, build-web]
  runs-on: ubuntu-latest  # GitHub Actions runs on Linux
  environment:
    name: production
    url: https://your-domain.com
  steps:
    - uses: actions/checkout@v4
    - name: Download artifacts
      uses: actions/download-artifact@v4
      with:
        path: ./build-artifacts

    - name: Deploy to Windows via WinRM
      uses: GabrielaRidao/deploy-to-windows@v1  # Uses WinRM to connect
      with:
        host: ${{ secrets.WINDOWS_HOST }}
        username: ${{ secrets.WINDOWS_USER }}
        password: ${{ secrets.WINDOWS_PASSWORD }}
        script: |
          # PowerShell script runs on Windows
          
          # Stop services
          Stop-Service -Name "Relaya.API" -ErrorAction SilentlyContinue
          Stop-Service -Name "Relaya.Ingest" -ErrorAction SilentlyContinue
          Stop-Service -Name "Relaya.Worker" -ErrorAction SilentlyContinue
          
          # Backup old binaries
          $timestamp = Get-Date -Format "yyyyMMddHHmmss"
          Copy-Item -Path "C:\relaya\bin\api.exe" -Destination "C:\relaya\backup\api.exe.$timestamp" -ErrorAction SilentlyContinue
          Copy-Item -Path "C:\relaya\bin\ingest.exe" -Destination "C:\relaya\backup\ingest.exe.$timestamp" -ErrorAction SilentlyContinue
          Copy-Item -Path "C:\relaya\bin\worker.exe" -Destination "C:\relaya\backup\worker.exe.$timestamp" -ErrorAction SilentlyContinue
          
          # Copy new binaries
          Copy-Item -Path "build-artifacts\backend-binaries\api" -Destination "C:\relaya\bin\api.exe" -Force
          Copy-Item -Path "build-artifacts\backend-binaries\ingest" -Destination "C:\relaya\bin\ingest.exe" -Force
          Copy-Item -Path "build-artifacts\backend-binaries\worker" -Destination "C:\relaya\bin\worker.exe" -Force
          Copy-Item -Path "build-artifacts\backend-binaries\migrate" -Destination "C:\relaya\bin\migrate.exe" -Force
          
          # Copy frontend
          Remove-Item -Recurse -Force "C:\relaya\site\*" -ErrorAction SilentlyContinue
          Copy-Item -Recurse -Path "build-artifacts\web-dist\*" -Destination "C:\relaya\site\"
          
          # Start services
          Start-Service -Name "Relaya.API"
          Start-Service -Name "Relaya.Ingest"
          Start-Service -Name "Relaya.Worker"
          
          # Wait and verify
          Start-Sleep -Seconds 5
          Get-Service -Name "Relaya.API" | select Status
```

### Step 6: Create Windows Services

Services auto-restart your applications if they crash.

```powershell
# Create service for API
$params = @{
    Name = "Relaya.API"
    BinaryPathName = "C:\relaya\bin\api.exe"
    DisplayName = "Relaya API Server"
    Description = "Relaya Integration API"
    StartupType = "Automatic"
}
New-Service @params

# Create service for Ingest
$params.Name = "Relaya.Ingest"
$params.BinaryPathName = "C:\relaya\bin\ingest.exe"
$params.DisplayName = "Relaya Webhook Ingest"
$params.Description = "Relaya Webhook Gateway"
New-Service @params

# Create service for Worker
$params.Name = "Relaya.Worker"
$params.BinaryPathName = "C:\relaya\bin\worker.exe"
$params.DisplayName = "Relaya Worker"
$params.Description = "Relaya Background Worker (delivery, alerts, syncs)"
New-Service @params

# Start all services
Start-Service -Name "Relaya.API"
Start-Service -Name "Relaya.Ingest"
Start-Service -Name "Relaya.Worker"

# Verify
Get-Service -Name "Relaya.*"
```

### Step 7: Configure IIS for URL Routing

IIS will proxy API calls to your Go services and serve the frontend.

```powershell
# Import IIS module
Import-Module WebAdministration

# Create IIS Site (or use existing)
New-IISSite -Name "Relaya" `
    -BindingInformation "*:80:your-domain.com" `
    -PhysicalPath "C:\relaya\site"

# Add URL Rewrite rules for API proxy
# Note: Requires IIS URL Rewrite and Application Request Routing installed

# The existing setup.ps1 should handle this
# Or manually configure in IIS Manager
```

---

## Option 2: Using SSH with Git for Windows

If your Windows VPS has Git and SSH installed:

```powershell
# On Windows VPS, configure SSH
# Add SSH to PATH if not already there
# Use GitHub's deploy key approach

# Generate SSH key
ssh-keygen -t ed25519 -f C:\Users\Deploy\.ssh\github-deploy -N ""

# Add public key to repo deploy keys
```

---

## Testing the Deployment

### Manual Test

1. **Push test commit:**
   ```bash
   echo "# Test" >> README.md
   git add README.md
   git commit -m "test: windows deployment"
   git push origin main
   ```

2. **Check GitHub Actions:**
   - Watch the deploy step
   - See if WinRM connection succeeds
   - Verify files copied to C:\relaya\

3. **Check Windows Services:**
   ```powershell
   Get-Service -Name "Relaya.*" | Select Name, Status, StartType
   ```

### Verify on Windows

```powershell
# Check if services are running
(Get-Service -Name "Relaya.API").Status  # Should be "Running"

# Test API endpoint
Invoke-WebRequest -Uri "http://localhost:8080/v1/health"

# Check IIS logs
Get-Content "C:\inetpub\logs\LogFiles\W3SVC1\*" -Tail 20

# Check for errors
Get-EventLog -LogName Application -Source "Relaya*" -Newest 10
```

---

## Monitoring on Windows

### View Service Logs

```powershell
# Event Viewer logs
Get-EventLog -LogName Application -Source "Relaya.API" -Newest 50 | Select TimeGenerated, Message

# Or open Event Viewer GUI
eventvwr.msc

# Service status
Get-Service -Name "Relaya.*" | Get-Member
```

### Restart Services

```powershell
# Restart all Relaya services
Restart-Service -Name "Relaya.*"

# Or individual service
Restart-Service -Name "Relaya.API" -Force
```

---

## Troubleshooting Windows

### WinRM Connection Fails

```powershell
# Check WinRM is enabled
Get-Service WinRM | Select Status, StartType

# Enable if needed
Enable-PSRemoting -Force
Start-Service WinRM

# Test connection
Test-WSMan localhost
```

### Services Won't Start

```powershell
# Check application event log
Get-EventLog -LogName Application -Newest 20

# Verify binary exists
ls C:\relaya\bin\

# Test running manually
C:\relaya\bin\api.exe

# Check permissions
Get-Acl C:\relaya\bin
```

### IIS Not Proxying to Backend

```powershell
# Verify services are listening
netstat -ano | findstr "8080"  # API
netstat -ano | findstr "8081"  # Ingest

# Check IIS logs
ls C:\inetpub\logs\LogFiles\

# Verify URL Rewrite rules in IIS Manager
```

---

## Next Steps for Windows VPS

1. Run the WinRM setup above
2. Add GitHub secrets (WINDOWS_HOST, WINDOWS_USER, WINDOWS_PASSWORD)
3. Update `.github/workflows/deploy.yml` with Windows steps
4. Test with a push to `main`
5. Monitor via Event Viewer or PowerShell

Your setup will be: **Any Developer → Push → Auto Build & Deploy to Windows VPS** ✨
