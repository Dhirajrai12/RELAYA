#!/bin/bash
# VPS Deployment Script - Auto-update Relaya services
# Usage: ./vps-deploy.sh <environment> [service]
# Examples:
#   ./vps-deploy.sh staging            (deploy all to staging)
#   ./vps-deploy.sh prod api           (deploy only api to production)

set -e

ENVIRONMENT=${1:-staging}
SERVICE=${2:-all}  # all, api, ingest, worker

BASE_DIR="/opt/relaya"
ENV_DIR="$BASE_DIR/$ENVIRONMENT"
BIN_DIR="$ENV_DIR/bin"
SITE_DIR="$ENV_DIR/site"
BACKUP_DIR="$ENV_DIR/backup"

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}ℹ️  $1${NC}"; }
log_success() { echo -e "${GREEN}✓ $1${NC}"; }
log_warn() { echo -e "${YELLOW}⚠️  $1${NC}"; }

# Verify environment
if [ "$ENVIRONMENT" != "staging" ] && [ "$ENVIRONMENT" != "prod" ]; then
    echo "Error: environment must be 'staging' or 'prod'"
    exit 1
fi

log_info "Deploying to $ENVIRONMENT"

# Check if deployment directory exists
if [ ! -d "$ENV_DIR" ]; then
    log_warn "Directory $ENV_DIR does not exist, creating..."
    sudo mkdir -p "$BIN_DIR" "$SITE_DIR" "$BACKUP_DIR"
    sudo chown -R "$USER:$USER" "$ENV_DIR"
fi

# Function to deploy a service
deploy_service() {
    local svc=$1
    local bin_name=$2

    if [ ! -f "$bin_name" ]; then
        log_warn "Binary not found: $bin_name, skipping $svc"
        return
    fi

    log_info "Deploying $svc..."

    # Backup old binary
    if [ -f "$BIN_DIR/$bin_name" ]; then
        cp "$BIN_DIR/$bin_name" "$BACKUP_DIR/$bin_name.$(date +%s)"
        log_success "Backed up old $bin_name"
    fi

    # Copy new binary
    cp "$bin_name" "$BIN_DIR/"
    chmod +x "$BIN_DIR/$bin_name"

    # Reload systemd and restart service
    sudo systemctl daemon-reload
    if sudo systemctl is-active --quiet "relaya-$svc"; then
        log_info "Stopping relaya-$svc..."
        sudo systemctl stop "relaya-$svc"
    fi

    log_info "Starting relaya-$svc..."
    sudo systemctl start "relaya-$svc"

    # Wait and verify
    sleep 2
    if sudo systemctl is-active --quiet "relaya-$svc"; then
        log_success "$svc is running"
    else
        log_warn "$svc may not have started, check logs: sudo journalctl -u relaya-$svc -n 50"
        return 1
    fi
}

# Deploy binaries
if [ "$SERVICE" = "all" ] || [ "$SERVICE" = "api" ]; then
    deploy_service "api" "api"
fi

if [ "$SERVICE" = "all" ] || [ "$SERVICE" = "ingest" ]; then
    deploy_service "ingest" "ingest"
fi

if [ "$SERVICE" = "all" ] || [ "$SERVICE" = "worker" ]; then
    deploy_service "worker" "worker"
fi

# Deploy frontend if dist/ exists
if [ -d "web/dist" ]; then
    log_info "Deploying frontend..."

    # Backup old site
    if [ -d "$SITE_DIR" ] && [ "$(ls -A $SITE_DIR)" ]; then
        tar czf "$BACKUP_DIR/site.$(date +%s).tar.gz" -C "$ENV_DIR" site/
        log_success "Backed up old site"
    fi

    # Deploy new site
    rm -rf "$SITE_DIR"
    cp -r "web/dist" "$SITE_DIR"
    log_success "Frontend deployed"
fi

# Run migrations
if [ "$ENVIRONMENT" = "prod" ]; then
    log_warn "Manual step required: Run database migrations"
    echo "  \$ $BIN_DIR/migrate -database \$DATABASE_URL"
fi

log_success "Deployment complete!"
log_info "Services: relaya-api, relaya-ingest, relaya-worker"
log_info "Check logs: sudo journalctl -u relaya-api -n 100"
