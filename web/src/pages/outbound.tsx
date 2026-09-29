import { ArrowLeftIcon, EyeIcon, ExternalLinkIcon, LinkIcon, PlusIcon, SendIcon, Trash2Icon, ZapIcon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'

import { CopyButton, CopyField, EmptyState, ErrorState, PageHeader } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { EventTypePicker } from '@/components/event-type-picker'
import { Pager, usePaged } from '@/components/pager'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage, PUBLIC_API } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { dateTime, timeAgo } from '@/lib/format'
import {
  fetchOutboundEndpointSecret,
  useCreateOutboundApp,
  useCreateOutboundEndpoint,
  useCreatePortalLink,
  useDeleteOutboundApp,
  useDeleteOutboundEndpoint,
  useDeleteOutboundEventType,
  useOutboundApp,
  useOutboundApps,
  useOutboundEventTypes,
  useSaveOutboundEventType,
  useSendOutboundMessage,
  useTestOutboundEndpoint,
  useUpdateOutboundEndpoint,
} from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { EndpointTestResult, OutboundEndpoint } from '@/lib/types'

const green = 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
const red = 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400'

// ---- list ---------------------------------------------------------------------------

export function OutboundPage() {
  const apps = useOutboundApps()
  const canManage = useCanManage()
  const [adding, setAdding] = useState(false)
  const list = apps.data?.data ?? []
  const paged = usePaged(list)

  return (
    <>
      <PageHeader
        title="Outbound webhooks"
        description="Send events to your customers' servers: signed, retried and logged. Each customer (an app) manages its own endpoints in a portal you link from your product."
        actions={
          canManage && (
            <Button onClick={() => setAdding(true)}>
              <PlusIcon /> Add app
            </Button>
          )
        }
      />

      {apps.error ? (
        <ErrorState error={apps.error} />
      ) : apps.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : list.length === 0 ? (
        <EmptyState
          title="No apps yet"
          icon={SendIcon}
          action={
            canManage && (
              <Button onClick={() => setAdding(true)}>
                <PlusIcon /> Add app
              </Button>
            )
          }
        >
          An app is one of your customers. Your backend usually creates them with the API when a customer signs up.
        </EmptyState>
      ) : (
        <div className="rounded-lg border">
          <ul className="divide-y">
            {paged.items.map((a) => (
              <li key={a.id}>
                <Link to={`/outbound/${encodeURIComponent(a.uid)}`} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 hover:bg-muted/40">
                  <div className="min-w-0 flex-1 basis-48">
                    <div className="truncate font-medium">{a.name}</div>
                    <div className="truncate font-mono text-xs text-muted-foreground">{a.uid}</div>
                  </div>
                  <div className="flex flex-wrap gap-x-4 text-xs text-muted-foreground">
                    <span>
                      {a.endpoints} endpoint{a.endpoints === 1 ? '' : 's'}
                    </span>
                    <span>{a.messages_24h} messages (24h)</span>
                    {a.failed_24h > 0 && <span className="text-red-700 dark:text-red-400">{a.failed_24h} failed</span>}
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          <Pager paged={paged} noun="apps" />
        </div>
      )}

      <EventTypesCard />
      <OutboundUsageCard />

      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="sm:max-w-md">{adding && <AddAppForm onClose={() => setAdding(false)} />}</DialogContent>
      </Dialog>
    </>
  )
}

function AddAppForm({ onClose }: { onClose: () => void }) {
  const create = useCreateOutboundApp()
  const navigate = useNavigate()
  const [uid, setUid] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setError('')
    try {
      const a = await create.mutateAsync({ uid: uid.trim(), name: name.trim() || undefined })
      onClose()
      navigate(`/outbound/${encodeURIComponent(a.uid)}`)
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  return (
    <form onSubmit={onSubmit} className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>Add an app</DialogTitle>
        <DialogDescription>One of your customers, who will receive your webhooks.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor="oa-uid">ID</Label>
        <Input id="oa-uid" value={uid} onChange={(e) => setUid(e.target.value)} required maxLength={128} placeholder="customer-123" className="font-mono" />
        <p className="text-xs text-muted-foreground">Your ID for this customer; your code sends messages to it.</p>
      </div>
      <div className="grid gap-2">
        <Label htmlFor="oa-name">Name</Label>
        <Input id="oa-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={100} placeholder="Acme Corp" />
      </div>
      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={create.isPending || !uid.trim()}>
          {create.isPending ? 'Adding…' : 'Add app'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function EventTypesCard() {
  const types = useOutboundEventTypes()
  const canManage = useCanManage()
  const save = useSaveOutboundEventType()
  const remove = useDeleteOutboundEventType()
  const [name, setName] = useState('')
  const [desc, setDesc] = useState('')
  const rows = types.data?.data ?? []
  const paged = usePaged(rows, 10)
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Event types</CardTitle>
        <CardDescription>What you send. Customers pick from these in the portal. Types you send are added here automatically.</CardDescription>
      </CardHeader>
      <CardContent className="grid min-w-0 gap-3">
        {rows.length > 0 && (
          <div className="rounded-lg border">
            <ul className="divide-y text-sm">
              {paged.items.map((t) => (
                <li key={t.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
                  <code className="font-mono text-xs">{t.name}</code>
                  <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{t.description}</span>
                  {canManage && (
                    <ConfirmButton
                      variant="ghost"
                      destructive
                      title={`Remove ${t.name}?`}
                      description="It disappears from the portal's list. Endpoints subscribed to it keep receiving it."
                      confirmLabel="Remove"
                      onConfirm={() => remove.mutateAsync(t.name).then(() => toast.success('Removed'))}
                    >
                      <Trash2Icon />
                    </ConfirmButton>
                  )}
                </li>
              ))}
            </ul>
            <Pager paged={paged} noun="types" />
          </div>
        )}
        {canManage && (
          <form
            className="grid gap-2 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)_auto]"
            onSubmit={(e) => {
              e.preventDefault()
              save.mutateAsync({ name: name.trim(), description: desc.trim() }).then(
                () => {
                  setName('')
                  setDesc('')
                  toast.success('Saved')
                },
                (err) => toast.error(errorMessage(err)),
              )
            }}
          >
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="invoice.paid" className="font-mono text-xs" required />
            <Input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="An invoice was paid" maxLength={300} />
            <Button type="submit" variant="outline" disabled={save.isPending || !name.trim()}>
              Add type
            </Button>
          </form>
        )}
      </CardContent>
    </Card>
  )
}

function OutboundUsageCard() {
  const orgId = useAuth((s) => s.orgId)
  const api = `${PUBLIC_API}/v1/orgs/${orgId}/outbound`
  const snippet = `# When a customer signs up: create their app (your ID for them)
curl -X POST ${api}/apps -H "Authorization: Bearer $RELAYA_API_KEY" \\
  -d '{"uid":"customer-123","name":"Acme Corp"}'

# Whenever something happens: send a message. Relaya signs it, retries it and logs it
# for every endpoint of that customer subscribed to the type.
curl -X POST ${api}/messages -H "Authorization: Bearer $RELAYA_API_KEY" \\
  -d '{"app":"customer-123","event_type":"invoice.paid","payload":{"invoice_id":"in_1","amount":1999},"idempotency_key":"in_1-paid"}'

# In your product's settings page: a portal link where the customer manages their endpoints (24 hours)
curl -X POST ${api}/apps/customer-123/portal-link -H "Authorization: Bearer $RELAYA_API_KEY"

# Your customers verify requests with any Standard Webhooks library (standardwebhooks.com):
#   headers webhook-id, webhook-timestamp, webhook-signature; secret whsec_… from the portal`
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Send from your backend</CardTitle>
        <CardDescription>Messages use the Standard Webhooks format, so your customers can verify them in any language.</CardDescription>
      </CardHeader>
      <CardContent className="min-w-0">
        <pre className="overflow-x-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed">{snippet}</pre>
        <div className="mt-2 flex justify-end">
          <CopyButton value={snippet} />
        </div>
      </CardContent>
    </Card>
  )
}

// ---- one app ----------------------------------------------------------------------------

export function OutboundAppPage() {
  const { app: ref = '' } = useParams()
  const q = useOutboundApp(ref)
  const canManage = useCanManage()
  const remove = useDeleteOutboundApp()
  const navigate = useNavigate()
  const [adding, setAdding] = useState(false)
  const [portalOpen, setPortalOpen] = useState(false)

  if (q.error) return <ErrorState error={q.error} />
  if (q.isPending) return <Skeleton className="h-64 w-full" />
  const { app, endpoints } = q.data

  return (
    <>
      <Link to="/outbound" className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeftIcon className="size-4" /> Outbound
      </Link>
      <PageHeader
        title={app.name}
        description={`${app.uid} · ${app.messages_24h} messages in 24h${app.failed_24h ? ` · ${app.failed_24h} failed` : ''}`}
        actions={
          canManage && (
            <>
              <Button variant="outline" onClick={() => setPortalOpen(true)}>
                <LinkIcon /> Portal link
              </Button>
              <ConfirmButton
                variant="ghost"
                destructive
                title={`Delete ${app.name}?`}
                description="Its endpoints and message history are deleted. Messages sent to it afterwards are refused."
                confirmLabel="Delete app"
                onConfirm={() => remove.mutateAsync(app.uid).then(() => navigate('/outbound'))}
              >
                <Trash2Icon />
              </ConfirmButton>
            </>
          )
        }
      />

      <Card>
        <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 flex-1 basis-64">
            <CardTitle>Endpoints</CardTitle>
            <CardDescription className="mt-1.5">Where this customer receives your events. They can also manage these themselves in the portal.</CardDescription>
          </div>
          {canManage && (
            <Button size="sm" onClick={() => setAdding(true)}>
              <PlusIcon /> Add endpoint
            </Button>
          )}
        </CardHeader>
        <CardContent>
          {endpoints.length === 0 ? (
            <p className="text-sm text-muted-foreground">No endpoints yet: messages to this app are stored but not sent anywhere.</p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {endpoints.map((ep) => (
                <EndpointRow key={ep.id} app={app.uid} ep={ep} />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <SendTestCard app={app.uid} webhookId={app.webhook_id} />

      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="sm:max-w-lg">{adding && <AddEndpointForm app={app.uid} onClose={() => setAdding(false)} />}</DialogContent>
      </Dialog>
      <Dialog open={portalOpen} onOpenChange={setPortalOpen}>
        <DialogContent className="sm:max-w-lg">{portalOpen && <PortalLinkPanel app={app.uid} name={app.name} />}</DialogContent>
      </Dialog>
    </>
  )
}

function EndpointRow({ app, ep }: { app: string; ep: OutboundEndpoint }) {
  const canManage = useCanManage()
  const orgId = useAuth((s) => s.orgId)!
  const update = useUpdateOutboundEndpoint(app)
  const remove = useDeleteOutboundEndpoint(app)
  const test = useTestOutboundEndpoint(app)
  const [secret, setSecret] = useState<string | null>(null)
  const [result, setResult] = useState<EndpointTestResult | null>(null)
  return (
    <li className="p-3">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 break-all font-mono text-xs">{ep.url}</span>
            {!ep.enabled && <Badge variant="outline">disabled</Badge>}
          </div>
          {ep.description && <div className="mt-0.5 text-sm">{ep.description}</div>}
          <div className="mt-1 flex flex-wrap gap-1">
            {ep.event_types.length === 0 ? (
              <span className="text-xs text-muted-foreground">all event types</span>
            ) : (
              ep.event_types.map((t) => (
                <span key={t} className="rounded border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                  {t}
                </span>
              ))
            )}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
            <span>{ep.succeeded_24h} delivered (24h)</span>
            {ep.failed_24h > 0 && <span className="text-red-700 dark:text-red-400">{ep.failed_24h} failed</span>}
            {ep.retrying > 0 && <span className="text-amber-700 dark:text-amber-400">{ep.retrying} retrying</span>}
            {ep.last_success_at && <span title={dateTime(ep.last_success_at)}>last OK {timeAgo(ep.last_success_at)}</span>}
          </div>
        </div>
        {canManage && (
          <div className="flex flex-wrap items-center gap-1">
            <Button
              size="sm"
              variant="outline"
              disabled={test.isPending}
              onClick={() => test.mutateAsync(ep.id).then(setResult, (e) => toast.error(errorMessage(e)))}
            >
              <ZapIcon /> {test.isPending ? 'Sending…' : 'Test'}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              aria-label="Signing secret"
              title="Signing secret"
              onClick={() => fetchOutboundEndpointSecret(orgId, app, ep.id).then((r) => setSecret(r.signing_secret), (e) => toast.error(errorMessage(e)))}
            >
              <EyeIcon />
            </Button>
            <Button size="sm" variant="ghost" onClick={() => update.mutateAsync({ id: ep.id, enabled: !ep.enabled }).catch((e) => toast.error(errorMessage(e)))}>
              {ep.enabled ? 'Disable' : 'Enable'}
            </Button>
            <ConfirmButton
              variant="ghost"
              destructive
              title="Delete this endpoint?"
              description="It stops receiving events right away."
              confirmLabel="Delete endpoint"
              onConfirm={() => remove.mutateAsync(ep.id).then(() => toast.success('Endpoint deleted'))}
            >
              <Trash2Icon />
            </ConfirmButton>
          </div>
        )}
      </div>
      {secret && (
        <div className="mt-2 grid min-w-0 gap-1">
          <span className="text-xs text-muted-foreground">Signing secret (the receiver verifies webhook-signature with it):</span>
          <CopyField value={secret} />
        </div>
      )}
      {result && <TestResult r={result} onDismiss={() => setResult(null)} />}
    </li>
  )
}

export function TestResult({ r, onDismiss }: { r: EndpointTestResult; onDismiss: () => void }) {
  return (
    <div className={cn('mt-2 flex items-start justify-between gap-2 rounded-md border p-2 text-xs', r.ok ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-red-500/30 bg-red-500/5')}>
      <span className={cn('min-w-0 break-words', r.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-700 dark:text-red-400')}>
        {r.ok ? `Delivered: HTTP ${r.status_code} in ${r.duration_ms} ms` : `Failed: ${r.error || `HTTP ${r.status_code}`}`}
        {r.response_body && <span className="mt-0.5 block truncate font-mono text-muted-foreground">{r.response_body}</span>}
      </span>
      <button type="button" className="shrink-0 text-muted-foreground hover:text-foreground" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  )
}

function AddEndpointForm({ app, onClose }: { app: string; onClose: () => void }) {
  const create = useCreateOutboundEndpoint(app)
  const types = useOutboundEventTypes()
  const [url, setUrl] = useState('')
  const [desc, setDesc] = useState('')
  const [eventTypes, setEventTypes] = useState<string[]>([])
  const [secret, setSecret] = useState('')
  const [error, setError] = useState('')
  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setError('')
    try {
      const r = await create.mutateAsync({ url: url.trim(), description: desc.trim(), event_types: eventTypes })
      setSecret(r.signing_secret)
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  if (secret) {
    return (
      <div className="grid min-w-0 grid-cols-1 gap-4">
        <DialogHeader>
          <DialogTitle>Endpoint added</DialogTitle>
          <DialogDescription>Give this signing secret to whoever runs the endpoint. It can be shown again later.</DialogDescription>
        </DialogHeader>
        <CopyField value={secret} />
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </div>
    )
  }
  return (
    <form onSubmit={onSubmit} className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>Add an endpoint</DialogTitle>
        <DialogDescription>A URL of your customer's that receives your events.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor="oe-url">URL</Label>
        <Input id="oe-url" type="url" value={url} onChange={(e) => setUrl(e.target.value)} required placeholder="https://customer.example.com/webhooks" />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="oe-desc">Description (optional)</Label>
        <Input id="oe-desc" value={desc} onChange={(e) => setDesc(e.target.value)} maxLength={200} placeholder="Billing system" />
      </div>
      <div className="grid gap-2">
        <Label>Event types</Label>
        <EventTypePicker known={types.data?.data ?? []} value={eventTypes} onChange={setEventTypes} />
      </div>
      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={create.isPending || !url.trim()}>
          {create.isPending ? 'Adding…' : 'Add endpoint'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function PortalLinkPanel({ app, name }: { app: string; name: string }) {
  const create = useCreatePortalLink(app)
  const [link, setLink] = useState<{ url: string; expires_at: string } | null>(null)
  return (
    <div className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>Portal link for {name}</DialogTitle>
        <DialogDescription>
          A page where this customer adds and manages their own endpoints, sees deliveries and retries failures. Put a button in your product that asks your
          backend for a fresh link (POST …/portal-link) and opens it.
        </DialogDescription>
      </DialogHeader>
      {link ? (
        <>
          <CopyField value={link.url} />
          <p className="text-xs text-muted-foreground">Works until {dateTime(link.expires_at)}. Anyone with the link can manage this customer's endpoints.</p>
          <DialogFooter>
            <Button variant="outline" onClick={() => window.open(link.url, '_blank', 'noopener')}>
              <ExternalLinkIcon /> Open portal
            </Button>
          </DialogFooter>
        </>
      ) : (
        <DialogFooter>
          <Button disabled={create.isPending} onClick={() => create.mutateAsync(undefined).then(setLink, (e) => toast.error(errorMessage(e)))}>
            {create.isPending ? 'Creating…' : 'Create a 24-hour link'}
          </Button>
        </DialogFooter>
      )}
    </div>
  )
}

function SendTestCard({ app, webhookId }: { app: string; webhookId: string }) {
  const send = useSendOutboundMessage()
  const types = useOutboundEventTypes()
  const canManage = useCanManage()
  const [eventType, setEventType] = useState('')
  const [payload, setPayload] = useState('{\n  "id": "in_1",\n  "amount": 1999\n}')
  const [result, setResult] = useState('')
  if (!canManage) return null
  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setResult('')
    let data: unknown
    try {
      data = JSON.parse(payload)
    } catch {
      return toast.error('The payload is not valid JSON')
    }
    try {
      const r = await send.mutateAsync({ app, event_type: eventType.trim(), payload: data })
      setResult(r.endpoints ? `Sent to ${r.endpoints} endpoint${r.endpoints === 1 ? '' : 's'}.` : 'Stored, but no endpoint takes this type.')
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  const known = types.data?.data ?? []
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Send a message</CardTitle>
        <CardDescription>
          What your backend does with the API. Sent messages appear in{' '}
          <Link to={`/events?webhook_id=${webhookId}`} className="text-brand hover:underline">
            Events
          </Link>
          , with every delivery attempt.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={onSubmit} className="grid min-w-0 gap-3">
          <Input
            list="ob-types"
            value={eventType}
            onChange={(e) => setEventType(e.target.value)}
            placeholder="Event type, e.g. invoice.paid"
            className="font-mono text-xs"
            required
          />
          <datalist id="ob-types">
            {known.map((t) => (
              <option key={t.name} value={t.name} />
            ))}
          </datalist>
          <textarea
            aria-label="Payload"
            value={payload}
            onChange={(e) => setPayload(e.target.value)}
            rows={5}
            className="min-w-0 rounded-md border bg-transparent px-3 py-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
          />
          <div className="flex flex-wrap items-center gap-3">
            <Button type="submit" disabled={send.isPending || !eventType.trim()}>
              <SendIcon /> {send.isPending ? 'Sending…' : 'Send'}
            </Button>
            {result && <Badge variant="outline" className={result.startsWith('Sent') ? green : red}>{result}</Badge>}
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
