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

`generic` (hex HMAC-SHA256 in a configurable header, default `X-Signature`), `razorpay`, `stripe` (with 5-minute timestamp tolerance), `shopify`, `github`,
`standardwebhooks` ([Standard Webhooks](https://www.standardwebhooks.com/) and Svix: `webhook-*` or `svix-*` headers, `whsec_` HMAC or `whpk_` Ed25519 keys, 5-minute tolerance, dedup on the message ID),
`cashfree` (base64 HMAC-SHA256 of timestamp + body with the PG secret key, 5-minute tolerance),
`payu` (SHA-512 reverse hash in the body, keyed with the merchant salt; form or JSON),
`phonepe` (`Authorization` = SHA-256 of `username:password`; proves the sender, not the body),
`jira` (Jira Cloud system webhooks: `X-Hub-Signature: sha256=<hex HMAC-SHA256 of the body>` when a secret is set; dedup on `X-Atlassian-Webhook-Identifier`, which stays the same on retries; event type from `webhookEvent`, e.g. `jira:issue_created`).
Add one in `internal/provider`: implement `Verify`, `DedupKey`, `EventType`, register it, and add its case to `Sign` (`sign.go`) and samples to `internal/simulate` (a test checks every sample signs and verifies).

Ingest outcomes:

- Valid signature, or no secret configured: stored as `received`, `200 {"id", "duplicate"}`.
- Same dedup key again (per webhook, 30-day window): `200` with the original ID; nothing new stored.
- Bad or missing signature: stored as `rejected` (evidence for the Explorer), `401`. Rejected events never claim a dedup key, so a forged request can't block the real one.

### Event simulator

Test an integration without a real payment: pick a sample event for the webhook's provider (Razorpay `payment.captured`, Stripe `payment_intent.succeeded`, Shopify `orders/create`, GitHub `push`, Jira `jira:issue_created`…), edit it if you like, and send it. Relaya signs it with the webhook's own secret exactly as the provider does (`provider.Sign`, the counterpart of `Verify`) and runs it through the same checks as a real delivery: signature, dedup, destinations and retries. Samples get fresh IDs on each load, so sending one twice unchanged shows duplicate handling where the provider dedupes on the body (Stripe). Simulated events are stored with `simulated = true` and a `Relaya-Simulated: true` header, labelled in the Explorer, and never learned or checked by contracts. A Standard Webhooks `whpk_` public key can't sign; the provider must send its own test.

| Method & path | Min role |
|---|---|
| `GET /v1/orgs/{org}/webhooks/{webhook}/samples` → `{provider, signed, data: [{type, description, payload}]}` | member |
| `POST /v1/orgs/{org}/webhooks/{webhook}/simulate` `{"event_type", "payload"}` (both optional) → `{id, duplicate, event_type, status, signature, deliveries}` | admin |

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
| `connection_broken` | A connection's provider refuses to renew access (revoked, uninstalled, password changed) |
| `connection_recovered` | A broken connection works again (the user reconnected, or a manual refresh succeeded) |

Channel types:
- **Slack**: an Incoming Webhook URL (`https://hooks.slack.com/…`), stored encrypted.
- **Email**: to a member of the org. Needs the `SMTP_*` settings; without them the channel can be created but sends fail.
- **Webhook**: JSON `{type, title, body, link, org_id, alert_id, sent_at}`, signed like deliveries (`Relaya-Signature`) with a secret shown once. Same outbound URL rules as destinations.
- **Jira**: a Jira Cloud site, the Atlassian account's email and API token (stored encrypted), a project key and an issue type (default `Task`); checked with Jira when saved. Each problem opens one issue labelled `relaya` with a link back to the dashboard. While it is open, the same problem again (same incident, destination, connection, sync or webhook) adds a comment; the recovery comments and moves the issue to the first "done" status its workflow allows. "Send test" checks the token, project and issue type without creating an issue. The alert log shows each alert's issue key and link.

Alerts are queued in the same transaction as the change that caused them and sent by the worker (4 attempts: now, +1m, +5m, +30m).

| Method & path | Min role |
|---|---|
| `GET /v1/orgs/{org}/alert-settings` (kinds, whether email is configured) | member |
| `GET /v1/orgs/{org}/alert-channels` (with sent/failed counts for 7 days) | member |
| `POST /v1/orgs/{org}/alert-channels` `{"type", "name", "url" or "email" or "jira": {"site", "email", "api_token", "project", "issue_type"}, "events"}` | admin |
| `PATCH …/alert-channels/{channel}` `{"name", "events", "enabled"}`, `DELETE …` | admin |
| `POST …/alert-channels/{channel}/test` (sends now, returns `{ok, error}`) | admin |
| `GET /v1/orgs/{org}/alerts` (last 100) | member |

## Connections

Your users connect their accounts at other apps; Relaya runs the sign-in, stores the tokens encrypted (org data key) and keeps them fresh.

Providers (`internal/connect/catalog.go`): `zoho` (OAuth2; follows the user's data centre from the callback's `accounts-server`, Zoho hosts only), `hubspot` (OAuth2), `google` (OAuth2 + PKCE, offline access), `jira` (Atlassian OAuth 2.0 3LO with `offline_access`; after connecting, Relaya reads the granted sites from `accessible-resources` and calls the first Jira one through `https://api.atlassian.com/ex/jira/<cloud id>`, stored as `api_base` with `site_url`, and any other sites in `sites`; refresh tokens rotate and each new one is kept), `shiprocket` (API-user login; the token is renewed by logging in again before its 10 days run out).

Flow:
1. An admin adds an **integration**: the provider plus the org's own OAuth client (Zoho/HubSpot/Google), registered with the redirect URI `CONNECT_REDIRECT_URI` (shown in the dashboard).
2. The org's backend creates a **Connect link** for one of its users (`end_user_id` = its own ID for them). Links work once, for 30 minutes.
3. The user opens `/connect/{token}`, signs in at the provider (or enters their Shiprocket API login), and lands on the org's `return_url` with `?status=connected&connection_id=…&end_user_id=…`, or on Relaya's result page.
4. The org's backend calls `GET …/connections/{connection}/token` before calling the provider; Relaya renews the token first when it's about to expire.

The worker renews tokens expiring within 10 minutes. Temporary failures (timeouts, 5xx, rate limits) are retried with a backoff of one minute per failure (max 10). Permanent ones (`invalid_grant`, revoked, wrong login) mark the connection **broken** and send `connection_broken`; connecting the same `end_user_id` again repairs it.

| Method & path | Min role |
|---|---|
| `GET /v1/connect/providers` (catalog + redirect URI) | any signed-in caller |
| `GET /v1/orgs/{org}/integrations` | member |
| `POST /v1/orgs/{org}/integrations` `{"provider", "key", "name", "client_id", "client_secret", "scopes"}` | admin |
| `PATCH …/integrations/{integration}`, `DELETE …` (deletes its connections) | admin |
| `POST /v1/orgs/{org}/connect-sessions` `{"integration" (key or ID), "end_user_id", "return_url"}` → `{url, expires_at}` | admin |
| `GET /v1/orgs/{org}/connections` (`?integration=`, `?end_user_id=`, `?status=`), `GET …/connections/{connection}` | member |
| `GET …/connections/{connection}/token` → `{access_token, token_type, expires_at, api_base}`; 409 when broken | admin |
| `POST …/connections/{connection}/refresh` (renew now; returns the connection and any error), `DELETE …` | admin |

### Proxy

`GET|POST|PUT|PATCH|DELETE /v1/orgs/{org}/connections/{connection}/proxy/{path}` (admin) calls the provider's API as the connected user: Relaya adds the token (`Bearer`, or `Zoho-oauthtoken` for Zoho) and forwards the method, query string and body (max 10 MB).
- **Where**: the connection's API base (e.g. Zoho's `https://www.zohoapis.in`), or `Relaya-Proxy-Base-Url` set to another host of the same provider (Google: any `https://*.googleapis.com`). Tokens are never sent anywhere else, and redirects are handed back rather than followed.
- **Headers**: `Content-Type`, `Accept`, `Accept-Language`, `If-*` and `Idempotency-Key` go through; send any other header as `Relaya-Proxy-<Name>`. Your `Authorization` and cookies never do. The provider's `Content-Type`, `ETag`, `Location`, `Retry-After`, `Link` and `X-*` headers come back, plus `Relaya-Proxy-Attempts`.
- **Reliability**: a 401 renews the token and tries once more; GET/PUT/DELETE are retried on 429/502/503/504 (3 attempts, `Retry-After` up to 5 s). POST and PATCH are never retried. Calls may take up to 110 s.
- **Errors from Relaya itself** (connection broken 409, host not allowed 400, provider unreachable 502) carry `Relaya-Proxy-Error: true`; everything else is the provider's own answer.
- **Log**: `GET /v1/orgs/{org}/proxy-calls[?connection=]` (member): method, host, path, status, attempts and duration of the last 100 calls; no query strings or bodies; kept 30 days.

### Syncs

A sync reads a connection's data on a schedule (every 5 minutes to once a day) and turns new and changed records into events such as `zoho.lead.created` or `google.sheet_row.updated`. They are stored on a webhook, so they go through the normal path: destinations, retries, contracts, incidents, replay. This is also how apps without webhooks become event sources.

| Model | Reads | Change detection |
|---|---|---|
| `hubspot.crm_objects` | contacts, companies or deals (search API, by last-modified time) | incremental |
| `zoho.crm_records` | any CRM module (v2 Get Records, `If-Modified-Since`, by `Modified_Time`) | incremental |
| `google.sheet_rows` | a sheet or range; the first row is the column names; rows keyed by row number or a key column | full look each run (10,000 rows) |
| `shiprocket.orders` | the latest 100-1,000 orders | full look each run |
| `jira.issues` | issues, optionally some `projects` and an extra `jql` filter, with chosen `fields` (JQL search, `/rest/api/3/search/jql`) | incremental, by `updated` in the user's time zone (to the minute) |

- Each record's content hash is kept (`sync_records`); only a new ID or a different hash makes an event (`record_id`, `change`, `record`, `end_user_id`, `sync_id`). Deleted records aren't reported yet.
- The first run only remembers what exists, unless `emit_existing` is set. The baseline counts as done only once a run reaches the end.
- A run reads at most 5,000 records; the rest continues a minute later from the saved cursor. Records and their events are written in one transaction per page.
- API calls go through the proxy (token renewal, retries, host allowlist) and appear in the proxy call log as `sync:<id>`.
- Three failed runs in a row send `sync_failing`, once, and the next good run sends `sync_recovered`. Runs failing because the connection is broken don't send these (the connection alerts).
- Without a `webhook_id`, a new webhook "Sync: …" is created in the org's first project, with a random signing secret so nothing outside can post into it.

| Method & path | Min role |
|---|---|
| `GET /v1/connect/sync-models` (models and their settings) | any signed-in caller |
| `GET /v1/orgs/{org}/syncs`, `GET …/syncs/{sync}/runs` (last 50) | member |
| `POST /v1/orgs/{org}/syncs` `{"connection_id", "model", "config", "interval_minutes", "webhook_id", "emit_existing"}` | admin |
| `PATCH …/syncs/{sync}` `{"enabled", "interval_minutes", "config"}` (new config starts over), `DELETE …` | admin |
| `POST …/syncs/{sync}/run` (run now) | admin |

Public, rate-limited per IP (the link token is the credential): `GET /v1/connect/sessions/{token}`, `POST …/authorize` (→ provider URL), `POST …/login` (login providers), `GET /v1/connect/callback` (OAuth redirect URI; always redirects).

## Outbound webhooks

For SaaS products that send webhooks to their own customers. The product calls one API; Relaya signs, delivers, retries and logs, and each customer manages their endpoints in a hosted portal.

- **Apps.** One per customer, keyed by the product's own ID (`uid`, e.g. `customer-123`). Each app owns a hidden webhook (`webhooks.kind = 'outbound'`) whose destinations are the customer's endpoints (at most 20). Outbound webhooks don't appear in the inbound list and refuse ingest (404).
- **Messages.** `POST …/outbound/messages` `{"app", "event_type", "payload", "idempotency_key"}` stores an event with body `{"type", "timestamp", "data"}` and queues one delivery per endpoint that takes that type, through the normal worker (retries, Events, replay). The same `idempotency_key` returns the first message with `duplicate: true`. New types are added to the event-type catalog automatically.
- **Signing.** [Standard Webhooks](https://www.standardwebhooks.com): `webhook-id` (the message ID, the same on every retry), `webhook-timestamp`, `webhook-signature: v1,<base64 HMAC-SHA256(key, "<id>.<ts>.<body>")>`, secret `whsec_…`. No `Relaya-*` headers, so customers can use any Standard Webhooks library.
- **Routing.** Endpoints (and inbound destinations, via `event_types`) can take only some event types; an empty list takes all of them, including new ones.
- **Portal.** `POST …/portal-link` returns `{url, expires_at}` for a 24-hour link `DASHBOARD_URL/portal#ps_…`; the token sits in the URL fragment, so it never reaches server logs. There the customer adds, edits, disables and deletes endpoints, shows and rotates signing secrets, sends test events, and browses deliveries (with the last response) and re-sends failed ones. The portal only reaches its own app's endpoints; its changes are audited as `portal:<uid>`.

| Method & path | Min role |
|---|---|
| `GET /v1/orgs/{org}/outbound/apps`, `GET …/apps/{app}` (with endpoints) | member |
| `POST /v1/orgs/{org}/outbound/apps` `{"uid", "name"}`, `DELETE …/apps/{app}` | admin |
| `POST …/apps/{app}/endpoints` `{"url", "description", "event_types"}` (→ `signing_secret`), `PATCH/DELETE …/endpoints/{endpoint}` | admin |
| `GET …/endpoints/{endpoint}/secret`, `POST …/endpoints/{endpoint}/test[?event_type=]` | admin |
| `POST …/apps/{app}/portal-link` | admin |
| `POST /v1/orgs/{org}/outbound/messages` | admin (API key) |
| `GET /v1/orgs/{org}/outbound/event-types`; `POST` `{"name", "description"}`, `DELETE …/event-types/{name}` | member; admin |

Public, rate-limited per IP, `Authorization: Bearer ps_…`: `GET /v1/portal/app`, `GET/POST /v1/portal/endpoints`, `PATCH/DELETE …/endpoints/{endpoint}`, `GET …/secret`, `POST …/rotate-secret`, `POST …/test`, `GET /v1/portal/deliveries?status=&endpoint=&before=`, `POST /v1/portal/deliveries/{delivery}/retry` (failed or retrying only).

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

## Operations

**Rate limits** (in memory per process: right for one server; several servers would share them through Redis). Over-limit requests get `429` with `Retry-After`; providers retry, so nothing is lost.

| What | Limit |
|---|---|
| Sign-in | 20 a minute per IP, and 10 **failed** attempts per 15 minutes per account (successful sign-ins never lock the owner out) |
| Sign-up | 10 an hour per IP |
| Authenticated API | `RATE_LIMIT_API_PER_MINUTE` per user or API key (default 600) |
| Ingest | `RATE_LIMIT_INGEST_PER_SECOND` / `_BURST` per webhook URL (default 100/s, bursts of 1000); requests to unknown URLs: 60 a minute per IP |

Client IPs come from `X-Real-IP`, which only IIS can set: keep `API_ADDR`/`INGEST_ADDR` on `127.0.0.1` when `TRUST_PROXY_HEADERS=true`.

**Retention** (the worker, hourly). Event payloads with their deliveries, attempts and contract findings are kept for `EVENT_RETENTION` (default 30 days): whole months past it are dropped as partitions, the rest deleted in batches. The alert log is kept for `ALERT_RETENTION` (90 days); expired sessions are removed. Incidents, contracts, rules and the audit log are kept.

**Backups.** `setup.ps1` schedules *Relaya database backup* nightly at 02:30 (`bin\backup.ps1`): `pg_dump` into `C:\relaya\backups` (admins and SYSTEM only), newest 14 kept, each with a SHA-256. `bin\restore-drill.ps1` restores the newest into a temporary database, compares row counts with the live one, times it (target: under an hour) and drops it. Run the drill after changes to the schema, and copy the backups off this machine too.

**Metrics.** `GET /metrics` on the API (via IIS: `/api/metrics`) and ingest, in Prometheus format, with `Authorization: Bearer $METRICS_TOKEN` (404 without it). Requests and latency per route (IDs and tokens normalised), delivery and contract-check queue depth and lag (`relaya_delivery_lag_seconds` climbing means the worker is stuck), events and delivery outcomes in the last 5 minutes, open incidents, pending alerts, database size.

**Monitoring.** `deploy\monitoring\install.ps1 -Downloads <folder> -PublicUrl https://<host>/grafana/` installs Prometheus (scrapes every 15 s, keeps 30 days, rules in `alerts.yml`), Grafana (the *Relaya overview* dashboard) and windows_exporter (CPU, memory, disk, service state, and the nightly backup's time and size) as services under `C:\relaya\monitoring`, all on 127.0.0.1. IIS publishes Grafana only, at `/grafana` behind its login (admin password in `secrets.txt`); Prometheus has no login, so it stays on `http://localhost:9090` (use Grafana's Explore instead). Alerts cover: a service down, deliveries stuck over 5 minutes, contract checks behind, the database unreachable, over 5% server errors, customer alerts piling up, no backup for a day, and disk C: under 10% free.

**Security headers** (IIS, `web.config`): HSTS for this host only, a same-origin Content-Security-Policy, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy`, `Permissions-Policy`.

**Load** (measured on this 7-core server, sharing it with other sites; 10 webhooks, each forwarding to an endpoint): ~370 events/s sustained with no errors and nothing dropped; provider-facing latency p50 7 ms / p99 44 ms; every event delivered, provider → endpoint p99 under 300 ms; every event contract-checked with no backlog. That is about 100× the average rate of the Business plan (10M events a month).

## Design notes

- **Events** are partitioned monthly (`events_YYYY_MM`); ingest creates partitions 2 months ahead every 6 hours. Dedup lives in `event_dedup` so uniqueness holds across partitions.
- **Raw payloads are stored unmasked** because Month 4 replay must resend the exact bytes. Masking is applied when the Explorer reads them. Credential headers (`Authorization`, `Cookie`) are dropped at ingest and never stored.
- **Vault**: each org gets its own AES-256-GCM data key, wrapped by `MASTER_KEY`. Swap `LocalWrapper` for a KMS wrapper later without touching callers.
- **Logs** never include headers or bodies; ingest tokens are redacted from paths.
- **Audit log** entries are written in the same transaction as the change they describe.
