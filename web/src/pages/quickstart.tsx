import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { CopyButton, PageHeader } from '@/components/common'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { PUBLIC_API } from '@/lib/api'
import { cn } from '@/lib/utils'

type Lang = 'node' | 'python' | 'php'

const LANGS: { key: Lang; label: string; install: string }[] = [
  { key: 'node', label: 'Node.js', install: 'npm install relaya-node' },
  { key: 'python', label: 'Python', install: 'pip install relaya' },
  { key: 'php', label: 'PHP / Laravel', install: 'composer require relayaa/relaya-php' },
]

function savedLang(): Lang {
  try {
    const v = localStorage.getItem('relaya.quickstart.lang')
    if (v === 'node' || v === 'python' || v === 'php') return v
  } catch {
    // storage unavailable: default
  }
  return 'node'
}

function Code({ children }: { children: string }) {
  return (
    <div className="min-w-0">
      <pre className="overflow-x-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed">{children}</pre>
      <div className="mt-2 flex justify-end">
        <CopyButton value={children} />
      </div>
    </div>
  )
}

function Step({ n, title, description, children }: { n: number; title: string; description: ReactNode; children: ReactNode }) {
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <span className="flex size-6 shrink-0 items-center justify-center rounded-full bg-brand/15 text-xs font-semibold text-brand">{n}</span>
          {title}
        </CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="grid min-w-0 gap-3">{children}</CardContent>
    </Card>
  )
}

