import { KeyRoundIcon, PlusIcon, SendIcon, Trash2Icon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { toast } from 'sonner'

import { ConfirmButton } from '@/components/confirm'
import { SecretDialog } from '@/components/delivery'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage } from '@/lib/api'
import { timeAgo } from '@/lib/format'
import {
  useCreateDestination,
  useDeleteDestination,
  useDestinations,
  useRotateDestinationSecret,
  useTestDestination,
  useUpdateDestination,
} from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { Destination, TestDeliveryResult } from '@/lib/types'

/** Where this webhook's events are forwarded, with health and controls. */
export function DestinationsCard({ webhookId }: { webhookId: string }) {
  const { data, isPending } = useDestinations(webhookId)
  const canManage = useCanManage()
  const [adding, setAdding] = useState(false)
  const [secret, setSecret] = useState<string | null>(null)
  const destinations = data?.data ?? []

  return (
    <Card className="lg:col-span-2">
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>Destinations</CardTitle>
          <CardDescription className="mt-1.5">
            Every accepted event is forwarded to these endpoints, signed by Relaya, with automatic retries.
          </CardDescription>
        </div>
        {canManage && (
          <Button size="sm" onClick={() => setAdding(true)}>
            <PlusIcon /> Add
          </Button>
        )}
      </CardHeader>
      <CardContent>
        {isPending ? (
          <Skeleton className="h-20 w-full" />
        ) : destinations.length === 0 ? (
          <div className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">
            Events are recorded but not forwarded yet.{' '}
            {canManage ? 'Add your endpoint to start delivering them, with retries if it is down.' : 'Ask an admin to add one.'}
          </div>
        ) : (
          <ul className="divide-y rounded-lg border">
            {destinations.map((d) => (
              <DestinationRow key={d.id} d={d} onSecret={setSecret} />
            ))}
          </ul>
        )}
      </CardContent>
      <AddDestinationDialog webhookId={webhookId} open={adding} onOpenChange={setAdding} onCreated={setSecret} />
      <SecretDialog secret={secret} onClose={() => setSecret(null)} />
    </Card>
  )
}

function DestinationRow({ d, onSecret }: { d: Destination; onSecret: (s: string) => void }) {
  const canManage = useCanManage()
  const update = useUpdateDestination()
  const remove = useDeleteDestination()
  const rotate = useRotateDestinationSecret()
  const test = useTestDestination()
  const [result, setResult] = useState<TestDeliveryResult | null>(null)
  const s = d.stats

  return (
    <li className="p-4">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1 basis-56">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{d.name}</span>
            {!d.enabled && <Badge variant="outline">disabled</Badge>}
          </div>
          <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground">{d.url}</div>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            <span className="text-emerald-700 dark:text-emerald-400">{s.succeeded_24h} delivered (24h)</span>
            <span className={s.failed_24h ? 'text-red-700 dark:text-red-400' : 'text-muted-foreground'}>{s.failed_24h} failed</span>
            <span className={s.retrying ? 'text-amber-700 dark:text-amber-400' : 'text-muted-foreground'}>{s.retrying} retrying</span>
            <span className="text-muted-foreground">
              {s.last_success_at ? `last success ${timeAgo(s.last_success_at)}` : 'no successful delivery yet'}
            </span>
          </div>
        </div>
        {canManage && (
          <div className="flex flex-wrap items-center gap-1">
            <Button
              size="sm"
              variant="outline"
              disabled={test.isPending}
              onClick={() =>
                test.mutateAsync(d.id).then(setResult, (e) => toast.error(errorMessage(e)))
              }
            >
              <SendIcon /> {test.isPending ? 'Sending…' : 'Send test'}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() =>
                update
                  .mutateAsync({ id: d.id, enabled: !d.enabled })
                  .then(() => toast.success(d.enabled ? 'Disabled: new events won’t be forwarded, and queued retries will stop' : 'Enabled: new events will be forwarded again'), (e) => toast.error(errorMessage(e)))
              }
            >
              {d.enabled ? 'Disable' : 'Enable'}
            </Button>
            <ConfirmButton
              variant="ghost"
              title="Rotate the signing secret?"
              description="Requests are signed with the new secret immediately. Update your endpoint right away or its signature check will start failing."
              confirmLabel="Rotate secret"
              onConfirm={() => rotate.mutateAsync(d.id).then((r) => onSecret(r.signing_secret))}
            >
              <KeyRoundIcon />
            </ConfirmButton>
            <ConfirmButton
              variant="ghost"
              destructive
              title={`Delete “${d.name}”?`}
              description="Events stop being forwarded to this endpoint, and its delivery history is removed."
              confirmLabel="Delete destination"
              onConfirm={() => remove.mutateAsync(d.id).then(() => toast.success('Destination deleted'))}
            >
              <Trash2Icon />
            </ConfirmButton>
          </div>
        )}
      </div>
      {result && <TestResult r={result} onClose={() => setResult(null)} />}
    </li>
  )
}

function TestResult({ r, onClose }: { r: TestDeliveryResult; onClose: () => void }) {
  return (
    <div
      className={cn(
        'mt-3 rounded-md border p-3 text-xs',
        r.ok ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-red-500/30 bg-red-500/5',
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className={cn('font-medium', r.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-700 dark:text-red-400')}>
          {r.ok ? `Test delivered: ${r.status_code}` : `Test failed: ${r.status_code || 'no response'}`}
          <span className="font-normal text-muted-foreground"> · {r.duration_ms} ms</span>
        </span>
        <button type="button" className="text-muted-foreground hover:text-foreground" onClick={onClose}>
          Dismiss
        </button>
      </div>
      {r.error && <div className="mt-1 text-red-700 dark:text-red-400">{r.error}</div>}
      {r.response_body && <pre className="mt-2 max-h-28 overflow-auto rounded border bg-background p-2 font-mono text-[11px]">{r.response_body}</pre>}
    </div>
  )
}

function AddDestinationDialog({
  webhookId,
  open,
  onOpenChange,
  onCreated,
}: {
  webhookId: string
  open: boolean
  onOpenChange: (o: boolean) => void
  onCreated: (secret: string) => void
}) {
  const create = useCreateDestination(webhookId)
  const [error, setError] = useState('')

  async function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    const f = new FormData(e.currentTarget)
    try {
      const res = await create.mutateAsync({
        name: String(f.get('name')),
        url: String(f.get('url')),
        max_attempts: Number(f.get('max_attempts')) || 8,
      })
      onOpenChange(false)
      onCreated(res.signing_secret)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add a destination</DialogTitle>
          <DialogDescription>Events are forwarded here as they arrive. Failed deliveries are retried for up to about 11 hours.</DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4">
          <div className="grid gap-2">
            <Label htmlFor="d-name">Name</Label>
            <Input id="d-name" name="name" placeholder="Order service (production)" required autoFocus />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="d-url">Endpoint URL</Label>
            <Input id="d-url" name="url" type="url" placeholder="https://api.yourapp.com/webhooks/razorpay" required />
            <p className="text-xs text-muted-foreground">Must be a public https address. Redirects aren't followed.</p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="d-attempts">Maximum attempts</Label>
            <Input id="d-attempts" name="max_attempts" type="number" min={1} max={20} defaultValue={8} />
            <p className="text-xs text-muted-foreground">Retries back off: 30s, 2m, 10m, 30m, 1h, 3h, 6h.</p>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? 'Adding…' : 'Add destination'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
