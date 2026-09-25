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

## Delivery (forwarding to your endpoints)

Add **destinations** to a webhook; every accepted event is forwarded to each enabled destination by `cmd/worker`.

- **Queued with the event.** Ingest inserts one `deliveries` row per destination in the same transaction as the event and `NOTIFY`s the workers, so nothing is lost and delivery starts within milliseconds. Duplicates and rejected events are never forwarded.
- **Request.** `POST` of the original body with the original `Content-Type` and provider headers (so existing provider-signature checks keep working), plus `Relaya-Event-Id`, `Relaya-Delivery-Id`, `Relaya-Attempt`, `Relaya-Event-Type`, `Idempotency-Key` (= delivery ID, stable across retries) and `Relaya-Signature: t=<unix>,v1=<hex HMAC-SHA256(secret, "<t>.<body>")>`.
- **Retries.** 2xx = delivered. 429/408/5xx/timeouts/network errors retry after 30s, 2m, 10m, 30m, 1h, 3h, 6h (±20% jitter; `Retry-After` honoured) up to `max_attempts` (default 8). Other 4xx and 3xx fail immediately. "Retry now" in the dashboard or `POST /deliveries/{id}/retry` adds one more attempt.
- **Verifying on the customer side.** The SDKs in [relaya-sdks](https://github.com/Dhirajrai12/relaya-sdks) (Node.js, Python, PHP, Go, Java) do it in one line and also wrap this API.
- **Workers.** Claim jobs with `FOR UPDATE SKIP LOCKED` and a 5-minute lease, so you can run several and a crashed worker's jobs are picked up again.
- **SSRF protection.** Destinations must be public `https` URLs. The worker re-checks the resolved IP at connect time and refuses loopback, private, link-local (cloud metadata), CGNAT and other internal ranges, and never follows redirects. `DELIVERY_ALLOW_HTTP` / `DELIVERY_ALLOW_PRIVATE` relax this for local development only (on by default when `APP_ENV=dev`).

| Method & path | Min role |
|---|---|
| `GET/POST /v1/orgs/{org}/webhooks/{webhook}/destinations` | member / admin |
| `PATCH/DELETE /v1/orgs/{org}/destinations/{destination}` | admin |
| `POST …/destinations/{destination}/test` (synchronous test delivery) | admin |
| `POST …/destinations/{destination}/rotate-secret` | admin |
| `GET /v1/orgs/{org}/deliveries?event_id=&destination_id=&webhook_id=&status=` | member |
| `GET /v1/orgs/{org}/deliveries/{delivery}` (with attempts) | member |
| `POST /v1/orgs/{org}/deliveries/{delivery}/retry` | admin |

## Integration contracts

Relaya learns the payload shape of each **(webhook, event type)** and checks every later event against it. The work happens in the worker (`internal/contract`), never on the ingest path.

1. **Learn.** Ingest queues each accepted JSON-object event that has a type (`contract_queue`, same transaction). The checker flattens the payload into paths (`payload.payment.entity.amount`, arrays as `items[].sku`) and records types (string/integer/number/boolean/null/object/array), how often each path appears (required = in every sample) and, for strings whose values repeat, the allowed values (enum). After `CONTRACT_MIN_SAMPLES` events (default 20), or `CONTRACT_LEARN_WINDOW` (24h) with at least 3, the contract is **proposed**.
2. **Activate.** An admin marks critical fields and activates it (`POST /contracts/{id}/versions`). Each version stores the schema, a fingerprint and the critical fields.
3. **Check.** Each event gets `contract_status`:

| Change | Result |
|---|---|
| Same shape | `ok` |
| New field | `compatible` (tracked under "new fields", can be accepted) |
| New enum value; integer becomes decimal; non-critical field missing, retyped or null | `suspicious` (warning) |
| Critical field missing (removed or renamed), retyped or null | `breaking` + incident |

A missing object is reported once at its top path (breaking if any critical field is inside). Fields inside arrays are only required when the array has elements.

4. **Incidents.** One open incident per (contract, kind, path); repeats bump `event_count`. Resolve manually, or **accept changes**: a new version built from events since the last activation, which resolves the contract's open incidents. **Relearn** starts over.
5. **Auto-resolve.** Every minute the worker closes open incidents with no occurrence for `INCIDENT_AUTO_RESOLVE_AFTER` (default 1h), **provided** a later event of that contract was checked without the same finding. A provider that merely went quiet leaves the incident open. Closed with `resolved_by = system` and an audit entry.
6. **Replay (recover + verify).** After fixing your endpoint:
   - `GET /incidents/{id}/replay` is the dry run: affected events, deliveries per destination, already-delivered and in-flight counts. Nothing changes.
   - `POST /incidents/{id}/replay {"confirm": true}` (admin) re-queues those deliveries (enabled destinations; in-flight ones skipped) under a `replays` row. Only one running replay per incident.
   - The worker sends them with the original `Idempotency-Key` plus `Relaya-Replay: <replay id>`, with normal retries.
   - When the last one finishes, the replay completes. If none failed, the incident resolves itself: "verified by replay: N of N deliveries accepted by the destination". "Verified" means your endpoint answered 2xx for every event; Relaya doesn't read back into your system.

| Method & path | Min role |
|---|---|
| `GET /v1/orgs/{org}/contracts?webhook_id=` | member |
| `GET /v1/orgs/{org}/contracts/{contract}` (fields, new fields, versions, recent findings) | member |
| `POST /v1/orgs/{org}/contracts/{contract}/versions` `{"critical_fields": [...], "source": "observed" or "active"}` | admin |
| `POST /v1/orgs/{org}/contracts/{contract}/relearn` | admin |
| `GET /v1/orgs/{org}/incidents?status=open|resolved` | member |
| `POST /v1/orgs/{org}/incidents/{incident}/resolve` `{"resolution": "…"}` | admin |

Events can be filtered with `?contract_status=breaking`, and the event detail includes its `violations`.

## Repair rules

When a provider breaks its contract, a repair rule edits the payload before it's forwarded, so your endpoint keeps working while the provider fixes their side. The stored event is never changed.

- **Rules** belong to a webhook and optionally one event type, and run in order. Each is a list of changes (paths use the contract notation, `items[].price`):

  | Change | Example |
  |---|---|
  | `convert` to `string`, `number`, `integer` or `boolean` | `"100"` → `100`. Never loses data: `"12.5"` is left alone for an integer. |
  | `rename` from → to | `amount_paise` → `amount`. Never overwrites a real value. |
  | `default` | Fill `currency` with `"INR"` when missing or null. |
  | `set`, `remove` | Always set or remove a field. |
  | `map` | `"SUCCESS"` → `"captured"`. |

- **At send time.** The worker applies the current rules on every attempt, so retries and replays get them too. Payloads that aren't JSON objects, or that no rule changes, are sent byte for byte. A repaired request carries `Relaya-Repaired: <rule ids>`, is signed by `Relaya-Signature` over the repaired body, and drops the provider's own signature/digest headers (they'd no longer match). Key order and number formatting are kept.
- **Contracts.** An event that breaks the contract but passes once repaired gets `contract_status = repaired`: the finding is recorded (`repaired: true`) for visibility, but opens no incident. Contract stats count these as `repaired_24h`, not breaking.
- **From an incident.** `GET /incidents/{id}/repair-suggestion` proposes a rule: convert a retyped field back, rename a field the provider renamed (a new field of the same type in the same object), or fill a missing/null field with a value you choose. Save it with `incident_id`, then replay the incident: the old events go out repaired and the incident resolves itself. Replays leave out events that were already sent repaired.
- **Preview.** `POST /repair-rules/preview` dry-runs a draft (after the webhook's other rules) on a stored event and returns the masked before/after payloads, how many values each change touched, and the contract check before and after. Nothing is saved.

| Method & path | Min role |
|---|---|
| `GET /v1/orgs/{org}/repair-rules?webhook_id=` | member |
| `POST /v1/orgs/{org}/repair-rules` `{"webhook_id", "event_type", "name", "ops", "incident_id"?}` | admin |
| `PATCH …/repair-rules/{rule}` `{"name", "event_type", "ops", "enabled"}`, `DELETE …` | admin |
| `POST /v1/orgs/{org}/repair-rules/preview` `{"webhook_id", "event_type", "ops", "event_id"?, "rule_id"?}` | member |
| `GET /v1/orgs/{org}/incidents/{incident}/repair-suggestion` | member |

## Alerts

Channels (Settings → Alerts) get a message when something needs a human. Each channel picks which kinds it wants:

| Kind | When |
|---|---|
| `incident_opened` | A contract incident opens (once per incident, not per occurrence) |
| `incident_resolved` | An incident resolves: by hand, accepted change, relearn, auto-resolve or verified replay |
| `destination_failing` | A destination fails 3 attempts in a row (once per outage) |
| `destination_recovered` | The next successful attempt after a failing alert |
| `signature_failures` | A webhook rejects an event for a bad signature (at most once an hour per webhook) |

Channel types:
- **Slack**: an Incoming Webhook URL (`https://hooks.slack.com/…`), stored encrypted.
- **Email**: to a member of the org. Needs the `SMTP_*` settings; without them the channel can be created but sends fail.
- **Webhook**: JSON `{type, title, body, link, org_id, alert_id, sent_at}`, signed like deliveries (`Relaya-Signature`) with a secret shown once. Same outbound URL rules as destinations.

Alerts are queued in the same transaction as the change that caused them and sent by the worker (4 attempts: now, +1m, +5m, +30m).

| Method & path | Min role |
|---|---|
| `GET /v1/orgs/{org}/alert-settings` (kinds, whether email is configured) | member |
| `GET /v1/orgs/{org}/alert-channels` (with sent/failed counts for 7 days) | member |
| `POST /v1/orgs/{org}/alert-channels` `{"type", "name", "url" or "email", "events"}` | admin |
| `PATCH …/alert-channels/{channel}` `{"name", "events", "enabled"}`, `DELETE …` | admin |
| `POST …/alert-channels/{channel}/test` (sends now, returns `{ok, error}`) | admin |
| `GET /v1/orgs/{org}/alerts` (last 100) | member |

## Realtime (WebSocket)

Dashboards stay live without refreshing: `GET /v1/orgs/{org}/stream` upgrades to a WebSocket.

1. Client sends `{"type":"auth","token":"rs_… | rk_…"}` as its first message (within 10s). The token is never in the URL, so it can't end up in proxy or IIS logs.
2. Server replies `{"type":"ready"}`, then pushes small messages that say *what changed*:
   - `event`: an incoming webhook was stored (`event_id`, `webhook_id`, `status`)
   - `delivery`: a delivery attempt finished (`event_id`, `delivery_id`, `destination_id`, `status`)
   - `change`: anything written to the audit log (`action`, e.g. `webhook.create`, `target_id`)
   - `resync`: messages were dropped (slow client or DB reconnect); refetch everything
3. The dashboard refetches only the affected queries (bursts coalesced every 100 ms) and stops polling while connected. It reconnects with backoff, and polls as a fallback while offline.

How it works: writers call `realtime.Notify` inside their transaction (`pg_notify`, delivered only on commit). Each API process runs a `realtime.Hub` that `LISTEN`s and fans out by organization. The server pings every 25s (keeps IIS/ARR from idling the socket out) and re-validates the session and membership every 5 minutes. Allowed origins are `CORS_ALLOWED_ORIGINS` plus the host of `INGEST_BASE_URL`.

## Design notes

- **Events** are partitioned monthly (`events_YYYY_MM`); ingest creates partitions 2 months ahead every 6 hours. Dedup lives in `event_dedup` so uniqueness holds across partitions.
- **Raw payloads are stored unmasked** because Month 4 replay must resend the exact bytes. Masking is applied when the Explorer reads them. Credential headers (`Authorization`, `Cookie`) are dropped at ingest and never stored.
- **Vault**: each org gets its own AES-256-GCM data key, wrapped by `MASTER_KEY`. Swap `LocalWrapper` for a KMS wrapper later without touching callers.
- **Logs** never include headers or bodies; ingest tokens are redacted from paths.
- **Audit log** entries are written in the same transaction as the change they describe.
