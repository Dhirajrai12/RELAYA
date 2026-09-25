import { CheckCircle2Icon, ChevronDownIcon, CircleDashedIcon, Loader2Icon, RotateCwIcon, XCircleIcon } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { CopyButton, CopyField } from '@/components/common'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage } from '@/lib/api'
import { dateTime, timeAgo } from '@/lib/format'
import { useDeliveryAttempts, useRetryDelivery } from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { Delivery, DeliveryStatus, EventDeliveryState } from '@/lib/types'

// Status always pairs an icon with a word, never color alone.

const statusMeta: Record<DeliveryStatus, { label: string; cls: string }> = {
  succeeded: { label: 'delivered', cls: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400' },
  failed: { label: 'failed', cls: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400' },
  retrying: { label: 'retrying', cls: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400' },
  in_flight: { label: 'sending', cls: 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-400' },
  pending: { label: 'queued', cls: 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-400' },
}

export function DeliveryStatusBadge({ status }: { status: DeliveryStatus }) {
  const m = statusMeta[status]
  return (
    <Badge variant="outline" className={m.cls}>
      {m.label}
    </Badge>
  )
}

/** Compact forwarding state for an event row. */
export function EventDeliveryState({ state }: { state: EventDeliveryState }) {
  switch (state) {
    case 'delivered':
      return (
        <span className="inline-flex items-center gap-1 text-xs text-emerald-700 dark:text-emerald-400">
          <CheckCircle2Icon className="size-3.5" /> delivered
        </span>
      )
    case 'failed':
      return (
        <span className="inline-flex items-center gap-1 text-xs text-red-700 dark:text-red-400">
          <XCircleIcon className="size-3.5" /> failed
        </span>
      )
    case 'pending':
      return (
        <span className="inline-flex items-center gap-1 text-xs text-amber-700 dark:text-amber-400">
          <Loader2Icon className="size-3.5 motion-safe:animate-spin" /> in progress
        </span>
      )
    default:
      return (
        <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          <CircleDashedIcon className="size-3.5" /> not forwarded
        </span>
      )
  }
}

/** Shown once after creating a destination or rotating its secret. */
export function SecretDialog({ secret, onClose }: { secret: string | null; onClose: () => void }) {
  const snippet = `// Node.js: verify a request forwarded by Relaya (use the raw request body)
import crypto from 'node:crypto'

function verifyRelaya(rawBody, header, secret = process.env.RELAYA_SIGNING_SECRET) {
  const parts = Object.fromEntries(header.split(',').map((p) => p.split('=')))
  const expected = crypto.createHmac('sha256', secret).update(\`\${parts.t}.\${rawBody}\`).digest('hex')
  const fresh = Math.abs(Date.now() / 1000 - Number(parts.t)) < 300 // reject replays older than 5 min
  return fresh && expected.length === parts.v1?.length &&
    crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(parts.v1))
}

// verifyRelaya(req.rawBody, req.headers['relaya-signature'])`
  return (
    <Dialog open={!!secret} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Copy the signing secret now</DialogTitle>
          <DialogDescription>
            Relaya signs every request it forwards with this secret. It won't be shown again; store it where your endpoint can
            read it (for example <code className="font-mono text-xs">RELAYA_SIGNING_SECRET</code>).
          </DialogDescription>
        </DialogHeader>
        {secret && <CopyField value={secret} />}
        <div>
          <div className="mb-2 flex items-center justify-between">
            <span className="text-sm font-medium">Verify it on your side</span>
            <CopyButton value={snippet} label="Copy code" />
          </div>
          <pre className="max-h-64 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed">{snippet}</pre>
          <p className="mt-2 text-xs text-muted-foreground">
            The provider's own headers (for example <code className="font-mono">X-Razorpay-Signature</code>) are forwarded too,
            so existing verification code keeps working.
          </p>
        </div>
        <DialogFooter>
          <Button onClick={onClose}>I've saved it</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** The deliveries of one event, for the event detail sheet. */
export function EventDeliveries({ deliveries }: { deliveries: Delivery[] }) {
  if (deliveries.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Not forwarded: this webhook has no destinations, or it was rejected. Add a destination on the webhook page to forward
        future events.
      </p>
    )
  }
  return (
    <ul className="space-y-2">
      {deliveries.map((d) => (
        <DeliveryItem key={d.id} d={d} />
      ))}
    </ul>
  )
}

function DeliveryItem({ d }: { d: Delivery }) {
  const [open, setOpen] = useState(false)
  const canManage = useCanManage()
  const retry = useRetryDelivery()
  const canRetry = canManage && (d.status === 'failed' || d.status === 'retrying')

  return (
    <li className="rounded-lg border">
      <div className="flex flex-wrap items-start gap-3 p-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{d.destination_name}</span>
            <DeliveryStatusBadge status={d.status} />
          </div>
          <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{d.destination_url}</div>
          <div className="mt-1.5 text-xs text-muted-foreground">
            Attempt {d.attempts} of {d.max_attempts}
            {d.last_status_code != null && ` · last response ${d.last_status_code}`}
            {d.last_error && <span className="text-red-700 dark:text-red-400"> · {d.last_error}</span>}
            {d.status === 'retrying' && d.next_attempt_at && ` · next try ${timeAgo(d.next_attempt_at)}`}
            {d.completed_at && d.status === 'succeeded' && ` · delivered ${timeAgo(d.completed_at)}`}
          </div>
        </div>
        <div className="flex items-center gap-1">
          {canRetry && (
            <Button
              size="sm"
              variant="outline"
              disabled={retry.isPending}
              onClick={() =>
                retry.mutateAsync(d.id).then(
                  () => toast.success('Retry queued'),
                  (e) => toast.error(errorMessage(e)),
                )
              }
            >
              <RotateCwIcon /> Retry now
            </Button>
          )}
          <Button size="sm" variant="ghost" onClick={() => setOpen(!open)} aria-expanded={open}>
            Attempts <ChevronDownIcon className={cn('transition-transform', open && 'rotate-180')} />
          </Button>
        </div>
      </div>
      {open && <Attempts id={d.id} />}
    </li>
  )
}

function Attempts({ id }: { id: string }) {
  const { data, isPending } = useDeliveryAttempts(id)
  if (isPending) return <Skeleton className="m-3 h-16" />
  const attempts = data?.attempts ?? []
  if (attempts.length === 0) return <p className="border-t p-3 text-xs text-muted-foreground">No attempts yet.</p>
  return (
    <ol className="divide-y border-t text-xs">
      {attempts.map((a) => (
        <li key={`${a.attempt}-${a.started_at}`} className="space-y-1 p-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span className="font-medium">#{a.attempt}</span>
            <span
              className={cn(
                a.outcome === 'succeeded' && 'text-emerald-700 dark:text-emerald-400',
                a.outcome === 'failed' && 'text-red-700 dark:text-red-400',
                a.outcome === 'retry' && 'text-amber-700 dark:text-amber-400',
              )}
            >
              {a.outcome === 'retry' ? 'will retry' : a.outcome}
            </span>
            <span className="text-muted-foreground">{a.status_code ?? 'no response'}</span>
            <span className="text-muted-foreground">{a.duration_ms} ms</span>
            <span className="text-muted-foreground" title={dateTime(a.started_at)}>
              {timeAgo(a.started_at)}
            </span>
          </div>
          {a.error && <div className="text-red-700 dark:text-red-400">{a.error}</div>}
          {a.response_body && (
            <pre className="max-h-32 overflow-auto rounded border bg-muted p-2 font-mono text-[11px]">{a.response_body}</pre>
          )}
        </li>
      ))}
    </ol>
  )
}
