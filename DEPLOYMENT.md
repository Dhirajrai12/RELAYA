# Deployment & CI/CD Pipeline

Relaya uses GitHub Actions for continuous integration and deployment. The pipeline automatically tests, builds, and deploys changes to staging and production.

## Pipeline Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Developer Push to main                    │
└────────────────┬────────────────────────────────────────────┘
                 │
        ┌────────▼────────┬─────────────────┐
        │                 │                 │
    ┌───▼────┐      ┌────▼──┐       ┌──────▼────┐
    │Backend │      │ Web   │       │  Deploy   │
    │ Tests  │      │ Tests │       │   Tests   │
    └───┬────┘      └────┬──┘       └──────┬────┘
        │                │                │
        └────────────────┼────────────────┘
                         │
        ┌────────────────▼────────────────┐
        │    Build Backend Binaries       │
        │    Build Web Distribution       │
        └────────────────┬────────────────┘
                         │
        ┌────────────────▼────────────────┐
        │   Auto-Deploy to Staging        │
        │  (runs on every main push)      │
        └────────────────┬────────────────┘
                         │
        ┌────────────────▼────────────────┐
        │  ⚠️  Requires Manual Approval    │
        │   Deploy to Production          │
        │  (on main, after staging OK)    │
        └────────────────┬────────────────┘
                         │
        ┌────────────────▼────────────────┐
        │   Create GitHub Release         │
        │   & Notify Team                 │
        └────────────────────────────────┘
