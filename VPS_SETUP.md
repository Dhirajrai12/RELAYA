# VPS Setup Guide - Auto-Deployment

This guide sets up your VPS for automatic deployments from GitHub Actions. Any developer who pushes to `main` will trigger automatic builds and deployments.

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
└─ SSH into VPS
    ├─ Copy binaries to /opt/relaya/prod/bin/
    ├─ Copy frontend to /opt/relaya/prod/site/
    ├─ Restart services via systemctl
    └─ Frontend served by nginx/apache
```

## Prerequisites

- VPS with Linux (Ubuntu 20.04+ or similar)
- PostgreSQL 15+ running
- SSH access with key-based authentication
- Root or sudo access
- 2+ CPU cores, 2GB+ RAM recommended

## Step 1: Create Deployment User

```bash
# SSH into your VPS
ssh root@your-vps-ip

# Create relaya user for services to run as
sudo useradd -m -s /bin/bash relaya
sudo usermod -aG sudo relaya

# Create deployment directory
sudo mkdir -p /opt/relaya/prod/{bin,site,backup}
sudo mkdir -p /opt/relaya/staging/{bin,site,backup}
sudo chown -R relaya:relaya /opt/relaya

# Create .env file for production
sudo touch /opt/relaya/prod/.env
sudo chown relaya:relaya /opt/relaya/prod/.env
sudo chmod 600 /opt/relaya/prod/.env
```

## Step 2: Configure SSH for GitHub Actions

GitHub Actions will SSH into your VPS to deploy. Use key-based authentication.

### Generate SSH Key (on your VPS)

```bash
# As the relaya user
sudo -u relaya ssh-keygen -t ed25519 -f ~/.ssh/github-actions -N ""

# View the public key
sudo cat /home/relaya/.ssh/github-actions.pub
```

### Add Key to GitHub Actions

1. Go to your GitHub repo → **Settings → Secrets and variables → Actions**
2. Click **New repository secret**
3. Name: `VPS_SSH_KEY`
4. Paste the private key contents:
   ```bash
   sudo cat /home/relaya/.ssh/github-actions
   ```

5. Add deployment secrets:
   - `VPS_HOST`: Your VPS IP or hostname
   - `VPS_USER`: `relaya` (or your deployment user)

## Step 3: Install Systemd Services

Systemd will automatically restart your services if they crash, and GitHub Actions can control them.

```bash
# Copy service files to systemd directory
sudo cp backend/deploy/relaya-*.service /etc/systemd/system/

# Set permissions
sudo chmod 644 /etc/systemd/system/relaya-*.service

# Reload systemd
sudo systemctl daemon-reload

# Enable services to auto-start on boot
sudo systemctl enable relaya-api relaya-ingest relaya-worker

# Verify services are installed
sudo systemctl list-unit-files | grep relaya
```

## Step 4: Configure Environment Variables

```bash
# Edit the .env file with your configuration
sudo nano /opt/relaya/prod/.env
```

Add your environment variables:

```bash
# Database
DATABASE_URL=postgres://relaya:PASSWORD@localhost:5432/relaya?sslmode=disable
MASTER_KEY=$(openssl rand -base64 32)

# Server
APP_ENV=production
INGEST_BASE_URL=https://your-domain.com

# Logging
LOG_LEVEL=info

# Rate limiting
RATE_LIMIT_INGEST_PER_SECOND=100
RATE_LIMIT_INGEST_BURST=1000

# Optional: Alerts, Stripe, etc.
SLACK_WEBHOOK_URL=https://hooks.slack.com/services/...
```

Save with Ctrl+X, then Y, then Enter.

```bash
# Verify permissions
sudo ls -la /opt/relaya/prod/.env
# Should show: -rw------- 1 relaya relaya ...
```

## Step 5: Test SSH from GitHub Actions

Before setting up CI/CD, verify SSH connectivity works:

```bash
# Test SSH login (as relaya user)
ssh -i /home/relaya/.ssh/github-actions relaya@your-vps-ip "whoami"

# Should output: relaya
```

If this fails, check:
- VPS firewall allows port 22
- SSH key permissions: `chmod 700 ~/.ssh && chmod 600 ~/.ssh/github-actions`
- User `relaya` exists and has SSH shell

## Step 6: Setup Web Server (nginx)

Frontend files will be served by nginx. Skip if you're using a different setup.

```bash
# Install nginx
sudo apt update
sudo apt install -y nginx

