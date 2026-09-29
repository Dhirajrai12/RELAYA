# Local Build on Your Server

Since you're running on the server directly, just pull code and build locally.

## Setup (One time)

```bash
# On your server
cd /opt/relaya/source  # or wherever you cloned the repo

# Install Go and Node.js if not already installed
```

## Build & Deploy Workflow

### 1. Pull Latest Code
```bash
cd /opt/relaya/source
git pull origin main
```

### 2. Build Backend
```bash
cd backend
go build -o dist/api ./cmd/api
go build -o dist/ingest ./cmd/ingest
go build -o dist/worker ./cmd/worker
go build -o dist/migrate ./cmd/migrate
```

### 3. Build Frontend
```bash
cd ../web
npm install
npm run build
# Output: web/dist/
```

### 4. Stop Old Services
```bash
sudo systemctl stop relaya-api relaya-ingest relaya-worker
```

### 5. Copy New Files
```bash
cp backend/dist/* /opt/relaya/bin/
cp -r web/dist/* /opt/relaya/site/
```

### 6. Restart Services
```bash
sudo systemctl start relaya-api relaya-ingest relaya-worker
```

### 7. Verify
```bash
sudo systemctl status relaya-api relaya-ingest relaya-worker
curl http://localhost:8080/v1/health
```

## Automated Local Deployment (Optional)

Create a script to auto-pull and build:

```bash
#!/bin/bash
cd /opt/relaya/source
git pull origin main
cd backend && go build -o dist/api ./cmd/api && go build -o dist/ingest ./cmd/ingest && go build -o dist/worker ./cmd/worker
cd ../web && npm install && npm run build
sudo systemctl stop relaya-api relaya-ingest relaya-worker
cp backend/dist/* /opt/relaya/bin/
cp -r web/dist/* /opt/relaya/site/
sudo systemctl start relaya-api relaya-ingest relaya-worker
```

Save as `/opt/relaya/deploy.sh` and run manually when needed.
