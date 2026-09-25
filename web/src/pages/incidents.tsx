import { CheckCircle2Icon, RotateCcwIcon, ShieldAlertIcon, TimerResetIcon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'

import { EmptyState, ErrorState, PageHeader } from '@/components/common'
import { FindingDiff } from '@/components/contract'
import { LiveIndicator } from '@/components/live-indicator'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { errorMessage } from '@/lib/api'
import { dateTime, kindLabel, timeAgo } from '@/lib/format'
import { useIncidents, useReplayPreview, useResolveIncident, useStartReplay } from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { Incident, Replay } from '@/lib/types'

export function IncidentsPage() {
  const [tab, setTab] = useState<'open' | 'resolved'>('open')
  const { data, error, isPending } = useIncidents(tab)
  const incidents = data?.data ?? []
  const autoAfter = data?.auto_resolve_after_seconds ?? 0

  return (
    <>
      <PageHeader
        title="Incidents"
        description="Breaking changes to your integrations, grouped: one incident per broken field, however many events it hits."
        actions={<LiveIndicator />}
      />
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div role="tablist" aria-label="Incident status" className="inline-flex rounded-lg border p-0.5 text-sm">
          {(['open', 'resolved'] as const).map((t) => (
            <button
              key={t}
              role="tab"
              type="button"
              aria-selected={tab === t}
              onClick={() => setTab(t)}
              className={cn('rounded-md px-3 py-1.5 capitalize text-muted-foreground', tab === t && 'bg-muted font-medium text-foreground')}
            >
              {t}
            </button>
          ))}
        </div>
        {autoAfter > 0 && (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <TimerResetIcon className="size-3.5" />
            Closes itself after {formatDuration(autoAfter)} without new occurrences, once a later event matches the contract.
          </span>
        )}
      </div>

      {error ? (
        <ErrorState error={error} />
      ) : isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : incidents.length === 0 ? (
        tab === 'open' ? (
          <EmptyState title="No open incidents" icon={CheckCircle2Icon}>
            Every checked event matches its contract's critical fields. New incidents appear here the moment one breaks.
          </EmptyState>
        ) : (
          <EmptyState title="No resolved incidents yet" />
        )
      ) : (
        <ul className="space-y-3">
          {incidents.map((i) => (
            <IncidentCard key={i.id} i={i} />
          ))}
        </ul>
      )}
    </>
  )
}

function formatDuration(s: number) {
  if (s % 3600 === 0) return s === 3600 ? '1 hour' : `${s / 3600} hours`
  if (s % 60 === 0) return `${s / 60} minutes`
  return `${s} seconds`
}

/** How an incident was closed, in words. */
function resolvedHow(i: Incident) {
  if (i.resolved_by !== 'system') return i.resolution ? `Resolved: ${i.resolution}` : 'Resolved manually'
  if (i.resolution.startsWith('verified by replay')) return `Verified: ${i.resolution.replace('verified by replay: ', 'replay delivered ')}`
  if (i.resolution.startsWith('stopped happening')) return 'Auto-resolved: stopped happening, and later events matched'
  return `Resolved: ${i.resolution}`
}

function IncidentCard({ i }: { i: Incident }) {
  const canManage = useCanManage()
  const [resolving, setResolving] = useState(false)
  const [replaying, setReplaying] = useState(false)
  const open = i.status === 'open'
  const running = i.replay?.status === 'running'

  return (
    <li className={cn('rounded-xl border p-4', open && 'border-red-500/30 bg-red-500/[0.03]')}>
      <div className="flex flex-wrap items-start gap-3">
        {open ? (
          <ShieldAlertIcon className="mt-0.5 size-5 shrink-0 text-red-600" aria-label="Open" />
        ) : (
          <CheckCircle2Icon className="mt-0.5 size-5 shrink-0 text-emerald-600" aria-label="Resolved" />
        )}
        <div className="min-w-0 flex-1">
          <div className="font-medium">{i.title}</div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>{i.webhook_name}</span>
            <span>{kindLabel(i.kind)}</span>
            <FindingDiff expected={i.expected} actual={i.actual} />
          </div>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            <span className={open ? 'font-medium text-red-700 dark:text-red-400' : 'text-muted-foreground'}>
              {i.event_count} event{i.event_count === 1 ? '' : 's'} affected
            </span>
            <span className="text-muted-foreground" title={dateTime(i.first_seen_at)}>
              first {timeAgo(i.first_seen_at)}
            </span>
            <span className="text-muted-foreground" title={dateTime(i.last_seen_at)}>
              last {timeAgo(i.last_seen_at)}
            </span>
          </div>
          {!open && i.resolved_at && (
            <div className="mt-2 text-xs text-emerald-700 dark:text-emerald-400" title={dateTime(i.resolved_at)}>
              {resolvedHow(i)} · {timeAgo(i.resolved_at)}
            </div>
          )}
          {i.replay && <ReplayProgress r={i.replay} />}
        </div>
        <div className="flex flex-wrap gap-2">
          {i.sample_event_id && (
            <Button variant="outline" size="sm" render={<Link to={`/events?event=${i.sample_event_id}`} />}>
              Sample event
            </Button>
          )}
          <Button variant="outline" size="sm" render={<Link to={`/contracts/${i.contract_id}`} />}>
            Contract
          </Button>
          {canManage && (
            <Button variant="outline" size="sm" disabled={running} onClick={() => setReplaying(true)}>
              <RotateCcwIcon /> {running ? 'Replaying…' : 'Replay'}
            </Button>
          )}
          {open && canManage && (
            <Button size="sm" onClick={() => setResolving(true)}>
              Resolve
            </Button>
          )}
        </div>
      </div>
      <ResolveDialog incident={resolving ? i : null} onClose={() => setResolving(false)} />
      <ReplayDialog incident={replaying ? i : null} onClose={() => setReplaying(false)} />
    </li>
  )
}

function ReplayProgress({ r }: { r: Replay }) {
  const done = r.succeeded + r.failed
  const pct = r.total ? Math.round((done / r.total) * 100) : 100
  const okPct = r.total ? (r.succeeded / r.total) * 100 : 0
  const badPct = r.total ? (r.failed / r.total) * 100 : 0
  return (
    <div className="mt-3 max-w-md rounded-lg border bg-background/60 p-3">
      <div className="mb-1.5 flex items-center justify-between gap-2 text-xs">
        <span className="font-medium">
          {r.status === 'running' ? `Replaying… ${pct}%` : r.failed ? 'Replay finished with failures' : 'Replay finished: all delivered'}
        </span>
        <span className="text-muted-foreground" title={dateTime(r.created_at)}>
          {timeAgo(r.created_at)}
        </span>
      </div>
      <div className="flex h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
        <div className="h-full bg-emerald-500 transition-[width] duration-500" style={{ width: `${okPct}%` }} />
        <div className="h-full bg-red-500 transition-[width] duration-500" style={{ width: `${badPct}%` }} />
      </div>
      <div className="mt-1.5 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
        <span className="text-emerald-700 dark:text-emerald-400">{r.succeeded} delivered</span>
        <span className={r.failed ? 'text-red-700 dark:text-red-400' : ''}>{r.failed} failed</span>
        {r.status === 'running' && <span>{r.total - done} remaining (retries included)</span>}
      </div>
    </div>
  )
}

function ReplayDialog({ incident, onClose }: { incident: Incident | null; onClose: () => void }) {
  const preview = useReplayPreview(incident?.id ?? null)
  const start = useStartReplay()
  const plan = preview.data

  async function run() {
    if (!incident) return
    try {
      const r = await start.mutateAsync(incident.id)
      toast.success(`Replaying ${r.total} deliver${r.total === 1 ? 'y' : 'ies'}`)
      onClose()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  return (
    <Dialog open={!!incident} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Replay affected events</DialogTitle>
          <DialogDescription>
            Use this after fixing your endpoint. The original payloads are sent again to the webhook's destinations. Nothing
            has been sent yet; this is a preview.
          </DialogDescription>
        </DialogHeader>

        {preview.error ? (
          <p className="text-sm text-destructive">{errorMessage(preview.error)}</p>
        ) : !plan ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          <div className="space-y-3 text-sm">
            <p>
              <strong>{plan.events}</strong> affected event{plan.events === 1 ? '' : 's'} →{' '}
              <strong>{plan.will_send}</strong> deliver{plan.will_send === 1 ? 'y' : 'ies'} will be sent.
            </p>
            {plan.destinations.length === 0 ? (
              <p className="text-amber-700 dark:text-amber-400">This webhook has no destinations, so there is nothing to replay to.</p>
            ) : (
              <ul className="divide-y rounded-lg border">
                {plan.destinations.map((d) => (
                  <li key={d.destination_id} className="px-3 py-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-medium">{d.destination_name}</span>
                      <span className={cn('text-xs', d.enabled ? 'text-muted-foreground' : 'text-amber-700 dark:text-amber-400')}>
                        {d.enabled ? `${d.deliveries - d.in_flight} to send` : 'disabled: skipped'}
                      </span>
                    </div>
                    <div className="truncate font-mono text-xs text-muted-foreground">{d.destination_url}</div>
                    {(d.already_succeeded > 0 || d.in_flight > 0) && (
                      <div className="mt-1 text-xs text-muted-foreground">
                        {d.already_succeeded > 0 && `${d.already_succeeded} were already delivered and will be sent again. `}
                        {d.in_flight > 0 && `${d.in_flight} are being sent right now and are skipped.`}
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
            <p className="text-xs text-muted-foreground">
              Each request keeps its original <code className="font-mono">Idempotency-Key</code>, so events your endpoint already
              processed can be ignored safely, and carries a <code className="font-mono">Relaya-Replay</code> header. If every
              delivery succeeds, this incident resolves itself as verified.
            </p>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={run} disabled={!plan || plan.will_send === 0 || start.isPending}>
            {start.isPending ? 'Starting…' : plan ? `Replay ${plan.will_send} deliver${plan.will_send === 1 ? 'y' : 'ies'}` : 'Replay'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ResolveDialog({ incident, onClose }: { incident: Incident | null; onClose: () => void }) {
  const resolve = useResolveIncident()
  const [note, setNote] = useState('')
  async function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    if (!incident) return
    try {
      await resolve.mutateAsync({ id: incident.id, resolution: note.trim() })
      toast.success('Incident resolved')
      setNote('')
      onClose()
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }
  return (
    <Dialog open={!!incident} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4">
          <DialogHeader>
            <DialogTitle>Resolve incident</DialogTitle>
            <DialogDescription>
              You don't have to: incidents close themselves once the problem stops, and a successful Replay closes them as
              verified. If the provider changed on purpose, use “Accept changes” on the contract instead.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="resolution">What happened? (optional)</Label>
            <Textarea id="resolution" value={note} onChange={(e) => setNote(e.target.value)} placeholder="Provider rolled back the change" maxLength={500} />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={resolve.isPending}>
              Resolve
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