# Create nginx config for Relaya
sudo tee /etc/nginx/sites-available/relaya > /dev/null <<EOF
server {
    listen 80;
    server_name your-domain.com in.your-domain.com;

    # Redirect HTTP to HTTPS (optional, requires Let's Encrypt)
    # return 301 https://\$server_name\$request_uri;

    # Frontend (React)
    location / {
        root /opt/relaya/prod/site;
        try_files \$uri /index.html;
        add_header Cache-Control "no-cache, must-revalidate";
    }

    # API proxy to backend
    location /api/v1/ {
        proxy_pass http://localhost:8080/v1/;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

    # Webhook ingest gateway
    location /v1/in/ {
        proxy_pass http://localhost:8081/v1/in/;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    }
}
EOF

# Enable the site
sudo ln -sf /etc/nginx/sites-available/relaya /etc/nginx/sites-enabled/

# Remove default site
sudo rm -f /etc/nginx/sites-enabled/default

# Test nginx config
sudo nginx -t

# Restart nginx
sudo systemctl restart nginx
sudo systemctl enable nginx
```

## Step 7: Configure GitHub Secrets

In your GitHub repo settings, add these secrets:

**VPS Deployment:**
- `VPS_HOST` = `your-vps-ip` (e.g., `203.0.113.42`)
- `VPS_USER` = `relaya`
- `VPS_SSH_KEY` = (private key contents)

**Optional - Staging (if using separate staging server):**
- `STAGING_HOST` = `staging-ip`
- `STAGING_USER` = `relaya`
- `STAGING_KEY` = (private key)

## Step 8: Update GitHub Actions Workflow

Edit `.github/workflows/deploy.yml` and update the SSH commands:

```yaml
# Change from:
DEPLOY_HOST: ${{ secrets.STAGING_HOST }}

# To:
DEPLOY_HOST: ${{ secrets.VPS_HOST }}
DEPLOY_USER: ${{ secrets.VPS_USER }}
DEPLOY_KEY: ${{ secrets.VPS_SSH_KEY }}
```

Also update the deployment paths:
```bash
# Instead of /opt/relaya/staging/
scp -i ~/.ssh/deploy_key -r backend/dist/ "$DEPLOY_USER@$DEPLOY_HOST:/opt/relaya/prod/bin/"
scp -i ~/.ssh/deploy_key -r web/dist/ "$DEPLOY_USER@$DEPLOY_HOST:/opt/relaya/prod/site/"
```

## Step 9: Test Deployment

1. Make a small test commit:
   ```bash
   echo "# Test deployment" >> README.md
   git add README.md
   git commit -m "test: ci/cd workflow"
   git push origin main
   ```

2. Watch the GitHub Actions run:
   - Go to **Actions** tab
   - Click the workflow run
   - Watch the `deploy-staging` step
   - Check SSH connection and file copying

3. Verify on VPS:
   ```bash
   ssh relaya@your-vps-ip
   ls -la /opt/relaya/prod/bin/
   ls -la /opt/relaya/prod/site/
   ```

4. Check service status:
   ```bash
   sudo systemctl status relaya-api
   sudo systemctl status relaya-ingest
   sudo systemctl status relaya-worker
   ```

## Step 10: Enable Branch Protection

Require approval before production deployments:

1. **Settings → Branches → main**
2. Enable:
   - ✅ Require pull request reviews (1+ approver)
   - ✅ Require status checks to pass (backend + web tests)
   - ✅ Dismiss stale pull request approvals

This prevents untested code from reaching production.

## Monitoring & Logs

### View Service Logs

```bash
# Real-time logs
sudo journalctl -u relaya-api -f
sudo journalctl -u relaya-ingest -f
sudo journalctl -u relaya-worker -f

# Last 100 lines
sudo journalctl -u relaya-api -n 100

# All today's logs
sudo journalctl -u relaya-api --since today
```

### Check Service Health

```bash
# Service status
sudo systemctl status relaya-api relaya-ingest relaya-worker

# Test API endpoint
curl http://localhost:8080/v1/health

# Test ingest endpoint (should return 401 without token)
curl http://localhost:8081/v1/in/test-token -X POST
```

### View Nginx Logs

```bash
# Access logs
sudo tail -f /var/log/nginx/access.log

# Error logs
sudo tail -f /var/log/nginx/error.log
```

## Troubleshooting

### SSH Connection Fails

```bash
# Test SSH manually
ssh -v -i ~/.ssh/github-actions relaya@your-vps-ip "ls -la /opt/relaya/prod/bin/"

# Common issues:
# - Wrong IP address
# - Firewall blocking port 22
# - Key permissions: sudo chmod 600 ~/.ssh/github-actions
# - User doesn't have SSH shell: sudo usermod -s /bin/bash relaya
```

### Services Won't Start

```bash
# Check service logs
sudo journalctl -u relaya-api -n 50

# Verify permissions
ls -la /opt/relaya/prod/bin/api

# Check if binary exists and is executable
file /opt/relaya/prod/bin/api
```

### Nginx Proxy Not Working

```bash
# Test backend is running
curl http://localhost:8080/v1/health

# Check nginx config
sudo nginx -t

# Check nginx logs
sudo tail -20 /var/log/nginx/error.log
```

### Database Connection Failed

```bash
# Test PostgreSQL connection
psql -h localhost -U relaya -d relaya -c "SELECT 1;"

# Check .env file
cat /opt/relaya/prod/.env | grep DATABASE_URL

# Verify permissions (should be 600)
sudo ls -la /opt/relaya/prod/.env
```

## Automatic Updates

Now that everything is set up, **any developer can trigger deployments:**

1. Developer commits code
2. Pushes to `main`
3. GitHub Actions:
   - Runs tests automatically
   - Builds binaries if tests pass
   - SSHes into your VPS
   - Copies new files
   - Restarts services
4. VPS serves new version

No manual steps needed! ✨

## Rollback Procedure

If something goes wrong:

```bash
# Stop the service
sudo systemctl stop relaya-api

# Restore previous version (backups are in /opt/relaya/prod/backup/)
ls -la /opt/relaya/prod/backup/
cp /opt/relaya/prod/backup/api.1234567890 /opt/relaya/prod/bin/api

# Restart
sudo systemctl start relaya-api

# Verify
sudo systemctl status relaya-api
```

## Next Steps

1. ✅ VPS is ready for automated deployments
2. Test with a real push to `main`
3. Invite developers to the repository
4. Set branch protection rules
5. Monitor deployments in GitHub Actions
6. Celebrate! 🎉

Questions? Check the logs with `sudo journalctl -u relaya-api` or see DEPLOYMENT.md.
