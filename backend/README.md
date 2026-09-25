# Backend

Go services for the Integration Reliability & Control Platform (working name "relaya"; final name TBD).

Month 1 scope: webhook gateway ingest, event store, Explorer API v0, auth + RBAC, credential vault, audit log.

## Services

| Binary | Port | Job |
|---|---|---|
| `cmd/ingest` | 8081 | `POST /v1/in/{token}`: verify signature, dedup, store, return 200. Kept tiny and separate so ingest stays up if the API goes down. |
| `cmd/api` | 8080 | REST API for the dashboard and customers. |
| `cmd/migrate` | n/a | Applies SQL migrations in `internal/db/migrations`, then exits. |

## Run locally

With Docker (dev only; production on Windows uses IIS, below):

```sh
cp .env.example .env            # then set MASTER_KEY: openssl rand -base64 32
docker compose up --build
```

Without Docker (Postgres running locally):

```sh
export DATABASE_URL=postgres://relaya:relaya@localhost:5432/relaya?sslmode=disable
export MASTER_KEY=$(openssl rand -base64 32)
go run ./cmd/migrate
go run ./cmd/api &
go run ./cmd/ingest &
```

## Deploy on Windows Server + IIS

IIS is the reverse proxy (URL Rewrite + ARR); the Go binaries run as Windows services.

| Public URL | Proxied to |
|---|---|
| `https://<host>/v1/in/<token>` | ingest `127.0.0.1:8081` |
| `https://<host>/api/v1/...` | api `127.0.0.1:8080/v1/...` |
| `https://<host>/healthz` | ingest health |

1. Install PostgreSQL 17 as a service and create a `relaya` database and user.
2. Create `C:\relaya\bin\.env` from `.env.example` with `INGEST_BASE_URL=https://<host>` and `TRUST_PROXY_HEADERS=true`.
3. From an elevated PowerShell in `backend\`:

   ```powershell
   go build -trimpath -o dist\ ./cmd/...
   .\deploy\iis\setup.ps1 -HostName in.example.com [-CertThumbprint <thumbprint>]
   ```

The script is idempotent: re-run it to upgrade (it stops services, copies binaries, migrates, restarts). It:

- registers `relaya-api` and `relaya-ingest` as delayed-auto services running under their own virtual accounts, restarting on failure;
- locks `bin\` and `.env` down to those accounts and Administrators;
- creates the IIS site and app pool (No Managed Code) from [deploy/iis/web.config](deploy/iis/web.config);
- turns off IIS logging for `/v1/in` so webhook tokens never land in IIS logs;
- server-wide, additive only: enables the ARR proxy if off, and allows the `HTTP_X_REAL_IP` server variable.

Notes:

- IIS overwrites `X-Real-IP` with the real client address, so it can't be spoofed. ARR's `X-Forwarded-For` (which includes the port) is only a fallback, and only its last entry is trusted.
- There is no HTTP→HTTPS redirect for ingest: many webhook senders drop the POST body on a 301. Give providers the `https://` URL.
- `.env` is read from next to the executable (or `ENV_FILE`), because services start in `System32` with no shell environment. Real environment variables override it.
- Rate limiting isn't configured yet. Add IIS Dynamic IP Restrictions before public launch.

## Tests

```sh
go test ./...                                   # unit tests
TEST_DATABASE_URL=postgres://.../relaya_test go test ./internal/e2e   # full flow; wipes that DB
```

## Try it

```sh
API=http://localhost:8080
TOKEN=$(curl -s $API/v1/auth/signup -d '{"email":"you@example.com","password":"a-long-password","org_name":"Acme"}' | jq -r .token)
ORG=$(curl -s $API/v1/me -H "Authorization: Bearer $TOKEN" | jq -r '.orgs[0].id')
PROJECT=$(curl -s $API/v1/orgs/$ORG/projects -H "Authorization: Bearer $TOKEN" -d '{"name":"Payments"}' | jq -r .id)
URL=$(curl -s $API/v1/orgs/$ORG/webhooks -H "Authorization: Bearer $TOKEN" \
  -d "{\"project_id\":\"$PROJECT\",\"name\":\"Test\",\"provider\":\"generic\"}" | jq -r .ingest_url)

curl -s $URL -H 'Content-Type: application/json' -d '{"type":"order.created","id":"o_1"}'
curl -s "$API/v1/orgs/$ORG/events" -H "Authorization: Bearer $TOKEN" | jq
```

## API (v1)

Auth: `Authorization: Bearer <token>`: a session token (`rs_…`, from signup/login) or an API key (`rk_…`).

| Method & path | Min role |
|---|---|
| `POST /v1/auth/signup`, `POST /v1/auth/login` | none |
| `POST /v1/auth/logout`, `GET /v1/me`, `GET /v1/providers` | any |
| `GET/POST /v1/orgs`, `GET /v1/orgs/{org}` | member |
| `GET /v1/orgs/{org}/members` | member |
| `POST /v1/orgs/{org}/members`, `PATCH`/`DELETE …/members/{user}` | admin (owners only for owner changes) |
| `GET/POST /v1/orgs/{org}/api-keys`, `DELETE …/api-keys/{key}` | admin |
| `GET /v1/orgs/{org}/projects[/{project}]` | member |
| `POST /v1/orgs/{org}/projects` | admin |
| `DELETE /v1/orgs/{org}/projects/{project}` | owner |
| `GET /v1/orgs/{org}/webhooks[/{webhook}]` | member |
| `POST`, `PATCH`, `DELETE …/webhooks[/{webhook}]`, `POST …/rotate-url` | admin |
| `GET /v1/orgs/{org}/events` (filters: `project_id`, `webhook_id`, `type`, `status`, `signature`, `dedup_key`, `since`, `until`, `limit`, `cursor`) | member |
| `GET /v1/orgs/{org}/events/{event}` (masked payload) | member |
| `GET /v1/orgs/{org}/audit-logs?before=` | admin |

Callers without access to an org get 404, not 403, so org IDs can't be probed.

## Webhook providers

`generic` (hex HMAC-SHA256 in a configurable header, default `X-Signature`), `razorpay`, `stripe` (with 5-minute timestamp tolerance), `shopify`, `github`.
Add one in `internal/provider`: implement `Verify`, `DedupKey`, `EventType` and register it.

Ingest outcomes:

- Valid signature, or no secret configured: stored as `received`, `200 {"id", "duplicate"}`.
- Same dedup key again (per webhook, 30-day window): `200` with the original ID; nothing new stored.
- Bad or missing signature: stored as `rejected` (evidence for the Explorer), `401`. Rejected events never claim a dedup key, so a forged request can't block the real one.

## Design notes

- **Events** are partitioned monthly (`events_YYYY_MM`); ingest creates partitions 2 months ahead every 6 hours. Dedup lives in `event_dedup` so uniqueness holds across partitions.
- **Raw payloads are stored unmasked** because Month 4 replay must resend the exact bytes. Masking is applied when the Explorer reads them. Credential headers (`Authorization`, `Cookie`) are dropped at ingest and never stored.
- **Vault**: each org gets its own AES-256-GCM data key, wrapped by `MASTER_KEY`. Swap `LocalWrapper` for a KMS wrapper later without touching callers.
- **Logs** never include headers or bodies; ingest tokens are redacted from paths.
- **Audit log** entries are written in the same transaction as the change they describe.
