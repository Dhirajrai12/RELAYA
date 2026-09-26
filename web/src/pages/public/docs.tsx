import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { cn } from '@/lib/utils'
import { C, Code, PublicPage, Table } from './layout'

const API = 'https://server.aegonassett.com/api/v1'

const toc = [
  { id: 'quick-start', label: 'Quick start' },
  { id: 'receive', label: 'Receive events' },
  { id: 'sdks', label: 'SDKs' },
  { id: 'retries', label: 'Retries and idempotency' },
  { id: 'contracts', label: 'Contracts and incidents' },
  { id: 'repair', label: 'Repair rules and replay' },
  { id: 'alerts', label: 'Alerts' },
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
      intro="Everything you need to route a provider's webhooks through Relaya, forward them to your code, and recover when something breaks."
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
                <b className="text-l-text">project</b>, then a <b className="text-l-text">webhook</b>. Pick the provider (Razorpay, Stripe,
                Shopify, GitHub or generic HMAC) and paste its signing secret, so Relaya can verify what arrives.
              </li>
              <li>
                Give the provider your webhook's URL instead of your own endpoint:
                <Code title="Webhook URL">{`https://server.aegonassett.com/v1/in/<your webhook token>`}</Code>
                Every event is stored, checked and shown in <b className="text-l-text">Events</b> within milliseconds.
              </li>
              <li>
                Add a <b className="text-l-text">destination</b>: your endpoint's public HTTPS URL. Relaya forwards each accepted event there,
                with retries. Copy the signing secret it shows once.
              </li>
              <li>In your endpoint, verify Relaya's signature (below) and answer 2xx once you've handled the event.</li>
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
                [<C>Relaya-Signature</C>, <>Proves the request came from Relaya (see below).</>],
                [<C>Idempotency-Key</C>, <>The same on every retry and replay of one delivery. <b className="text-l-text">Dedupe on this.</b></>],
                [<C>Relaya-Event-Id</C>, <>The event's ID, to look it up in the dashboard or API.</>],
                [<C>Relaya-Delivery-Id</C>, <>This delivery's ID.</>],
                [<C>Relaya-Attempt</C>, <>1 on the first try, then 2, 3… on retries.</>],
                [<C>Relaya-Event-Type</C>, <>For example <C>payment.captured</C>, when Relaya could tell.</>],
                [<C>Relaya-Replay</C>, <>Set when the request is part of an incident replay.</>],
                [<C>Relaya-Repaired</C>, <>Set when repair rules changed the body (the provider's own signature headers are then left out).</>],
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
            <p>Each SDK verifies deliveries and wraps the REST API. Source and full READMEs: <a className="text-l-accent underline-offset-4 hover:underline" href="https://github.com/Dhirajrai12/relaya-sdks" target="_blank" rel="noreferrer">github.com/Dhirajrai12/relaya-sdks</a>.</p>
            <SdkTabs />
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
              In <b className="text-l-text">Settings → Alerts</b>, send alerts to Slack (an incoming webhook), a teammate's email or your own
              endpoint (signed like deliveries). Choose which: incident opened or resolved, a destination failing (3 failures in a row) or
              recovered, and signature failures. You get one alert per incident, not one per event.
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
                ['Events', <><C>GET /events</C> (filters: <C>webhook_id</C>, <C>type</C>, <C>status</C>, <C>contract_status</C>, <C>since</C>, <C>until</C>), <C>GET /events/{'{id}'}</C></>],
                ['Destinations', <><C>GET/POST /webhooks/{'{id}'}/destinations</C>, <C>PATCH/DELETE /destinations/{'{id}'}</C>, <C>POST …/test</C>, <C>POST …/rotate-secret</C></>],
                ['Deliveries', <><C>GET /deliveries</C>, <C>GET /deliveries/{'{id}'}</C>, <C>POST /deliveries/{'{id}'}/retry</C></>],
                ['Contracts', <><C>GET /contracts</C>, <C>GET /contracts/{'{id}'}</C>, <C>POST …/versions</C>, <C>POST …/relearn</C></>],
                ['Incidents', <><C>GET /incidents</C>, <C>POST …/resolve</C>, <C>GET/POST …/replay</C>, <C>GET …/repair-suggestion</C></>],
                ['Repair rules', <><C>GET/POST /repair-rules</C>, <C>PATCH/DELETE /repair-rules/{'{id}'}</C>, <C>POST /repair-rules/preview</C></>],
                ['Alerts', <><C>GET/POST /alert-channels</C>, <C>PATCH/DELETE /alert-channels/{'{id}'}</C>, <C>POST …/test</C>, <C>GET /alerts</C></>],
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
    install: 'composer require dhirajrai12/relaya-php',
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
    install: 'go get github.com/Dhirajrai12/relaya-sdks/go',
    code: `import relaya "github.com/Dhirajrai12/relaya-sdks/go"

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
    install: 'io.github.dhirajrai12:relaya-java (Maven)',
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
