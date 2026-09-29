import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { cn } from '@/lib/utils'
import { C, Code, PublicPage, Table } from './layout'

const API = 'https://server.aegonassett.com/api/v1'

const toc = [
  { id: 'quick-start', label: 'Quick start' },
  { id: 'receive', label: 'Receive events' },
  { id: 'sdks', label: 'SDKs' },
  { id: 'testing', label: 'Test events and the CLI' },
  { id: 'retries', label: 'Retries and idempotency' },
  { id: 'contracts', label: 'Contracts and incidents' },
  { id: 'repair', label: 'Repair rules and replay' },
  { id: 'alerts', label: 'Alerts' },
  { id: 'outbound', label: 'Send webhooks to your customers' },
  { id: 'connections', label: "Your users' accounts" },
  { id: 'api', label: 'REST API' },
  { id: 'limits', label: 'Limits' },
]

const tocIds = toc.map((t) => t.id)

export function DocsPage() {
  const active = useActiveSection(tocIds)
  return (
    <PublicPage
      wide
      eyebrow="Documentation"
      title="Relaya docs"
      intro="Everything you need to route a provider's webhooks through Relaya, forward them to your code, test them, and recover when something breaks. Plus sending webhooks to your own customers, and working with your users' accounts at other apps."
    >
      <div className="grid grid-cols-1 gap-10 lg:grid-cols-[13rem_minmax(0,1fr)]">
        <nav aria-label="On this page" className="hidden lg:block">
          <ul className="sticky top-24 space-y-1 border-l border-l-border text-sm">
            {toc.map((t) => (
              <li key={t.id}>
                <a
                  href={`#${t.id}`}
                  className={cn(
                    '-ml-px block border-l-2 border-transparent py-1 pl-4 transition hover:text-l-text',
                    active === t.id && 'border-[#14b886] font-medium text-l-text',
                  )}
                >
                  {t.label}
                </a>
              </li>
            ))}
          </ul>
        </nav>

        <article className="min-w-0 max-w-3xl text-[15px] leading-relaxed">
          <Section id="quick-start" title="Quick start">
            <ol className="list-decimal space-y-3 pl-5">
              <li>
                <Link to="/signup" className="text-l-accent underline-offset-4 hover:underline">Sign up</Link> and create a{' '}
                <b className="text-l-text">project</b>, then a <b className="text-l-text">webhook</b>. Pick the provider (Razorpay, Cashfree, PayU,
                PhonePe, Stripe, Shopify, GitHub, Jira, Slack, Twilio, HubSpot, Square, Segment, SendGrid, Notion, Standard Webhooks / Svix (which also covers senders built on Svix, such as Brex, Clerk and Resend), or generic HMAC) and paste its signing secret, so Relaya can verify
                what arrives. The webhook's page shows where to set this up at the provider.
              </li>
              <li>
                Give the provider your webhook's URL instead of your own endpoint:
                <Code title="Webhook URL">{`https://server.aegonassett.com/v1/in/<your webhook token>`}</Code>
                Every event is stored, checked and shown in <b className="text-l-text">Events</b> within milliseconds.
              </li>
              <li>
                Add a <b className="text-l-text">destination</b>: your endpoint's public HTTPS URL. Relaya forwards each accepted event there,
                with retries. Copy the signing secret it shows once. A destination can take every event type or only some (e.g. just{' '}
                <C>payment.captured</C>).
              </li>
              <li>In your endpoint, verify Relaya's signature (below) and answer 2xx once you've handled the event.</li>
              <li>
                Try it without a real payment: <b className="text-l-text">Send a test event</b> on the webhook's page, or the{' '}
                <a href="#testing" className="text-l-accent underline-offset-4 hover:underline">CLI</a> to receive events on your own computer.
              </li>
              <li>
                Optional: after 20 events of a type (or a day), Relaya proposes a <b className="text-l-text">contract</b>. Mark the fields you
                depend on as critical and activate it; from then on, breaking changes open an incident and alert you.
              </li>
            </ol>
          </Section>

          <Section id="receive" title="Receive events">
            <p>
              Relaya sends a <C>POST</C> with the provider's original body and <C>Content-Type</C>, the provider's own headers (so existing checks
              keep working), and these:
            </p>
            <Table
              head={['Header', 'Meaning']}
              rows={[
                [<C key="sig">Relaya-Signature</C>, <>Proves the request came from Relaya (see below).</>],
                [<C key="idem">Idempotency-Key</C>, <>The same on every retry and replay of one delivery. <b className="text-l-text">Dedupe on this.</b></>],
                [<C key="eid">Relaya-Event-Id</C>, <>The event's ID, to look it up in the dashboard or API.</>],
                [<C key="did">Relaya-Delivery-Id</C>, <>This delivery's ID.</>],
                [<C key="att">Relaya-Attempt</C>, <>1 on the first try, then 2, 3… on retries.</>],
                [<C key="etype">Relaya-Event-Type</C>, <>For example <C>payment.captured</C>, when Relaya could tell.</>],
                [<C key="replay">Relaya-Replay</C>, <>Set when the request is part of an incident replay.</>],
                [<C key="repair">Relaya-Repaired</C>, <>Set when repair rules changed the body (the provider's own signature headers are then left out).</>],
                [<C key="sim">Relaya-Simulated</C>, <>Set on test events from the event simulator or <C>relaya trigger</C>.</>],
              ]}
            />
            <h3 className="mt-8 text-lg font-semibold text-l-text">The signature</h3>
            <Code title="Relaya-Signature">{`Relaya-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, "<t>.<raw body>")>`}</Code>
            <p>
              Compute the HMAC over the exact bytes you received (not re-serialised JSON), compare in constant time, and reject timestamps more than
              5 minutes from now. The SDKs below do all of this in one line.
            </p>
            <Code title="Any language: check it by hand">{`t=1790380800
body='{"type":"payment.captured","amount":100}'
printf '%s.%s' "$t" "$body" | openssl dgst -sha256 -hmac "$RELAYA_SIGNING_SECRET"
# must equal the v1= value`}</Code>
          </Section>

          <Section id="sdks" title="SDKs">
            <p>Each SDK verifies deliveries and wraps the REST API. Source and full READMEs: <a className="text-l-accent underline-offset-4 hover:underline" href="https://github.com/relayaa/relaya-sdks" target="_blank" rel="noreferrer">github.com/relayaa/relaya-sdks</a>.</p>
            <SdkTabs />
            <p>
              Every SDK (Node.js, Python, PHP, Go, Java) also covers{' '}
              <a href="#outbound" className="text-l-accent underline-offset-4 hover:underline">sending webhooks to your customers</a> and{' '}
              <a href="#connections" className="text-l-accent underline-offset-4 hover:underline">your users' accounts</a>.
            </p>
          </Section>

          <Section id="testing" title="Test events and the CLI">
            <h3 className="text-lg font-semibold text-l-text">Event simulator</h3>
            <p>
              On a webhook's page, <b className="text-l-text">Send a test event</b> picks a sample for its provider (Razorpay{' '}
              <C>payment.captured</C>, Stripe <C>payment_intent.succeeded</C>, Shopify <C>orders/create</C>, GitHub <C>push</C>, Jira{' '}
              <C>jira:issue_created</C>…), lets you edit it, and sends it. Relaya signs it with the webhook's own secret exactly the way the provider
              does, so it goes through everything a real event does: signature check, duplicate detection, your destinations and retries. Test events
              are labelled <b className="text-l-text">simulated</b> in Events and never teach contracts.
            </p>
            <h3 className="mt-8 text-lg font-semibold text-l-text">The CLI: events on your own computer</h3>
            <p>
              While you build, forward the events a webhook receives to your laptop, like the Stripe CLI. It connects out to Relaya, so it works behind
              any firewall or home network, and catches up on anything missed if the connection drops.
            </p>
            <Code title="Terminal">{`relaya login                        # paste an admin API key (it isn't shown)
relaya listen --forward-to http://localhost:3000/webhooks
relaya trigger payment.captured     # in another terminal: a signed test event`}</Code>
            <Code title="What you see">{`11:15:19  Ready. Waiting for events…
11:15:22  payment.captured (simulated)  6b20d442  → ✓ 200 OK (27 ms)
11:15:40  payment.failed  03867e95  → ✗ 500 Internal Server Error (4 ms)`}</Code>
            <p>
              Your local server gets the provider's original body and headers (so your own Razorpay or Stripe signature check keeps working) plus
              Relaya's headers, signed with a secret the CLI prints; it stays the same across runs, so keep it in your local <C>.env</C>. Options:{' '}
              <C>--webhook</C> (ID or name), <C>--events a,b</C>, <C>-H "Name: value"</C>, <C>--print-body</C>.
            </p>
            <p>
              Download it for Windows, macOS or Linux from the{' '}
              <a className="text-l-accent underline-offset-4 hover:underline" href="https://github.com/relayaa/relaya-sdks/releases/latest" target="_blank" rel="noreferrer">
                latest release
              </a>
              , or with Go: <C>go install github.com/relayaa/relaya-sdks/cli/cmd/relaya@latest</C>.
            </p>
          </Section>

          <Section id="retries" title="Retries and idempotency">
            <ul className="list-disc space-y-2 pl-5">
              <li><b className="text-l-text">Success</b> is any 2xx. Answer quickly and do slow work afterwards; the timeout is 10 seconds by default.</li>
              <li><b className="text-l-text">Retried:</b> 408, 429, 5xx, timeouts and network errors, after 30s, 2m, 10m, 30m, 1h, 3h and 6h (±20%), up to 8 attempts. A <C>Retry-After</C> header is honoured.</li>
              <li><b className="text-l-text">Not retried:</b> other 4xx and redirects (redirects are never followed: use the final URL).</li>
              <li>Because of retries and replays, the same event can arrive more than once. Store the <C>Idempotency-Key</C> and ignore repeats.</li>
              <li>Destinations must be public HTTPS URLs; private and internal addresses are refused.</li>
            </ul>
          </Section>

          <Section id="contracts" title="Contracts and incidents">
            <p>
              Relaya learns the shape of each event type: which fields exist, their types, which are always present, and small sets of values
              (like a status). When you activate a contract, every event is checked against it:
            </p>
            <Table
              head={['Result', 'Meaning']}
              rows={[
                ['matches', 'Fits the contract.'],
                ['new fields', 'The provider added fields. Nothing breaks.'],
                ['warning', 'A non-critical field changed type, went missing or has a new value.'],
                ['breaking', 'A critical field is missing, null or changed type. Opens an incident.'],
                ['repaired', 'It broke the contract, but a repair rule fixed it before forwarding. No incident.'],
              ]}
            />
            <p>
              An incident groups every event with the same broken field. It resolves when you resolve it, when you accept the change into a new
              contract version, after an hour with no new occurrences once a later event matches, or when a replay is fully delivered.
            </p>
          </Section>

          <Section id="repair" title="Repair rules and replay">
            <p>
              A repair rule edits the payload before it is forwarded, so your endpoint keeps working while the provider fixes their side. The stored
              event is never changed. Changes: convert a type (<C>"100"</C> → <C>100</C>), rename a field, fill in a missing value, replace values,
              set or remove a field. Paths use dots, and <C>[]</C> for every item of a list: <C>items[].price</C>.
            </p>
            <p>
              On an incident, <b className="text-l-text">Fix with a rule</b> suggests the change and lets you preview it on the incident's own event.
              Then <b className="text-l-text">Replay</b> resends the affected events (repaired, with their original idempotency keys); when every one
              is accepted, the incident resolves itself.
            </p>
          </Section>

          <Section id="alerts" title="Alerts">
            <p>
              In <b className="text-l-text">Settings → Alerts</b>, send alerts to Slack (an incoming webhook), a teammate's email, Jira, or your own
              endpoint (signed like deliveries). Choose which: incident opened or resolved, a destination failing (3 failures in a row) or
              recovered, signature failures, a connection broken or recovered, and a sync failing or recovered. You get one alert per problem, not
              one per event.
            </p>
            <p>
              <b className="text-l-text">Jira:</b> enter your Jira site, your Atlassian email, an{' '}
              <a className="text-l-accent underline-offset-4 hover:underline" href="https://id.atlassian.com/manage-profile/security/api-tokens" target="_blank" rel="noreferrer">
                API token
              </a>
              , a project key and an issue type; Relaya checks them with Jira when you save. Each problem opens one issue labelled <C>relaya</C>. If it
              happens again while the issue is open, Relaya adds a comment; when it's fixed, Relaya comments and moves the issue to Done. The alert log
              links every alert to its issue.
            </p>
          </Section>

          <Section id="outbound" title="Send webhooks to your customers">
            <p>
              If your product sends webhooks to its own customers, Relaya can do the sending: one API call per event, and Relaya signs it with{' '}
              <a className="text-l-accent underline-offset-4 hover:underline" href="https://www.standardwebhooks.com" target="_blank" rel="noreferrer">Standard Webhooks</a>
              , retries failures for up to a day and logs every attempt.
            </p>
            <ol className="list-decimal space-y-2 pl-5">
              <li>
                Create an <b className="text-l-text">app</b> for each customer, keyed by your own ID for them (<C>POST /outbound/apps</C>, or{' '}
                <b className="text-l-text">Outbound</b> in the dashboard).
              </li>
              <li>Whenever something happens, send a message. With an <C>idempotency_key</C>, sending the same message twice sends it once.</li>
              <li>
                Give each customer a <b className="text-l-text">portal link</b> (valid 24 hours) where they add their endpoints, choose event types,
                see every delivery and re-send failures. Or manage endpoints for them through the API.
              </li>
            </ol>
            <Code title="Send an event">{`curl -X POST ${API}/orgs/<org id>/outbound/messages \\
  -H "Authorization: Bearer rk_..." -H "Content-Type: application/json" \\
  -d '{"app":"customer-123","event_type":"invoice.paid","payload":{"invoice_id":"in_1","amount":1999},"idempotency_key":"in_1-paid"}'`}</Code>
            <p>
              Your customers verify each request with any Standard Webhooks library, using the endpoint's <C>whsec_…</C> secret and the{' '}
              <C>webhook-id</C>, <C>webhook-timestamp</C> and <C>webhook-signature</C> headers. <C>webhook-id</C> stays the same on retries, so it's
              what they dedupe on.
            </p>
          </Section>

          <Section id="connections" title="Your users' accounts: connect, call, sync">
            <p>
              Let your users connect their <b className="text-l-text">Zoho, HubSpot, Google, Jira or Shiprocket</b> accounts. Relaya runs the
              sign-in, stores their tokens encrypted and keeps them fresh. Add your OAuth app once in <b className="text-l-text">Connections</b>{' '}
              (the page shows each provider's setup steps and callback URL), then:
            </p>
            <ol className="list-decimal space-y-2 pl-5">
              <li>
                <b className="text-l-text">Connect:</b> your backend creates a one-time link for a user (<C>POST /connect-sessions</C>); open it in a
                popup with <C>connect.js</C> or redirect them.
              </li>
              <li>
                <b className="text-l-text">Call:</b> send API calls through Relaya's proxy
                (<C>{'/connections/{id}/proxy/<provider path>'}</C>). Relaya adds and renews the user's token and retries what is safe to retry. The
                provider's own answers come back as they are; errors from Relaya itself carry <C>Relaya-Proxy-Error: true</C>.
              </li>
              <li>
                <b className="text-l-text">Sync:</b> new and changed records become events on a schedule, delivered like any webhook, e.g.{' '}
                <C>zoho.lead.created</C>, <C>hubspot.contact.updated</C>, <C>google.sheet_row.created</C>, <C>shiprocket.order.updated</C> or{' '}
                <C>jira.issue.created</C> (optionally only some projects or a JQL filter). This also works for apps that don't send webhooks.
              </li>
            </ol>
            <Code title="Call Zoho as one of your users (Node.js)">{`const conn = await relaya.connections.find('zoho', user.id)
const res = await relaya.proxy(conn.id).get('/crm/v2/Leads', { query: { per_page: 10 } })
if (res.ok) console.log(res.data)`}</Code>
            <p>
              Jira connections use Atlassian OAuth: after the user approves, Relaya finds their Jira site and calls it through{' '}
              <C>api.atlassian.com</C>. A connection breaks when the user revokes access; you get a <b className="text-l-text">connection broken</b>{' '}
              alert and send them a new link.
            </p>
          </Section>

          <Section id="api" title="REST API">
            <p>
              Base URL <C>{API}</C>. Create an API key in <b className="text-l-text">Settings → API keys</b> (member keys read, admin keys can
              change things) and send it as a bearer token:
            </p>
            <Code title="curl">{`curl ${API}/orgs/<org id>/events?contract_status=breaking \\
  -H "Authorization: Bearer rk_..."`}</Code>
            <p>
              Errors look like <C>{'{"error": {"code": "not_found", "message": "..."}}'}</C>. Lists return <C>{'{"data": [...]}'}</C>; events also
              return <C>next_cursor</C>: pass it back as <C>cursor</C> for the next page. Get your org ID from <C>GET /v1/me</C>.
            </p>
            <Table
              head={['Resource', 'Endpoints (under /v1/orgs/{org})']}
              rows={[
                ['Projects', <><C>GET/POST /projects</C>, <C>GET/DELETE /projects/{'{id}'}</C></>],
                ['Webhooks', <><C>GET/POST /webhooks</C>, <C>GET/PATCH/DELETE /webhooks/{'{id}'}</C>, <C>POST …/rotate-url</C></>],
                ['Test events', <><C>GET /webhooks/{'{id}'}/samples</C>, <C>POST /webhooks/{'{id}'}/simulate</C> (admin)</>],
                ['Events', <><C>GET /events</C> (filters: <C>webhook_id</C>, <C>type</C>, <C>status</C>, <C>contract_status</C>, <C>since</C>, <C>until</C>), <C>GET /events/{'{id}'}</C>, <C>GET …/raw</C> (unmasked, admin)</>],
                ['Destinations', <><C>GET/POST /webhooks/{'{id}'}/destinations</C>, <C>PATCH/DELETE /destinations/{'{id}'}</C>, <C>POST …/test</C>, <C>POST …/rotate-secret</C> (<C>event_types</C> limits which types it takes)</>],
                ['Deliveries', <><C>GET /deliveries</C>, <C>GET /deliveries/{'{id}'}</C>, <C>POST /deliveries/{'{id}'}/retry</C></>],
                ['Contracts', <><C>GET /contracts</C>, <C>GET /contracts/{'{id}'}</C>, <C>POST …/versions</C>, <C>POST …/relearn</C></>],
                ['Incidents', <><C>GET /incidents</C>, <C>POST …/resolve</C>, <C>GET/POST …/replay</C>, <C>GET …/repair-suggestion</C></>],
                ['Repair rules', <><C>GET/POST /repair-rules</C>, <C>PATCH/DELETE /repair-rules/{'{id}'}</C>, <C>POST /repair-rules/preview</C></>],
                ['Alerts', <><C>GET/POST /alert-channels</C> (Slack, email, webhook, Jira), <C>PATCH/DELETE /alert-channels/{'{id}'}</C>, <C>POST …/test</C>, <C>GET /alerts</C></>],
                ['Outbound', <><C>POST /outbound/messages</C>, <C>GET/POST /outbound/apps</C>, <C>GET/DELETE /outbound/apps/{'{app}'}</C>, <C>POST …/portal-link</C>, <C>…/endpoints</C>, <C>GET/POST/DELETE /outbound/event-types</C></>],
                ['Connections', <><C>GET/POST /integrations</C>, <C>POST /connect-sessions</C>, <C>GET /connections</C>, <C>GET …/token</C>, <C>POST …/refresh</C>, <C>{'…/connections/{id}/proxy/<path>'}</C>, <C>GET /proxy-calls</C></>],
                ['Syncs', <><C>GET/POST /syncs</C>, <C>PATCH/DELETE /syncs/{'{id}'}</C>, <C>POST …/run</C>, <C>GET …/runs</C>; models at <C>GET /v1/connect/sync-models</C></>],
                ['Team', <><C>GET/POST /members</C>, <C>GET/POST/DELETE /api-keys</C>, <C>GET /audit-logs</C></>],
              ]}
            />
          </Section>

          <Section id="limits" title="Limits">
            <Table
              head={['What', 'Limit']}
              rows={[
                ['Event body', '5 MB'],
                ['Events per webhook URL', '100 a second, bursts of 1,000 (over it: 429 with Retry-After; providers retry)'],
                ['API requests', '600 a minute per user or API key'],
                ['Sign-in', '10 failed attempts per account per 15 minutes'],
                ['Event payloads kept', '30 days (incidents, contracts and the audit log are kept)'],
              ]}
            />
            <p>
              Something not covered here? See <Link to="/security" className="text-l-accent underline-offset-4 hover:underline">security</Link> and{' '}
              <Link to="/status" className="text-l-accent underline-offset-4 hover:underline">system status</Link>.
            </p>
          </Section>
        </article>
      </div>
    </PublicPage>
  )
}

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section id={id} className="scroll-mt-24 border-b border-l-border py-10 first:pt-0 last:border-0 [&_p]:my-3">
      <h2 className="mb-4 text-2xl font-semibold tracking-tight text-l-text">
        <a href={`#${id}`} className="hover:underline hover:underline-offset-4">{title}</a>
      </h2>
      {children}
    </section>
  )
}

const sdks: { id: string; label: string; install: string; code: string }[] = [
  {
    id: 'node',
    label: 'Node.js',
    install: 'npm install relaya-node',
    code: `import express from 'express'
import { relayaMiddleware } from 'relaya-node'

const app = express()
// Mount before express.json(): the signature needs the raw body.
app.post('/webhooks/relaya', relayaMiddleware({ secret: process.env.RELAYA_SIGNING_SECRET }), async (req, res) => {
  const delivery = req.relaya
  if (await alreadyProcessed(delivery.idempotencyKey)) return res.sendStatus(200)
  await handle(delivery.json())
  res.sendStatus(200)
})`,
  },
  {
    id: 'python',
    label: 'Python',
    install: 'pip install relaya',
    code: `import os

from flask import Flask, request
from relaya import verify_delivery, WebhookVerificationError

app = Flask(__name__)

@app.post("/webhooks/relaya")
def relaya_webhook():
    try:
        delivery = verify_delivery(request.get_data(), request.headers, os.environ["RELAYA_SIGNING_SECRET"])
    except WebhookVerificationError as e:
        return {"error": e.reason}, 400
    handle(delivery.json())
    return "", 200`,
  },
  {
    id: 'php',
    label: 'PHP / Laravel',
    install: 'composer require relayaa/relaya-php',
    code: `// config/services.php
'relaya' => ['signing_secret' => env('RELAYA_SIGNING_SECRET')],

// routes/api.php
Route::post('/webhooks/relaya', function (Request $request) {
    $delivery = $request->attributes->get('relaya'); // Relaya\\Delivery
    ProcessPayment::dispatch($delivery->json());
    return response()->noContent();
})->middleware(\\Relaya\\Laravel\\VerifyRelayaSignature::class);`,
  },
  {
    id: 'go',
    label: 'Go',
    install: 'go get github.com/relayaa/relaya-sdks/go',
    code: `import relaya "github.com/relayaa/relaya-sdks/go"

secret := relaya.Secret(os.Getenv("RELAYA_SIGNING_SECRET"))
http.Handle("/webhooks/relaya", relaya.Middleware(secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	d, _ := relaya.DeliveryFromContext(r.Context())
	var event map[string]any
	d.JSON(&event)
	// dedupe on d.IdempotencyKey, then handle the event
})))`,
  },
  {
    id: 'java',
    label: 'Java',
    install: 'io.github.relayaa:relaya-java (Maven)',
    code: `@PostMapping("/webhooks/relaya")
ResponseEntity<?> receive(@RequestBody byte[] body, @RequestHeader HttpHeaders headers) {
    Delivery delivery;
    try {
        delivery = Webhook.verifyDelivery(body, headers, System.getenv("RELAYA_SIGNING_SECRET"));
    } catch (WebhookVerificationException e) {
        return ResponseEntity.badRequest().body(Map.of("error", e.reason()));
    }
    handle(delivery.json());
    return ResponseEntity.ok().build();
}`,
  },
]

function SdkTabs() {
  const [tab, setTab] = useState(sdks[0].id)
  const s = sdks.find((x) => x.id === tab)!
  return (
    <div className="mt-4">
      <div role="tablist" aria-label="Language" className="flex flex-wrap gap-1 rounded-lg border border-l-border p-1 text-sm">
        {sdks.map((x) => (
          <button
            key={x.id}
            role="tab"
            type="button"
            aria-selected={tab === x.id}
            onClick={() => setTab(x.id)}
            className={cn('rounded-md px-3 py-1.5 transition hover:text-l-text', tab === x.id && 'bg-l-soft font-medium text-l-text')}
          >
            {x.label}
          </button>
        ))}
      </div>
      <Code title="Install">{s.install}</Code>
      <Code title="Verify and handle deliveries">{s.code}</Code>
    </div>
  )
}

/** The section currently at the top of the viewport, for highlighting the table of contents. */
function useActiveSection(ids: string[]) {
  const [active, setActive] = useState(ids[0])
  useEffect(() => {
    const obs = new IntersectionObserver(
      (entries) => {
        const visible = entries.filter((e) => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]) setActive(visible[0].target.id)
      },
      { rootMargin: '-80px 0px -60% 0px' },
    )
    ids.forEach((id) => {
      const el = document.getElementById(id)
      if (el) obs.observe(el)
    })
    return () => obs.disconnect()
  }, [ids])
  return active
}
