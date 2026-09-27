import { BellIcon, MailIcon, PencilIcon, PlusIcon, SendIcon, HashIcon, Trash2Icon, WebhookIcon, type LucideIcon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { toast } from 'sonner'

import { EmptyState, ErrorState, PageHeader } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { SecretDialog } from '@/components/delivery'
import { SimpleSelect } from '@/components/simple-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { errorMessage } from '@/lib/api'
import { dateTime, timeAgo } from '@/lib/format'
import {
  useAlertChannels,
  useAlertLog,
  useAlertSettings,
  useCreateAlertChannel,
  useDeleteAlertChannel,
  useMembers,
  useTestAlertChannel,
  useUpdateAlertChannel,
} from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { AlertChannel, AlertChannelType, AlertKind, AlertLogEntry } from '@/lib/types'

const kindInfo: Record<AlertKind | 'test', { label: string; help: string }> = {
  incident_opened: { label: 'Incident opened', help: 'A provider broke a contract, e.g. a field changed type or went missing.' },
  incident_resolved: { label: 'Incident resolved', help: 'Resolved by hand, by accepting the change, automatically, or by a verified replay.' },
  destination_failing: { label: 'Destination failing', help: 'Your endpoint failed 3 deliveries in a row. Sent once per outage.' },
  destination_recovered: { label: 'Destination recovered', help: 'The endpoint accepted a delivery again after failing.' },
  signature_failures: { label: 'Signature failures', help: 'A webhook rejected events with a bad signature. At most once an hour.' },
  connection_broken: { label: 'Connection broken', help: "A user's connected account stopped working (access revoked or login changed). Sent once per break." },
  connection_recovered: { label: 'Connection recovered', help: 'A broken connection works again, e.g. after the user reconnected.' },
  test: { label: 'Test', help: '' },
}
const allKinds = Object.keys(kindInfo).filter((k) => k !== 'test') as AlertKind[]

const typeInfo: Record<AlertChannelType, { label: string; icon: LucideIcon }> = {
  slack: { label: 'Slack', icon: HashIcon },
  email: { label: 'Email', icon: MailIcon },
  webhook: { label: 'Webhook', icon: WebhookIcon },
}

export function AlertsPage() {
  const channels = useAlertChannels()
  const canManage = useCanManage()
  const [editing, setEditing] = useState<AlertChannel | 'new' | null>(null)
  const [secret, setSecret] = useState<string | null>(null)
  const list = channels.data?.data ?? []

  return (
    <>
      <PageHeader
        title="Alerts"
        description="Get told when a provider breaks a contract, an endpoint starts failing, or signatures stop matching, without watching the dashboard."
        actions={
          canManage && list.length > 0 ? (
            <Button onClick={() => setEditing('new')}>
              <PlusIcon /> Add channel
            </Button>
          ) : undefined
        }
      />

      {channels.error ? (
        <ErrorState error={channels.error} />
      ) : channels.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : list.length === 0 ? (
        <EmptyState
          title="No alert channels yet"
          icon={BellIcon}
          action={
            canManage && (
              <Button onClick={() => setEditing('new')}>
                <PlusIcon /> Add channel
              </Button>
            )
          }
        >
          Send alerts to a Slack channel, a teammate's email, or your own endpoint.
          {!canManage && ' Ask an admin to add one.'}
        </EmptyState>
      ) : (
        <ul className="divide-y rounded-lg border">
          {list.map((c) => (
            <ChannelRow key={c.id} c={c} onEdit={() => setEditing(c)} />
          ))}
        </ul>
      )}

      <AlertLog />

      <ChannelDialog
        channel={editing}
        onClose={() => setEditing(null)}
        onSecret={setSecret}
      />
      <SecretDialog secret={secret} onClose={() => setSecret(null)} />
    </>
  )
}

function ChannelRow({ c, onEdit }: { c: AlertChannel; onEdit: () => void }) {
  const canManage = useCanManage()
  const update = useUpdateAlertChannel()
  const remove = useDeleteAlertChannel()
  const test = useTestAlertChannel()
  const [result, setResult] = useState<{ ok: boolean; error: string } | null>(null)
  const Icon = typeInfo[c.type].icon

  return (
    <li className="p-4">
      <div className="flex flex-wrap items-start gap-3">
        <div className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-muted-foreground">
          <Icon className="size-4" />
        </div>
        <div className="min-w-0 flex-1 basis-48">
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 break-words font-medium">{c.name}</span>
            <Badge variant="secondary">{typeInfo[c.type].label}</Badge>
            {!c.enabled && <Badge variant="outline">paused</Badge>}
          </div>
          <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground" title={c.target}>
            {c.target}
          </div>
          <div className="mt-2 flex flex-wrap gap-1">
            {c.events.map((k) => (
              <span key={k} className="rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground">
                {kindInfo[k]?.label ?? k}
              </span>
            ))}
          </div>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            <span className="text-muted-foreground">{c.sent_7d} sent (7 days)</span>
            {c.failed_7d > 0 && <span className="text-red-700 dark:text-red-400">{c.failed_7d} failed</span>}
          </div>
        </div>
        {canManage && (
          <div className="flex flex-wrap items-center gap-1">
            <Button
              size="sm"
              variant="outline"
              disabled={test.isPending}
              onClick={() => test.mutateAsync(c.id).then(setResult, (e) => toast.error(errorMessage(e)))}
            >
              <SendIcon /> {test.isPending ? 'Sending…' : 'Send test'}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() =>
                update
                  .mutateAsync({ id: c.id, enabled: !c.enabled })
                  .then(() => toast.success(c.enabled ? 'Paused: no alerts will go to this channel' : 'Resumed'), (e) => toast.error(errorMessage(e)))
              }
            >
              {c.enabled ? 'Pause' : 'Resume'}
            </Button>
            <Button size="sm" variant="ghost" aria-label="Edit" onClick={onEdit}>
              <PencilIcon />
            </Button>
            <ConfirmButton
              variant="ghost"
              destructive
              title={`Delete “${c.name}”?`}
              description="Alerts stop going to this channel, and its alert history is removed."
              confirmLabel="Delete channel"
              onConfirm={() => remove.mutateAsync(c.id).then(() => toast.success('Channel deleted'))}
            >
              <Trash2Icon />
            </ConfirmButton>
          </div>
        )}
      </div>
      {result && (
        <div
          className={cn(
            'mt-3 flex items-start justify-between gap-2 rounded-md border p-3 text-xs',
            result.ok ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-red-500/30 bg-red-500/5',
          )}
        >
          <span className={cn('min-w-0 break-words', result.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-700 dark:text-red-400')}>
            {result.ok ? 'Test alert sent. Check the channel.' : `Test failed: ${result.error}`}
          </span>
          <button type="button" className="shrink-0 text-muted-foreground hover:text-foreground" onClick={() => setResult(null)}>
            Dismiss
          </button>
        </div>
      )}
    </li>
  )
}

function ChannelDialog({
  channel,
  onClose,
  onSecret,
}: {
  channel: AlertChannel | 'new' | null
  onClose: () => void
  onSecret: (s: string) => void
}) {
  return (
    <Dialog open={channel !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        {/* Keyed so the form resets each time it opens. */}
        {channel && <ChannelForm key={channel === 'new' ? 'new' : channel.id} channel={channel} onClose={onClose} onSecret={onSecret} />}
      </DialogContent>
    </Dialog>
  )
}

function ChannelForm({ channel, onClose, onSecret }: { channel: AlertChannel | 'new'; onClose: () => void; onSecret: (s: string) => void }) {
  const isNew = channel === 'new'
  const settings = useAlertSettings()
  const members = useMembers()
  const create = useCreateAlertChannel()
  const update = useUpdateAlertChannel()
  const [type, setType] = useState<AlertChannelType>(isNew ? 'slack' : channel.type)
  const [name, setName] = useState(isNew ? '' : channel.name)
  const [url, setUrl] = useState('')
  const [email, setEmail] = useState('')
  const [events, setEvents] = useState<AlertKind[]>(isNew ? allKinds : channel.events)
  const [error, setError] = useState('')
  const pending = create.isPending || update.isPending

  const memberOptions = (members.data?.data ?? []).map((m) => ({ value: m.email, label: m.name ? `${m.name} (${m.email})` : m.email }))

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setError('')
    try {
      if (isNew) {
        const res = await create.mutateAsync({
          type,
          name: name.trim(),
          events,
          ...(type === 'email' ? { email } : { url: url.trim() }),
        })
        onClose()
        // The secret dialog is confirmation enough; a toast would sit over its button on phones.
        if (res.signing_secret) onSecret(res.signing_secret)
        else toast.success('Channel added. Send a test to check it.')
      } else {
        await update.mutateAsync({ id: channel.id, name: name.trim(), events })
        toast.success('Channel updated')
        onClose()
      }
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>{isNew ? 'Add an alert channel' : `Edit “${channel.name}”`}</DialogTitle>
        <DialogDescription>{isNew ? 'Where alerts go, and which ones.' : 'Change its name or which alerts it gets.'}</DialogDescription>
      </DialogHeader>

      {isNew && (
        <div className="grid gap-2">
          <Label>Send to</Label>
          <div className="grid grid-cols-3 gap-2" role="radiogroup">
            {(Object.keys(typeInfo) as AlertChannelType[]).map((t) => {
              const Icon = typeInfo[t].icon
              return (
                <button
                  key={t}
                  type="button"
                  role="radio"
                  aria-checked={type === t}
                  onClick={() => setType(t)}
                  className={cn(
                    'flex min-w-0 flex-col items-center justify-center gap-1 rounded-md border px-1 py-2 text-sm transition-colors sm:flex-row sm:gap-2 sm:px-2',
                    type === t ? 'border-brand bg-brand/10 font-medium text-foreground' : 'text-muted-foreground hover:bg-muted',
                  )}
                >
                  <Icon className="size-4 shrink-0" />
                  <span className="truncate">{typeInfo[t].label}</span>
                </button>
              )
            })}
          </div>
        </div>
      )}

      <div className="grid gap-2">
        <Label htmlFor="ac-name">Name</Label>
        <Input
          id="ac-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={type === 'slack' ? '#payments-alerts' : type === 'email' ? 'On-call' : 'PagerDuty bridge'}
          required
          maxLength={100}
        />
      </div>

      {isNew && type === 'slack' && (
        <div className="grid gap-2">
          <Label htmlFor="ac-url">Slack Incoming Webhook URL</Label>
          <Input id="ac-url" type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://hooks.slack.com/services/…" required />
          <p className="text-xs text-muted-foreground">
            In Slack: Apps → Incoming Webhooks → Add to a channel, then paste the URL. It's stored encrypted.
          </p>
        </div>
      )}
      {isNew && type === 'webhook' && (
        <div className="grid gap-2">
          <Label htmlFor="ac-url">Endpoint URL</Label>
          <Input id="ac-url" type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://ops.yourapp.com/relaya-alerts" required />
          <p className="text-xs text-muted-foreground">Receives signed JSON: type, title, body, link. Must be a public https address.</p>
        </div>
      )}
      {isNew && type === 'email' && (
        <div className="grid gap-2">
          <Label htmlFor="ac-email">Member</Label>
          <SimpleSelect id="ac-email" className="w-full" value={email} onChange={setEmail} options={memberOptions} placeholder="Choose a member" />
          {settings.data && !settings.data.email_enabled ? (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              Email isn't set up on this server yet, so sends will fail until an admin adds SMTP settings.
            </p>
          ) : (
            <p className="text-xs text-muted-foreground">Only members of this organization can receive alert emails.</p>
          )}
        </div>
      )}

      <fieldset className="grid gap-2">
        <legend className="mb-2 text-sm font-medium">Alerts</legend>
        {allKinds.map((k) => (
          <label key={k} className="flex cursor-pointer items-start gap-3 rounded-md border p-2.5 hover:bg-muted/50">
            <input
              type="checkbox"
              className="mt-0.5 size-4 shrink-0 accent-[var(--brand)]"
              checked={events.includes(k)}
              onChange={(e) => setEvents((cur) => (e.target.checked ? [...cur, k] : cur.filter((x) => x !== k)))}
            />
            <span className="min-w-0">
              <span className="block text-sm">{kindInfo[k].label}</span>
              <span className="block text-xs text-muted-foreground">{kindInfo[k].help}</span>
            </span>
          </label>
        ))}
      </fieldset>

      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={pending || events.length === 0 || !name.trim() || (isNew && type === 'email' && !email)}>
          {pending ? 'Saving…' : isNew ? 'Add channel' : 'Save'}
        </Button>
      </DialogFooter>
    </form>
  )
}

const statusStyle: Record<AlertLogEntry['status'], string> = {
  sent: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  pending: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400',
  failed: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400',
}

function AlertLog() {
  const log = useAlertLog()
  const rows = log.data?.data ?? []
  if (!log.isPending && !log.error && rows.length === 0) return null

  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Recent alerts</CardTitle>
        <CardDescription>The last 100 alerts sent to any channel.</CardDescription>
      </CardHeader>
      <CardContent>
        {log.error ? (
          <ErrorState error={log.error} />
        ) : log.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          <div className="rounded-lg border">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow>
                  <TableHead className="hidden w-28 sm:table-cell">When</TableHead>
                  <TableHead>Alert</TableHead>
                  <TableHead className="hidden w-40 md:table-cell">Channel</TableHead>
                  <TableHead className="w-20">Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((a) => (
                  <TableRow key={a.id}>
                    <TableCell className="hidden align-top text-xs whitespace-normal text-muted-foreground sm:table-cell" title={dateTime(a.created_at)}>
                      {timeAgo(a.created_at)}
                    </TableCell>
                    <TableCell className="align-top whitespace-normal">
                      <div className="line-clamp-2 break-words text-sm sm:line-clamp-none sm:truncate" title={a.title}>
                        {a.title}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        <span className="sm:hidden">{timeAgo(a.created_at)} · </span>
                        {kindInfo[a.kind]?.label ?? a.kind}
                        <span className="md:hidden"> · {a.channel_name}</span>
                      </div>
                      {a.last_error && (
                        <div className="mt-0.5 line-clamp-2 break-words text-xs text-red-700 dark:text-red-400" title={a.last_error}>
                          {a.last_error}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="hidden align-top md:table-cell">
                      <div className="truncate text-sm" title={a.channel_name}>
                        {a.channel_name}
                      </div>
                      <div className="text-xs text-muted-foreground">{typeInfo[a.channel_type].label}</div>
                    </TableCell>
                    <TableCell className="align-top">
                      <Badge variant="outline" className={statusStyle[a.status]}>
                        {a.status}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