export function QuickstartPage() {
  const [lang, setLangState] = useState<Lang>(savedLang)
  const setLang = (l: Lang) => {
    setLangState(l)
    try {
      localStorage.setItem('relaya.quickstart.lang', l)
    } catch {
      // not saved: fine
    }
  }
  const origin = location.origin
  const base = PUBLIC_API
  const info = LANGS.find((l) => l.key === lang)!

  const client = {
    node: `import { Relaya } from 'relaya-node'

const relaya = new Relaya({ apiKey: process.env.RELAYA_API_KEY${base ? `, baseUrl: '${base}'` : ''} })`,
    python: `from relaya import Relaya

relaya = Relaya(${base ? `base_url="${base}"` : ''})  # reads RELAYA_API_KEY`,
    php: `$relaya = new Relaya\\Client(getenv('RELAYA_API_KEY')${base ? `, ['base_url' => '${base}']` : ''});`,
  }[lang]

  const receive = {
    node: `// Express: mount before express.json() for this route (it needs the raw body)
import { relayaMiddleware } from 'relaya-node'

app.post('/webhooks/relaya', relayaMiddleware({ secret: process.env.RELAYA_SIGNING_SECRET }), async (req, res) => {
  const delivery = req.relaya                       // signature checked; bad ones get a 400
  if (await alreadyProcessed(delivery.idempotencyKey)) return res.sendStatus(200)
  const event = delivery.json()                     // e.g. { type: 'payment.captured', ... }
  await handle(event)
  res.sendStatus(200)                               // anything else: Relaya retries
})`,
    python: `# Flask (Django, FastAPI: same idea; always pass the raw body)
from relaya import verify_delivery, WebhookVerificationError

@app.post("/webhooks/relaya")
def relaya_webhook():
    try:
        delivery = verify_delivery(request.get_data(), request.headers, os.environ["RELAYA_SIGNING_SECRET"])
    except WebhookVerificationError as e:
        return {"error": e.reason}, 400
    if not already_processed(delivery.idempotency_key):
        handle(delivery.json())
    return "", 200   # anything else: Relaya retries`,
    php: `// Laravel: config/services.php → 'relaya' => ['signing_secret' => env('RELAYA_SIGNING_SECRET')]
use Relaya\\Laravel\\VerifyRelayaSignature;

Route::post('/webhooks/relaya', function (Request $request) {
    $delivery = $request->attributes->get('relaya');   // signature checked; bad ones get a 400
    if (Cache::add("relaya:{$delivery->idempotencyKey}", true, now()->addDay())) {
        HandleEvent::dispatch($delivery->json());
    }
    return response()->noContent();                     // anything else: Relaya retries
})->middleware(VerifyRelayaSignature::class);`,
  }[lang]

  const link = {
    node: `// Your backend: a one-time link for the signed-in user
app.post('/integrations/zoho/link', async (req, res) => {
  const link = await relaya.connections.createLink({ integration: 'zoho', end_user_id: String(req.user.id) })
  res.json({ url: link.url })
})`,
    python: `# Your backend: a one-time link for the signed-in user
@app.post("/integrations/zoho/link")
def zoho_link():
    link = relaya.connections.create_link("zoho", end_user_id=str(current_user.id))
    return {"url": link["url"]}`,
    php: `// Your backend: a one-time link for the signed-in user
Route::post('/integrations/zoho/link', function (Request $request) use ($relaya) {
    $link = $relaya->connections->createLink('zoho', (string) $request->user()->id);
    return ['url' => $link['url']];
});`,
  }[lang]

  const frontend = `<!-- Your frontend: a popup; resolves once they've connected -->
<script src="${origin}/connect.js"></script>
<button id="connect-zoho">Connect Zoho</button>
<script>
  document.getElementById('connect-zoho').onclick = async () => {
    const { url } = await fetch('/integrations/zoho/link', { method: 'POST' }).then((r) => r.json())
    try {
      const { connectionId } = await Relaya.connect(url)
      // connected: refresh your UI
    } catch (err) {
      // err.code: closed | expired | failed | popup_blocked
    }
  }
</script>`

  const call = {
    node: `// Call Zoho as that user: Relaya adds and renews the token, retries what is safe to retry
const conn = await relaya.connections.find('zoho', String(user.id))
const res = await relaya.proxy(conn.id).get('/crm/v2/Leads', { query: { per_page: 10 } })
if (res.ok) console.log(res.data)
// A RelayaError with code 'connection_broken' means: send the user a new link.

// Or get new and changed records as events (zoho.lead.created / .updated), delivered to step 1
await relaya.syncs.create({ connection_id: conn.id, model: 'zoho.crm_records', config: { module: 'Leads' } })`,
    python: `# Call Zoho as that user: Relaya adds and renews the token, retries what is safe to retry
conn = relaya.connections.find("zoho", str(user.id))
res = relaya.proxy(conn["id"]).get("/crm/v2/Leads", params={"per_page": 10})
if res.ok:
    print(res.data)
# RelayaError with code "connection_broken" means: send the user a new link.

# Or get new and changed records as events (zoho.lead.created / .updated), delivered to step 1
relaya.syncs.create(conn["id"], "zoho.crm_records", {"module": "Leads"})`,
    php: `// Call Zoho as that user: Relaya adds and renews the token, retries what is safe to retry
$conn = $relaya->connections->find('zoho', (string) $user->id);
$res = $relaya->proxy($conn['id'])->get('/crm/v2/Leads', ['query' => ['per_page' => 10]]);
if ($res->ok) { print_r($res->data); }
// RelayaException with errorCode 'connection_broken' means: send the user a new link.

// Or get new and changed records as events (zoho.lead.created / .updated), delivered to step 1
$relaya->syncs->create($conn['id'], 'zoho.crm_records', ['module' => 'Leads']);`,
  }[lang]

  return (
    <>
      <PageHeader
        title="Quickstart"
        description="Everything a developer needs to plug Relaya into your app: receive events safely, let your users connect their accounts, and call those apps. Copy, paste, done."
      />

      <div className="flex flex-wrap gap-2" role="tablist" aria-label="Language">
        {LANGS.map((l) => (
          <button
            key={l.key}
            type="button"
            role="tab"
            aria-selected={lang === l.key}
            onClick={() => setLang(l.key)}
            className={cn(
              'rounded-md border px-3 py-1.5 text-sm transition-colors',
              lang === l.key ? 'border-brand bg-brand/10 font-medium text-foreground' : 'text-muted-foreground hover:bg-muted',
            )}
          >
            {l.label}
          </button>
        ))}
      </div>

      <Step
        n={1}
        title="Install and set your key"
        description={
          <>
            Create an API key in{' '}
            <Link to="/settings/api-keys" className="text-brand hover:underline">
              Settings → API keys
            </Link>{' '}
            (admin role for connections) and set it as <code className="font-mono text-xs">RELAYA_API_KEY</code> on your server. Never put it in
            frontend code.
          </>
        }
      >
        <Code>{info.install}</Code>
        <Code>{client}</Code>
      </Step>

      <Step
        n={2}
        title="Receive events"
        description={
          <>
            Add your endpoint as a destination on a{' '}
            <Link to="/webhooks" className="text-brand hover:underline">
              webhook
            </Link>
            ; Relaya shows its signing secret once (<code className="font-mono text-xs">RELAYA_SIGNING_SECRET</code>). Every event is signed, retried
            until you answer 2xx, and can be replayed.
          </>
        }
      >
        <Code>{receive}</Code>
      </Step>

      <Step
        n={3}
        title="Let your users connect their accounts"
        description={
          <>
            Add the app once under{' '}
            <Link to="/connections" className="text-brand hover:underline">
              Connections
            </Link>{' '}
            (your OAuth app for Zoho, HubSpot or Google). Then a button in your app opens a popup; Relaya handles the sign-in and keeps the tokens
            fresh.
          </>
        }
      >
        <Code>{link}</Code>
        <Code>{frontend}</Code>
      </Step>

      <Step n={4} title="Call their apps, or get their changes as events" description="No tokens in your code: Relaya adds them, renews them and alerts you if a connection breaks.">
        <Code>{call}</Code>
      </Step>
    </>
  )
}
