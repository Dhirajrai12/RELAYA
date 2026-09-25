import { ArrowLeftIcon, RefreshCwIcon, Trash2Icon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { toast } from 'sonner'

import { CopyField, ErrorState, PageHeader, SignatureLabel, StatusBadge } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage } from '@/lib/api'
import { providerLabel, timeAgo } from '@/lib/format'
import { ContractStateBadge } from '@/components/contract'
import { DestinationsCard } from '@/components/destinations-card'
import { RepairRulesCard } from '@/components/repair'
import { useContracts, useDeleteWebhook, useEvents, useRotateWebhookURL, useUpdateWebhook, useWebhook } from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import type { Webhook } from '@/lib/types'

const setupSteps: Record<string, string[]> = {
  razorpay: [
    'Razorpay Dashboard → Accounts & Settings → Webhooks → Add New Webhook.',
    'Paste the URL above as the Webhook URL.',
    'Set a Secret there and enter the same secret below.',
    'Choose the events you want (e.g. payment.captured) and save.',
  ],
  stripe: [
    'Stripe Dashboard → Developers → Webhooks → Add endpoint.',
    'Paste the URL above and choose the events to send.',
    'After saving, reveal the "Signing secret" (whsec_…) and enter it below.',
  ],
  shopify: [
    'Shopify admin → Settings → Notifications → Webhooks → Create webhook (or register via the Admin API).',
    'Paste the URL above, format JSON.',
    "Enter your app's API secret key (or the webhook signing key shown by Shopify) below.",
  ],
  github: [
    'Repository or organization → Settings → Webhooks → Add webhook.',
    'Paste the URL above, content type application/json.',
    'Set a Secret there and enter the same secret below.',
  ],
  generic: [
    'Configure the sender to POST JSON to the URL above.',
    'For signature checks, have it send a hex HMAC-SHA256 of the raw body in the signature header, keyed with the secret below.',
  ],
}

export function WebhookDetailPage() {
  const { id = '' } = useParams()
  const webhook = useWebhook(id)

  if (webhook.error) return <ErrorState error={webhook.error} />
  if (webhook.isPending) return <Skeleton className="h-96 w-full" />
  return <WebhookView w={webhook.data} />
}

