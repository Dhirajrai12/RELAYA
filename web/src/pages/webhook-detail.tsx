import { ArrowLeftIcon, RefreshCwIcon, Trash2Icon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { toast } from 'sonner'

import { CopyField, ErrorState, PageHeader, SignatureLabel, StatusBadge } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage, get } from '@/lib/api'
import { providerLabel, timeAgo } from '@/lib/format'
import { ContractStateBadge } from '@/components/contract'
import { DestinationsCard } from '@/components/destinations-card'
import { SimulatorCard } from '@/components/simulator-card'
import { RepairRulesCard } from '@/components/repair'
import { useContracts, useDeleteWebhook, useEvents, useOrgId, useRotateWebhookURL, useUpdateWebhook, useWebhook } from '@/lib/queries'
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
  cashfree: [
    'Cashfree Merchant Dashboard → Payment Gateway → Developers → Webhooks → Add Webhook Endpoint.',
    'Paste the URL above and pick the events (e.g. payment success, failed, refund).',
    'Enter your Payment Gateway secret key (the API client secret) below: Cashfree signs webhooks with it.',
  ],
  payu: [
    'PayU Dashboard → Settings → Webhooks → Create Webhook, for each event type you want (successful, failed, refund).',
    'Paste the URL above.',
    'Enter your merchant salt below: PayU puts a hash made with it in every webhook.',
  ],
  phonepe: [
    'PhonePe Business Dashboard → Developer Settings → Webhooks → Create Webhook.',
    'Paste the URL above, choose a username and password there, and pick the events.',
    'Enter the same username and password below as username:password. (PhonePe proves the sender with them; it does not sign the body.)',
  ],
  standardwebhooks: [
    'For senders that follow the Standard Webhooks spec or use Svix (e.g. Resend, Clerk, OpenAI, Supabase): they send webhook-id / webhook-timestamp / webhook-signature (or svix-*) headers.',
    "Add the URL above as a webhook endpoint in the sender's dashboard.",
    'Copy the endpoint\'s signing secret (whsec_…, or a whpk_… public key for signed-with-Ed25519 endpoints) and enter it below.',
  ],
  slack: [
    'First enter your app\'s Signing Secret below (api.slack.com/apps → your app → Basic Information → App Credentials).',
    'Then Event Subscriptions → Enable Events → Request URL: paste the URL above. Slack checks it, and Relaya answers automatically.',
    'Subscribe to the bot or workspace events you need and save.',
  ],
  twilio: [
    'Twilio Console → Phone Numbers → your number (or a Messaging Service): paste the URL above as the webhook or status callback URL, exactly as shown.',
    'Enter your Auth Token (Console → Account Info) below. Twilio signs the full URL, so if the URL ever changes, update it at Twilio too.',
  ],
  hubspot: [
    'HubSpot developer account → your app → Webhooks → Target URL: paste the URL above, then create your subscriptions.',
    'Enter the app\'s Client secret (Auth tab) below. HubSpot signs each request with it (v3, and older v1/v2 are accepted).',
  ],
  square: [
    'Square Developer Dashboard → your app → Webhooks → Subscriptions → Add subscription: paste the URL above as the Notification URL and pick the events.',
    'Copy the subscription\'s Signature key and enter it below.',
  ],
  segment: [
    'Segment → Connections → Destinations → Add destination → Webhooks: paste the URL above.',
    'Set a Shared Secret in the destination settings and enter the same secret below.',
  ],
  sendgrid: [
    'SendGrid → Settings → Mail Settings → Event Webhook: paste the URL above as the Post URL and choose the events.',
    'Turn on Signed Event Webhook, copy the Verification Key and paste it below. It is a public key, so the test button can\'t sign SendGrid events; send one from SendGrid instead.',
  ],
  notion: [
    'notion.so/profile/integrations → your integration → Webhooks → Create a subscription: paste the URL above and choose the events.',
    'Notion sends a one-time verification token. It appears on this page: paste it back into Notion to verify, and use it as this webhook\'s signing secret.',
  ],
  jira: [
    'Jira → Settings (cog) → System → WebHooks → Create a WebHook (needs Jira admin).',
    'Paste the URL above, choose the events (e.g. Issue created / updated, Comment created), and optionally a JQL filter.',
    'Set a Secret there and enter the same secret below: Jira then signs every request (X-Hub-Signature).',
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

        {canManage && w.provider === 'notion' && <NotionVerificationCard w={w} />}

        {canManage && <SimulatorCard w={w} />}

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

/**
 * Notion verifies a new subscription by sending a token that must be pasted back
 * into Notion, and that is also the signing secret. The Explorer masks tokens, so
 * admins see it here, read from the raw event.
 */
function NotionVerificationCard({ w }: { w: Webhook }) {
  const orgId = useOrgId()
  const events = useEvents({ webhook_id: w.id, type: 'verification' })
  const update = useUpdateWebhook(w.id)
  const latest = events.data?.pages[0]?.data[0]
  const raw = useQuery({
    queryKey: ['raw-event', orgId, latest?.id],
    queryFn: () => get<{ body_base64: string }>(`/orgs/${orgId}/events/${latest!.id}/raw`),
    enabled: !!latest,
  })
  let token = ''
  try {
    token = raw.data ? (JSON.parse(atob(raw.data.body_base64)).verification_token ?? '') : ''
  } catch {
    token = ''
  }
  return (
    <Card className="lg:col-span-2">
      <CardHeader>
        <CardTitle>Notion verification token</CardTitle>
        <CardDescription>
          Notion sends this once, when you create the subscription. Paste it back into Notion to verify the subscription, and save it as this
          webhook's signing secret so Relaya can check Notion's signatures.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid min-w-0 gap-3">
        {!latest ? (
          <p className="text-sm text-muted-foreground">Nothing yet. Create the subscription in Notion with the URL above; the token appears here within seconds.</p>
        ) : !token ? (
          <Skeleton className="h-9 w-full" />
        ) : (
          <>
            <CopyField value={token} />
            <div className="flex flex-wrap items-center gap-3">
              <Button
                variant="outline"
                disabled={update.isPending}
                onClick={() =>
                  update.mutateAsync({ signing_secret: token }).then(() => toast.success('Saved as the signing secret'), (err) => toast.error(errorMessage(err)))
                }
              >
                Use as signing secret
              </Button>
              <span className="text-xs text-muted-foreground">Received {timeAgo(latest.received_at)}</span>
            </div>
          </>
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
