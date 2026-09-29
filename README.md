# Relaya

[![Backend Tests](https://github.com/relayaa/relaya/actions/workflows/backend.yml/badge.svg?branch=main)](https://github.com/relayaa/relaya/actions/workflows/backend.yml)
[![Dashboard Tests](https://github.com/relayaa/relaya/actions/workflows/web.yml/badge.svg?branch=main)](https://github.com/relayaa/relaya/actions/workflows/web.yml)
[![Deployments](https://github.com/relayaa/relaya/actions/workflows/deploy.yml/badge.svg?branch=main)](https://github.com/relayaa/relaya/actions/workflows/deploy.yml)

Relaya is an integration reliability and control platform. It receives webhooks from third-party providers, verifies and stores every event, forwards them to your endpoints with retries, and helps you see and fix what breaks.

## What it does

- **Webhook gateway.** Verifies signatures, removes duplicates and stores every event before returning 200.
- **Event Explorer.** Search, inspect and replay events from the dashboard or the API.
- **Delivery.** Forwards events to your destinations with retries, idempotency keys and SSRF protection.
- **Integration contracts.** Learns each provider's payload shape and flags drift as incidents.
- **Repair rules.** Fixes payloads at send time, so retries and replays get the fix too.
- **Alerts.** Email, Slack, Jira and outbound webhooks.
- **Connections.** OAuth connections for your users, with token refresh, a proxy and syncs.
- **Platform.** Auth and RBAC, a credential vault, an audit log and realtime updates over WebSocket.

## Repository layout

| Path | What |
|---|---|
| [`backend/`](backend/README.md) | Go services: `ingest`, `api`, `worker` and `migrate`, backed by PostgreSQL |
| [`web/`](web/README.md) | Dashboard: React, TypeScript, Vite and Tailwind CSS |
| `sdk/` | Client SDKs (git submodule: [relaya-sdks](https://github.com/relayaa/relaya-sdks)) |
| `.github/workflows/` | CI for the backend |

## Getting started

Clone with the SDK submodule:

```sh
git clone --recurse-submodules https://github.com/relayaa/relaya.git
```

Run the backend (Docker, dev only):

```sh
cd backend
cp .env.example .env            # then set MASTER_KEY: openssl rand -base64 32
docker compose up --build
```

Run the dashboard against it:

```sh
cd web
npm install
API_PROXY_TARGET=http://localhost:8080 npm run dev   # http://localhost:5173
```

## Tests

```sh
cd backend && go test ./...
cd web && npm run lint && npm run build
```

## Documentation

- [Backend](backend/README.md): services, API, providers, delivery, contracts, repair rules, alerts, connections, operations and deployment on Windows Server + IIS.
- [Dashboard](web/README.md): development, build and deployment.
- [Deployment & CI/CD](DEPLOYMENT.md): automated testing, building, and deployment pipeline.

## CI/CD Pipeline

Every push to `main` triggers:
1. **Tests** — Backend and dashboard tests run in parallel
2. **Build** — Go binaries and React distribution are compiled
3. **Deploy to Staging** — Automatically deployed after tests pass
4. **Deploy to Production** — Manual approval required

See [DEPLOYMENT.md](DEPLOYMENT.md) for setup instructions and customization.