function WebhookView({ w }: { w: Webhook }) {
  const canManage = useCanManage()
  const navigate = useNavigate()
  const rotate = useRotateWebhookURL(w.id)
  const remove = useDeleteWebhook()

  return (
    <>
      <Link to="/webhooks" className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeftIcon className="size-4" /> Webhooks
      </Link>
      <PageHeader
        title={w.name}
        description={`${providerLabel(w.provider)} · created ${timeAgo(w.created_at)}`}
        actions={<Badge variant={w.status === 'active' ? 'secondary' : 'outline'}>{w.status}</Badge>}
      />

      <div className="grid gap-6 lg:grid-cols-2">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Webhook URL</CardTitle>
            <CardDescription>Give this URL to {providerLabel(w.provider)}. Treat it like a password.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <CopyField value={w.ingest_url} />
            <ol className="list-decimal space-y-1 pl-5 text-sm text-muted-foreground">
              {(setupSteps[w.provider] ?? setupSteps.generic).map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ol>
            <details className="text-sm">
              <summary className="cursor-pointer text-muted-foreground">Send a test delivery from a terminal</summary>
              <pre className="mt-2 overflow-x-auto rounded-md border bg-muted p-3 font-mono text-xs">
                {`curl -X POST '${w.ingest_url}' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"type":"test.event","id":"test_1"}'`}
              </pre>
              {w.has_signing_secret && (
                <p className="mt-1 text-xs text-muted-foreground">
                  Signature checks are on, so an unsigned test will be stored as rejected (HTTP 401). That's expected.
                </p>
              )}
            </details>
          </CardContent>
        </Card>

        <DestinationsCard webhookId={w.id} />

        <RepairRulesCard webhookId={w.id} />

        <WebhookContracts webhookId={w.id} />

        <RecentEvents webhookId={w.id} />

        {canManage && (
          <Card>
            <CardHeader>
              <CardTitle>Settings</CardTitle>
              <CardDescription>
                Signature check is{' '}
                <strong className={w.has_signing_secret ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'}>
                  {w.has_signing_secret ? 'on' : 'off'}
                </strong>
                .
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-6">
              <RenameForm w={w} />
              <SecretForm w={w} />
              <div className="flex flex-wrap gap-2 border-t pt-4">
                <ConfirmButton
                  title="Rotate webhook URL?"
                  description="A new URL is issued and the current one stops working within 30 seconds. You'll need to update it at the provider."
                  confirmLabel="Rotate URL"
                  onConfirm={() => rotate.mutateAsync().then(() => toast.success('New URL issued'))}
                >
                  <RefreshCwIcon /> Rotate URL
                </ConfirmButton>
                <ConfirmButton
                  destructive
                  title="Delete this webhook?"
                  description="The URL stops accepting deliveries. Events already received are kept."
                  confirmLabel="Delete webhook"
                  onConfirm={() =>
                    remove.mutateAsync(w.id).then(() => {
                      toast.success('Webhook deleted')
                      navigate('/webhooks')
                    })
                  }
                >
                  <Trash2Icon /> Delete
                </ConfirmButton>
              </div>
            </CardContent>
          </Card>
        )}
      </div>
    </>
  )
}

function RecentEvents({ webhookId }: { webhookId: string }) {
  const events = useEvents({ webhook_id: webhookId })
  const rows = events.data?.pages[0]?.data.slice(0, 8) ?? []
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent deliveries</CardTitle>
        <CardDescription>
          <Link to={`/events?webhook_id=${webhookId}`} className="underline underline-offset-4">
            Open in Events
          </Link>
        </CardDescription>
      </CardHeader>
      <CardContent>
        {events.isPending ? (
          <Skeleton className="h-32 w-full" />
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing received yet. Waiting for the first delivery…</p>
        ) : (
          <ul className="divide-y text-sm">
            {rows.map((e) => (
              <li key={e.id}>
                <Link to={`/events?webhook_id=${webhookId}&event=${e.id}`} className="flex items-center gap-3 py-2 hover:bg-muted/50">
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">{e.type || '—'}</span>
                  <SignatureLabel value={e.signature} />
                  <StatusBadge status={e.status} />
                  <span className="w-20 text-right text-xs text-muted-foreground">{timeAgo(e.received_at)}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function RenameForm({ w }: { w: Webhook }) {
  const update = useUpdateWebhook(w.id)
  const [name, setName] = useState(w.name)
  return (
    <form
      className="grid gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        update.mutateAsync({ name }).then(() => toast.success('Renamed'), (err) => toast.error(errorMessage(err)))
      }}
    >
      <Label htmlFor="wh-name">Name</Label>
      <div className="flex gap-2">
        <Input id="wh-name" value={name} onChange={(e) => setName(e.target.value)} />
        <Button type="submit" variant="outline" disabled={name === w.name || !name.trim() || update.isPending}>
          Save
        </Button>
      </div>
    </form>
  )
}

function SecretForm({ w }: { w: Webhook }) {
  const update = useUpdateWebhook(w.id)
  const [secret, setSecret] = useState('')

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    try {
      await update.mutateAsync({ signing_secret: secret })
      setSecret('')
      toast.success('Signing secret saved')
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  return (
    <form className="grid gap-2" onSubmit={onSubmit}>
      <Label htmlFor="wh-secret">{w.has_signing_secret ? 'Replace signing secret' : 'Signing secret'}</Label>
      <div className="flex gap-2">
        <Input
          id="wh-secret"
          type="password"
          autoComplete="off"
          placeholder={w.has_signing_secret ? '••••••••  (stored encrypted)' : 'Paste from the provider'}
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
        />
        <Button type="submit" variant="outline" disabled={!secret || update.isPending}>
          Save
        </Button>
      </div>
      {w.has_signing_secret && (
        <div>
          <ConfirmButton
            variant="ghost"
            title="Turn off signature checks?"
            description="Deliveries will be accepted without verifying they came from the provider. Anyone who knows the URL could send fake events."
            confirmLabel="Remove secret"
            destructive
            onConfirm={() => update.mutateAsync({ signing_secret: '' }).then(() => toast.success('Signature checks turned off'))}
          >
            Remove secret
          </ConfirmButton>
        </div>
      )}
    </form>
  )
}

/** The contracts learned for this webhook, one per event type. */
function WebhookContracts({ webhookId }: { webhookId: string }) {
  const { data, isPending } = useContracts(webhookId)
  const contracts = data?.data ?? []
  return (
    <Card className="lg:col-span-2">
      <CardHeader>
        <CardTitle>Contracts</CardTitle>
        <CardDescription>
          One per event type, learned automatically. Critical fields that go missing or change type open an incident.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : contracts.length === 0 ? (
          <p className="text-sm text-muted-foreground">No contracts yet. They appear when JSON events with a type arrive.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {contracts.map((c) => (
              <li key={c.id}>
                <Link to={`/contracts/${c.id}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5 hover:bg-muted/50">
                  <span className="min-w-0 flex-1 truncate font-mono text-sm">{c.event_type}</span>
                  {c.open_incidents > 0 && (
                    <span className="text-xs font-medium text-red-700 dark:text-red-400">
                      {c.open_incidents} open incident{c.open_incidents > 1 ? 's' : ''}
                    </span>
                  )}
                  <ContractStateBadge state={c.status} version={c.active_version} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
