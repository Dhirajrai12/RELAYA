import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2Icon, EyeIcon, LockIcon, PlusIcon, RotateCcwIcon, Trash2Icon, TriangleAlertIcon, ZapIcon } from 'lucide-react'
import { useEffect, useState, type ReactNode, type SubmitEvent } from 'react'
import { toast } from 'sonner'

import { CopyButton, CopyField } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { EventTypePicker } from '@/components/event-type-picker'
import { LogoMark } from '@/components/logo'
import { Pager, usePaged } from '@/components/pager'
import { SimpleSelect } from '@/components/simple-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { API_BASE, ApiError, errorMessage } from '@/lib/api'
import { dateTime, timeAgo } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { EndpointTestResult, OutboundEndpoint, PortalDelivery, PortalInfo } from '@/lib/types'
import { TestResult } from '@/pages/outbound'

// The portal is used by our customers' customers: no Relaya login, the token
// in the URL fragment (#ps_…) is the credential. It never reaches server logs.

function portalToken() {
  return decodeURIComponent(location.hash.replace(/^#/, ''))
}

async function portalApi<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${API_BASE}/portal${path}`, {
    method,
    headers: { Authorization: `Bearer ${portalToken()}`, ...(body !== undefined && { 'Content-Type': 'application/json' }) },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new ApiError(res.status, data?.error?.code ?? 'error', data?.error?.message ?? `Request failed (${res.status})`)
  return data as T
}

function usePortalMutation<V, R>(fn: (v: V) => Promise<R>) {
  const qc = useQueryClient()
  return useMutation({ mutationFn: fn, onSuccess: () => qc.invalidateQueries({ queryKey: ['portal'] }) })
}

const statusStyle: Record<string, string> = {
  succeeded: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  failed: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400',
  retrying: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400',
  pending: 'border-border text-muted-foreground',
  in_flight: 'border-border text-muted-foreground',
}

export function PortalPage() {
  const info = useQuery({ queryKey: ['portal', 'app'], queryFn: () => portalApi<PortalInfo>('GET', '/app'), retry: false })
  useEffect(() => {
    document.title = info.data ? `Webhooks · ${info.data.org_name}` : 'Webhooks'
  }, [info.data])
  // A new link pasted into the same tab only changes the fragment: start over with it.
  useEffect(() => {
    const onHash = () => location.reload()
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  return (
    <div className="min-h-svh bg-muted/30">
      <div className="mx-auto max-w-4xl px-4 py-8 sm:py-12">
        {info.isPending ? (
          <Skeleton className="h-64 w-full" />
        ) : info.error ? (
          <Notice title={info.error instanceof ApiError && info.error.status === 401 ? 'This link has expired' : 'Something went wrong'}>
            {info.error instanceof ApiError && info.error.status === 401
              ? 'Portal links work for 24 hours. Open it again from the app that sent you here.'
              : errorMessage(info.error)}
          </Notice>
        ) : (
          <>
            <header className="mb-6">
              <p className="text-sm text-muted-foreground">{info.data.org_name}</p>
              <h1 className="text-xl font-semibold tracking-tight sm:text-2xl">Webhooks for {info.data.app.name}</h1>
              <p className="mt-1 text-sm text-muted-foreground">
                Add the URLs where {info.data.org_name} should send you events, choose which events, and see every delivery.
              </p>
            </header>
            <Endpoints info={info.data} />
            <Deliveries />
            <VerifyHelp org={info.data.org_name} />
          </>
        )}
        <p className="mt-8 flex items-center justify-center gap-1.5 text-xs text-muted-foreground">
          <LockIcon className="size-3" /> Webhooks delivered by <LogoMark className="size-4" /> Relaya
        </p>
      </div>
    </div>
  )
}

function Notice({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="mx-auto max-w-md rounded-xl border bg-card p-8 text-center">
      <div className="mx-auto mb-4 flex size-12 items-center justify-center rounded-full bg-red-500/10 text-red-600">
        <TriangleAlertIcon />
      </div>
      <h1 className="text-lg font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-muted-foreground">{children}</p>
    </div>
  )
}

function Endpoints({ info }: { info: PortalInfo }) {
  const eps = useQuery({ queryKey: ['portal', 'endpoints'], queryFn: () => portalApi<{ data: OutboundEndpoint[] }>('GET', '/endpoints') })
  const [editing, setEditing] = useState<OutboundEndpoint | 'new' | null>(null)
  const list = eps.data?.data ?? []
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <CardTitle>Endpoints</CardTitle>
          <CardDescription className="mt-1.5">Each one gets a POST for every event it subscribes to, retried for up to a day if it fails.</CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditing('new')}>
          <PlusIcon /> Add endpoint
        </Button>
      </CardHeader>
      <CardContent>
        {eps.isPending ? (
          <Skeleton className="h-20 w-full" />
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">No endpoints yet. Add the URL of the server that should receive the events.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {list.map((ep) => (
              <PortalEndpointRow key={ep.id} ep={ep} onEdit={() => setEditing(ep)} />
            ))}
          </ul>
        )}
      </CardContent>
      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          {editing && <EndpointForm key={editing === 'new' ? 'new' : editing.id} ep={editing} types={info.event_types} onClose={() => setEditing(null)} />}
        </DialogContent>
      </Dialog>
    </Card>
  )
}

function PortalEndpointRow({ ep, onEdit }: { ep: OutboundEndpoint; onEdit: () => void }) {
  const [secret, setSecret] = useState<string | null>(null)
  const [result, setResult] = useState<EndpointTestResult | null>(null)
  const test = usePortalMutation((id: string) => portalApi<EndpointTestResult>('POST', `/endpoints/${id}/test`))
  const toggle = usePortalMutation((v: { id: string; enabled: boolean }) => portalApi<OutboundEndpoint>('PATCH', `/endpoints/${v.id}`, { enabled: v.enabled }))
  const remove = usePortalMutation((id: string) => portalApi<void>('DELETE', `/endpoints/${id}`))
  const rotate = usePortalMutation((id: string) => portalApi<{ signing_secret: string }>('POST', `/endpoints/${id}/rotate-secret`))
  return (
    <li className="p-3">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 break-all font-mono text-xs">{ep.url}</span>
            {!ep.enabled && <Badge variant="outline">disabled</Badge>}
          </div>
          {ep.description && <div className="mt-0.5 text-sm">{ep.description}</div>}
          <div className="mt-1 text-xs text-muted-foreground">
            {ep.event_types.length === 0 ? 'All events' : ep.event_types.join(', ')} · {ep.succeeded_24h} delivered (24h)
            {ep.failed_24h > 0 && <span className="text-red-700 dark:text-red-400"> · {ep.failed_24h} failed</span>}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-1">
          <Button size="sm" variant="outline" disabled={test.isPending} onClick={() => test.mutateAsync(ep.id).then(setResult, (e) => toast.error(errorMessage(e)))}>
            <ZapIcon /> {test.isPending ? 'Sending…' : 'Send test'}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            aria-label="Signing secret"
            title="Signing secret"
            onClick={() =>
              portalApi<{ signing_secret: string }>('GET', `/endpoints/${ep.id}/secret`).then(
                (r) => setSecret(r.signing_secret),
                (e) => toast.error(errorMessage(e)),
              )
            }
          >
            <EyeIcon />
          </Button>
          <Button size="sm" variant="ghost" onClick={onEdit}>
            Edit
          </Button>
          <Button size="sm" variant="ghost" onClick={() => toggle.mutateAsync({ id: ep.id, enabled: !ep.enabled }).catch((e) => toast.error(errorMessage(e)))}>
            {ep.enabled ? 'Disable' : 'Enable'}
          </Button>
          <ConfirmButton
            variant="ghost"
            destructive
            title="Delete this endpoint?"
            description="It stops receiving events right away."
            confirmLabel="Delete"
            onConfirm={() => remove.mutateAsync(ep.id).then(() => toast.success('Endpoint deleted'))}
          >
            <Trash2Icon />
          </ConfirmButton>
        </div>
      </div>
      {secret && (
        <div className="mt-2 grid min-w-0 gap-1">
          <span className="text-xs text-muted-foreground">Signing secret: verify each request's webhook-signature with it.</span>
          <CopyField value={secret} />
          <div>
            <ConfirmButton
              variant="ghost"
              title="Rotate the signing secret?"
              description="Requests are signed with the new secret right away; update your server first, or verification fails."
              confirmLabel="Rotate"
              onConfirm={() => rotate.mutateAsync(ep.id).then((r) => setSecret(r.signing_secret))}
            >
              <RotateCcwIcon /> Rotate secret
            </ConfirmButton>
          </div>
        </div>
      )}
      {result && <TestResult r={result} onDismiss={() => setResult(null)} />}
    </li>
  )
}

function EndpointForm({ ep, types, onClose }: { ep: OutboundEndpoint | 'new'; types: PortalInfo['event_types']; onClose: () => void }) {
  const isNew = ep === 'new'
  const [url, setUrl] = useState(isNew ? '' : ep.url)
  const [desc, setDesc] = useState(isNew ? '' : ep.description)
  const [eventTypes, setEventTypes] = useState<string[]>(isNew ? [] : ep.event_types)
  const [secret, setSecret] = useState('')
  const [error, setError] = useState('')
  const save = usePortalMutation((v: { url: string; description: string; event_types: string[] }): Promise<OutboundEndpoint | { signing_secret: string }> =>
    isNew
      ? portalApi<{ endpoint: OutboundEndpoint; signing_secret: string }>('POST', '/endpoints', v)
      : portalApi<OutboundEndpoint>('PATCH', `/endpoints/${ep.id}`, v),
  )
  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setError('')
    try {
      const r = await save.mutateAsync({ url: url.trim(), description: desc.trim(), event_types: eventTypes })
      if ('signing_secret' in r) setSecret(r.signing_secret)
      else {
        toast.success('Saved')
        onClose()
      }
    } catch (err) {
      setError(errorMessage(err))
    }
  }
  if (secret) {
    return (
      <div className="grid min-w-0 grid-cols-1 gap-4">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CheckCircle2Icon className="size-5 text-emerald-600" /> Endpoint added
          </DialogTitle>
          <DialogDescription>Your server verifies each request with this signing secret. You can show it again later.</DialogDescription>
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
        <DialogTitle>{isNew ? 'Add an endpoint' : 'Edit endpoint'}</DialogTitle>
        <DialogDescription>An https URL on your server that accepts POST requests.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor="pe-url">URL</Label>
        <Input id="pe-url" type="url" value={url} onChange={(e) => setUrl(e.target.value)} required placeholder="https://yourcompany.com/webhooks" />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="pe-desc">Description (optional)</Label>
        <Input id="pe-desc" value={desc} onChange={(e) => setDesc(e.target.value)} maxLength={200} placeholder="Production" />
      </div>
      <div className="grid gap-2">
        <Label>Events</Label>
        <EventTypePicker known={types} value={eventTypes} onChange={setEventTypes} />
      </div>
      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={save.isPending || !url.trim()}>
          {save.isPending ? 'Saving…' : isNew ? 'Add endpoint' : 'Save'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function Deliveries() {
  const [status, setStatus] = useState('')
  const q = useInfiniteQuery({
    queryKey: ['portal', 'deliveries', status],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams()
      if (status) qs.set('status', status)
      if (pageParam) qs.set('before', pageParam)
      return portalApi<{ data: PortalDelivery[]; next_before: string | null }>('GET', `/deliveries?${qs}`)
    },
    getNextPageParam: (last) => last.next_before ?? undefined,
    refetchInterval: 15_000,
  })
  const retry = usePortalMutation((id: string) => portalApi<void>('POST', `/deliveries/${id}/retry`))
  const rows = q.data?.pages.flatMap((p) => p.data) ?? []
  const paged = usePaged(rows, 10, status)
  return (
    <Card className="mt-6">
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <CardTitle>Deliveries</CardTitle>
          <CardDescription className="mt-1.5">Every event sent to your endpoints, newest first. Failed ones can be sent again.</CardDescription>
        </div>
        <SimpleSelect
          className="w-40"
          value={status}
          onChange={setStatus}
          options={[
            { value: '', label: 'All' },
            { value: 'succeeded', label: 'Delivered' },
            { value: 'retrying', label: 'Retrying' },
            { value: 'failed', label: 'Failed' },
          ]}
        />
      </CardHeader>
      <CardContent>
        {q.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing yet.</p>
        ) : (
          <div className="rounded-lg border">
            <ul className="divide-y">
              {paged.items.map((d) => (
                <li key={d.id} className="flex flex-wrap items-start gap-x-3 gap-y-1 px-3 py-2 text-sm">
                  <div className="min-w-0 flex-1 basis-56">
                    <div className="flex flex-wrap items-center gap-2">
                      <code className="font-mono text-xs">{d.event_type}</code>
                      <Badge variant="outline" className={cn(statusStyle[d.status])}>
                        {d.status === 'succeeded' ? 'delivered' : d.status}
                      </Badge>
                      {d.last_status_code != null && <span className="text-xs text-muted-foreground">HTTP {d.last_status_code}</span>}
                    </div>
                    <div className="truncate text-xs text-muted-foreground" title={d.endpoint_url}>
                      <span title={dateTime(d.created_at)}>{timeAgo(d.created_at)}</span> · {d.attempts} attempt{d.attempts === 1 ? '' : 's'} · {d.endpoint_url}
                    </div>
                    {d.last_error && <div className="line-clamp-2 break-words text-xs text-red-700 dark:text-red-400">{d.last_error}</div>}
                    {d.status === 'retrying' && <div className="text-xs text-muted-foreground">next try {timeAgo(d.next_attempt_at)}</div>}
                    <div className="truncate font-mono text-[11px] text-muted-foreground" title="webhook-id">
                      {d.message_id}
                    </div>
                  </div>
                  {(d.status === 'failed' || d.status === 'retrying') && (
                    <Button size="sm" variant="outline" disabled={retry.isPending} onClick={() => retry.mutateAsync(d.id).then(() => toast.success('Sending again'), (e) => toast.error(errorMessage(e)))}>
                      <RotateCcwIcon /> Send again
                    </Button>
                  )}
                </li>
              ))}
            </ul>
            <Pager paged={paged} hasMore={!!q.hasNextPage} loadMore={() => q.fetchNextPage()} noun="deliveries" />
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function VerifyHelp({ org }: { org: string }) {
  const snippet = `// Node.js: npm install standardwebhooks
import { Webhook } from 'standardwebhooks'

const wh = new Webhook(process.env.WEBHOOK_SECRET) // whsec_… from above
app.post('/webhooks', express.raw({ type: 'application/json' }), (req, res) => {
  const event = wh.verify(req.body, req.headers) // throws if the signature is wrong
  // event.type, event.data; webhook-id header is the same on retries: use it to skip duplicates
  res.sendStatus(200)
})`
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Verify requests</CardTitle>
        <CardDescription>
          {org} signs every request with the Standard Webhooks format (webhook-id, webhook-timestamp, webhook-signature). Libraries exist for most
          languages at standardwebhooks.com. Answer 2xx quickly; anything else is retried.
        </CardDescription>
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