```

## Workflows

### 1. **backend.yml** — Backend Tests
- **Trigger:** Push or PR to `backend/**`
- **Steps:**
  - `gofmt` check (code formatting)
  - `go vet` (static analysis)
  - `go test -race ./...` (unit tests with race detector)
  - Docker build (ensures Dockerfile is valid)

### 2. **web.yml** — Dashboard Tests
- **Trigger:** Push or PR to `web/**`
- **Steps:**
  - `npm ci` (install dependencies)
  - `tsc -b` (TypeScript type check)
  - `oxlint` (linting)
  - `npm run build` (build production bundle)

### 3. **deploy.yml** — Build & Deploy
- **Trigger:** Push to `main` (after tests pass)
- **Steps:**

#### Build Stage
- **build-backend**
  - Compiles Go binaries: `api`, `ingest`, `worker`, `migrate`
  - Uploads to artifact store (5-day retention)

- **build-web**
  - Builds React distribution (`web/dist`)
  - Uploads to artifact store (5-day retention)

#### Deployment Stage
- **deploy-staging** (automatic)
  - Downloads artifacts
  - Deploys via SSH to staging server
  - Restarts services
  - Updates deployment status

- **deploy-production** (manual approval required)
  - Downloads artifacts
  - Requires environment approval in GitHub
  - Deploys to production servers
  - Creates a GitHub Release
  - Notifies team

## Setup Instructions

### 1. GitHub Secrets Required

Add these secrets to your repository settings (`Settings → Secrets and variables → Actions`):

**Staging Deployment:**
- `STAGING_HOST` — staging server hostname
- `STAGING_USER` — SSH user (e.g., `deploy`)
- `STAGING_KEY` — Private SSH key for authentication

**Production Deployment:**
- `PROD_HOST` — production server hostname
- `PROD_USER` — SSH user
- `PROD_PASSWORD` — Password or key (for Windows WinRM)

### 2. Environment Protection (Production)

1. Go to `Settings → Environments`
2. Create/edit `production` environment
3. Add required reviewers (team members who approve production deploys)
4. Set deployment branch restrictions to `main`

### 3. Server Setup

#### Staging Server
```bash
# Create deployment directories
sudo mkdir -p /opt/relaya/staging/{bin,site}
sudo chown deploy:deploy /opt/relaya/staging

# Enable SSH key authentication
echo "ssh-rsa AAAA..." >> ~/.ssh/authorized_keys
```

#### Production Server (Windows IIS)
```powershell
# Create directories
New-Item -ItemType Directory -Path C:\relaya\staging -Force
New-Item -ItemType Directory -Path C:\relaya\site -Force

# Or use the existing deploy script:
# .\backend\deploy\iis\setup.ps1 -HostName in.example.com
```

## Deployment Flow

### On Every `main` Push:

1. **Tests Run** (parallel)
   - Backend: `go vet`, `go test`, Docker build
   - Web: TypeScript, linting, build

2. **If Tests Pass** (automatic):
   - Build backend binaries
   - Build web distribution
   - Deploy to staging
   - Restart staging services

3. **Production Deployment** (manual approval):
   - GitHub notifies reviewers
   - Team approves in GitHub UI
   - Deploy to production
   - Create release on GitHub

### Monitoring Deployments

#### View in GitHub UI:
- **Actions tab** → See workflow runs and logs
- **Environments** → See deployment history and approvals
- **Releases** → See production releases

#### Example Workflow Run:
```
✓ backend (5m) — All tests passed
✓ web (3m) — All tests passed
✓ build-backend (1m) — Binaries built
✓ build-web (1m) — Distribution built
✓ deploy-staging (2m) — Deployed to staging
⏳ deploy-production — Awaiting approval
```

## Rollback Procedure

If a production deployment goes wrong:

1. **Immediate Action:**
   - GitHub workflow can be cancelled mid-deployment
   - Revert the commit: `git revert <commit> && git push origin main`
   - Previous release will be redeployed

2. **Restore Previous Version:**
   ```bash
   # SSH to production server
   ssh deploy@prod.example.com
   
   # Restore from previous release
   git checkout v123  # tag from previous release
   systemctl restart relaya-api relaya-ingest relaya-worker
   ```

3. **Post-Mortem:**
   - Review logs in GitHub Actions
   - Add tests to catch the issue
   - Deploy the fix

## Performance

### Build Times (Typical)
- Backend tests: 2–3 minutes
- Web tests: 2–3 minutes
- Backend build: 1 minute
- Web build: 1–2 minutes
- Staging deploy: 1–2 minutes
- Total: ~7–10 minutes from push to staging

### Artifact Storage
- Artifacts retained for 5 days
- ~50 MB backend binaries
- ~2 MB web distribution (gzipped)

## Customization

### Modify Deployment Targets

Edit `.github/workflows/deploy.yml`:

- Change `STAGING_HOST` / `PROD_HOST` to your servers
- Modify SSH commands for your infrastructure
- Add additional environments (e.g., `preview`, `canary`)

### Add Deployment Steps

Common additions:

```yaml
# Run database migrations
- name: Migrate database
  run: |
    ssh -i ~/.ssh/deploy_key "$DEPLOY_USER@$DEPLOY_HOST" \
      "/opt/relaya/bin/migrate -database $DATABASE_URL"

# Run smoke tests
- name: Smoke tests
  run: |
    curl -f https://staging.relaya.example.com/health

# Notify Slack
- name: Notify Slack
  uses: slackapi/slack-github-action@v1
  with:
    payload: |
      {
        "text": "Deployment complete: ${{ job.status }}"
      }
```

### Add Manual Approval to Staging

To require approval even for staging:

```yaml
deploy-staging:
  needs: [build-backend, build-web]
  environment:
    name: staging
    url: https://staging.relaya.example.com
  # Approval will be required
```

## Troubleshooting

### Workflow Fails at Build Stage

**Backend:**
```bash
cd backend && go build ./cmd/...
```

**Web:**
```bash
cd web && npm ci && npm run build
```

### Deployment Fails

Check SSH connectivity:
```bash
ssh -i ~/.ssh/deploy_key deploy@staging.example.com "echo OK"
```

### Services Don't Restart

Verify systemd service files on the server:
```bash
systemctl status relaya-api
systemctl status relaya-ingest
systemctl status relaya-worker
```

Or for Windows IIS, check PowerShell script:
```powershell
.\backend\deploy\iis\setup.ps1 -HostName in.example.com
```

## Security

- **Secrets**: Never commit SSH keys or passwords; use GitHub Secrets
- **Approvals**: Production deployments require manual review
- **Artifacts**: Automatically deleted after 5 days
- **Logs**: GitHub Actions logs may contain secrets—review before sharing
- **SSH**: Use strong key-based authentication, disable password login

## Next Steps

1. **Add the secrets** to GitHub repository
2. **Set up environments** in GitHub (Settings → Environments)
3. **Configure servers** with SSH access for the deploy user
4. **Test a deployment** by pushing a non-critical change to `main`
5. **Monitor the workflow** in the Actions tab
6. **Approve the production deployment** when ready

Your CI/CD pipeline is now ready for production deployments! 🚀
