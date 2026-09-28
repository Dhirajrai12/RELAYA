import { HistoryIcon, PauseIcon, PlayIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { Link } from 'react-router-dom'
import { toast } from 'sonner'

import { ErrorState } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { Pager, usePaged } from '@/components/pager'
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
import { compact, dateTime, timeAgo } from '@/lib/format'
import { useConnections, useCreateSync, useDeleteSync, useRunSync, useSyncModels, useSyncRuns, useSyncs, useUpdateSync, useWebhooks } from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { Sync, SyncModel } from '@/lib/types'

const INTERVALS = [
  { value: '5', label: 'Every 5 minutes' },
  { value: '15', label: 'Every 15 minutes' },
  { value: '30', label: 'Every 30 minutes' },
  { value: '60', label: 'Every hour' },
  { value: '360', label: 'Every 6 hours' },
  { value: '1440', label: 'Once a day' },
]

const green = 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
const red = 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400'
const grey = 'border-border text-muted-foreground'

function every(minutes: number) {
  return INTERVALS.find((i) => i.value === String(minutes))?.label.toLowerCase() ?? `every ${minutes} minutes`
}

/** Syncs: pull records from connected apps and turn changes into events. */
export function SyncsCard() {
  const syncs = useSyncs()
  const canManage = useCanManage()
  const [adding, setAdding] = useState(false)
  const [history, setHistory] = useState<Sync | null>(null)
  const rows = syncs.data?.data ?? []
  const paged = usePaged(rows, 10)

  return (
    <Card className="mt-6">
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-64">
          <CardTitle>Syncs</CardTitle>
          <CardDescription className="mt-1.5">
            Relaya checks a connected app on a schedule. New and changed records become events, delivered and checked like webhooks, so it
            also works for apps that don’t send webhooks.
          </CardDescription>
        </div>
        {canManage && (
          <Button size="sm" onClick={() => setAdding(true)}>
            <PlusIcon /> Add sync
          </Button>
        )}
      </CardHeader>
      <CardContent>
        {syncs.error ? (
          <ErrorState error={syncs.error} />
        ) : syncs.isPending ? (
          <Skeleton className="h-20 w-full" />
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No syncs yet. Example: every 15 minutes, turn new Zoho leads into <code className="font-mono text-xs">zoho.lead.created</code> events for your app.
          </p>
        ) : (
          <div className="rounded-lg border">
            <ul className="divide-y">
              {paged.items.map((s) => (
                <SyncRow key={s.id} s={s} onHistory={() => setHistory(s)} />
              ))}
            </ul>
            <Pager paged={paged} noun="syncs" />
          </div>
        )}
      </CardContent>

      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className="sm:max-w-lg">{adding && <AddSyncForm onClose={() => setAdding(false)} />}</DialogContent>
      </Dialog>
      <Dialog open={history !== null} onOpenChange={(o) => !o && setHistory(null)}>
        <DialogContent className="sm:max-w-2xl">{history && <RunHistory s={history} />}</DialogContent>
      </Dialog>
    </Card>
  )
}

function SyncRow({ s, onHistory }: { s: Sync; onHistory: () => void }) {
  const canManage = useCanManage()
  const run = useRunSync()
  const update = useUpdateSync()
  const remove = useDeleteSync()
  const status = !s.enabled
    ? { label: 'paused', cls: grey }
    : s.running
      ? { label: 'running', cls: green }
      : s.last_status === 'error'
        ? { label: 'failing', cls: red }
        : s.last_status === 'ok'
          ? { label: 'ok', cls: green }
          : { label: 'waiting', cls: grey }
  const config = Object.entries(s.config)
    .map(([k, v]) => `${k}: ${v}`)
    .join(' · ')

  return (
    <li className="flex flex-wrap items-start gap-3 p-4">
      <div className="min-w-0 flex-1 basis-56">
        <div className="flex flex-wrap items-center gap-2">
          <span className="min-w-0 break-words font-medium">
            {s.integration_name} {s.model_name.toLowerCase()}
          </span>
          <Badge variant="outline" className={status.cls}>
            {status.label}
          </Badge>
        </div>
        <div className="mt-0.5 truncate text-xs text-muted-foreground" title={config}>
          <span className="font-mono">{s.end_user_id}</span>
          {config && ` · ${config}`}
        </div>
        <div className="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <span>{every(s.interval_minutes)}</span>
          <span>{s.last_run_at ? `last run ${timeAgo(s.last_run_at)}` : 'first run starting'}</span>
          <span>{compact(s.records)} records</span>
          <span>{compact(s.events)} events</span>
          <Link to={`/webhooks/${s.webhook_id}`} className="min-w-0 truncate text-brand hover:underline" title={s.webhook_name}>
            → {s.webhook_name}
          </Link>
        </div>
        {!s.baseline_done && s.enabled && (
          <p className="mt-1.5 text-xs text-muted-foreground">
            {s.emit_existing ? 'First run sends events for existing records too.' : 'First run only remembers existing records; changes after it become events.'}
          </p>
        )}
        {s.last_status === 'error' && s.last_error && (
          <p className="mt-1.5 line-clamp-2 break-words text-xs text-red-700 dark:text-red-400" title={s.last_error}>
            {s.consecutive_failures > 1 && `${s.consecutive_failures} runs failed. `}
            {s.last_error}
          </p>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-1">
        {canManage && (
          <Button
            size="sm"
            variant="outline"
            disabled={run.isPending || s.running || !s.enabled}
            onClick={() => run.mutateAsync(s.id).then(() => toast.success('Running now'), (e) => toast.error(errorMessage(e)))}
          >
            <RefreshCwIcon className={cn(s.running && 'animate-spin')} /> Run now
          </Button>
        )}
        <Button size="sm" variant="ghost" aria-label="Run history" title="Run history" onClick={onHistory}>
          <HistoryIcon />
        </Button>
        {canManage && (
          <Button
            size="sm"
            variant="ghost"
            aria-label={s.enabled ? 'Pause' : 'Resume'}
            title={s.enabled ? 'Pause' : 'Resume'}
            onClick={() =>
              update
                .mutateAsync({ id: s.id, enabled: !s.enabled })
                .then(() => toast.success(s.enabled ? 'Paused' : 'Resumed: running now'), (e) => toast.error(errorMessage(e)))
            }
          >
            {s.enabled ? <PauseIcon /> : <PlayIcon />}
          </Button>
        )}
        {canManage && (
          <ConfirmButton
            variant="ghost"
            destructive
            title="Delete this sync?"
            description="It stops checking the app. Events it already made stay; its webhook stays too (delete it separately if you like)."
            confirmLabel="Delete sync"
            onConfirm={() => remove.mutateAsync(s.id).then(() => toast.success('Sync deleted'))}
          >
            <Trash2Icon />
          </ConfirmButton>
        )}
      </div>
    </li>
  )
}

function AddSyncForm({ onClose }: { onClose: () => void }) {
  const connections = useConnections()
  const models = useSyncModels()
  const webhooks = useWebhooks()
  const create = useCreateSync()
  const active = (connections.data?.data ?? []).filter((c) => c.status === 'active')
  const [connId, setConnId] = useState(active[0]?.id ?? '')
  const conn = active.find((c) => c.id === connId)
  const available = (models.data?.data ?? []).filter((m) => m.provider === conn?.provider)
  const [modelKey, setModelKey] = useState('')
  const model: SyncModel | undefined = available.find((m) => m.key === modelKey) ?? available[0]
  const [config, setConfig] = useState<Record<string, string>>({})
  const [interval, setEvery] = useState('15')
  const [webhookId, setWebhookId] = useState('new')
  const [emitExisting, setEmitExisting] = useState(false)
  const [error, setError] = useState('')

  if (connections.isPending || models.isPending) return <Skeleton className="h-40 w-full" />
  if (active.length === 0) {
    return (
      <div className="grid gap-3">
        <DialogHeader>
          <DialogTitle>Add a sync</DialogTitle>
          <DialogDescription>A sync reads a connected account, so connect one first: create a connect link and sign in.</DialogDescription>
        </DialogHeader>
      </div>
    )
  }

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    if (!model) return
    setError('')
    try {
      const cfg: Record<string, string> = {}
      for (const f of model.fields) if (config[f.key]?.trim()) cfg[f.key] = config[f.key].trim()
      await create.mutateAsync({
        connection_id: connId,
        model: model.key,
        config: cfg,
        interval_minutes: Number(interval),
        emit_existing: emitExisting,
        ...(webhookId !== 'new' ? { webhook_id: webhookId } : {}),
      })
      toast.success('Sync added. Its first run starts in a few seconds.')
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <form onSubmit={onSubmit} className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>Add a sync</DialogTitle>
        <DialogDescription>What to read, how often, and where the events go.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor="sy-conn">Connected account</Label>
        <SimpleSelect
          id="sy-conn"
          className="w-full"
          value={connId}
          onChange={(v) => {
            setConnId(v)
            setModelKey('')
            setConfig({})
          }}
          options={active.map((c) => ({ value: c.id, label: `${c.integration_name} · ${c.end_user_id}` }))}
        />
      </div>
      {available.length === 0 ? (
        <p className="text-sm text-muted-foreground">No sync types for this app yet.</p>
      ) : (
        <>
          <fieldset className="grid gap-2">
            <legend className="mb-2 text-sm font-medium">Data</legend>
            {available.map((m) => (
              <label key={m.key} className="flex cursor-pointer items-start gap-3 rounded-md border p-2.5 hover:bg-muted/50">
                <input
                  type="radio"
                  name="sy-model"
                  className="mt-0.5 size-4 shrink-0 accent-[var(--brand)]"
                  checked={model?.key === m.key}
                  onChange={() => {
                    setModelKey(m.key)
                    setConfig({})
                  }}
                />
                <span className="min-w-0">
                  <span className="block text-sm">
                    {m.name}
                    {!m.verified && <span className="ml-2 text-[11px] text-amber-700 dark:text-amber-400">not yet tried with a real account</span>}
                  </span>
                  <span className="block text-xs text-muted-foreground">{m.description}</span>
                </span>
              </label>
            ))}
          </fieldset>
          {model?.fields.map((f) => (
            <div key={model.key + f.key} className="grid gap-2">
              <Label htmlFor={`sy-${f.key}`}>
                {f.label}
                {!f.required && <span className="font-normal text-muted-foreground"> (optional)</span>}
              </Label>
              {f.options ? (
                <SimpleSelect
                  id={`sy-${f.key}`}
                  className="w-full"
                  value={config[f.key] ?? f.default ?? f.options[0]}
                  onChange={(v) => setConfig((c) => ({ ...c, [f.key]: v }))}
                  options={f.options.map((o) => ({ value: o, label: o }))}
                />
              ) : (
                <Input
                  id={`sy-${f.key}`}
                  value={config[f.key] ?? f.default ?? ''}
                  onChange={(e) => setConfig((c) => ({ ...c, [f.key]: e.target.value }))}
                  placeholder={f.placeholder}
                  required={f.required}
                  maxLength={500}
                  className="font-mono text-xs"
                />
              )}
              {f.help && <p className="text-xs text-muted-foreground">{f.help}</p>}
            </div>
          ))}
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label htmlFor="sy-interval">How often</Label>
              <SimpleSelect id="sy-interval" className="w-full" value={interval} onChange={setEvery} options={INTERVALS} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="sy-webhook">Send events to</Label>
              <SimpleSelect
                id="sy-webhook"
                className="w-full"
                value={webhookId}
                onChange={setWebhookId}
                options={[{ value: 'new', label: 'A new webhook' }, ...(webhooks.data?.data ?? []).map((w) => ({ value: w.id, label: w.name }))]}
              />
            </div>
          </div>
          <p className="-mt-2 text-xs text-muted-foreground">
            Events land on that webhook: add a destination there to receive them in your app.
          </p>
          <label className="flex cursor-pointer items-start gap-3 text-sm">
            <input type="checkbox" className="mt-0.5 size-4 shrink-0 accent-[var(--brand)]" checked={emitExisting} onChange={(e) => setEmitExisting(e.target.checked)} />
            <span className="min-w-0">
              Also send events for records that already exist
              <span className="block text-xs text-muted-foreground">Off: the first run only takes note of them, and only later changes become events.</span>
            </span>
          </label>
        </>
      )}
      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={create.isPending || !model}>
          {create.isPending ? 'Adding…' : 'Add sync'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function RunHistory({ s }: { s: Sync }) {
  const runs = useSyncRuns(s.id)
  const rows = runs.data?.data ?? []
  const paged = usePaged(rows, 10)
  return (
    <div className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>Run history</DialogTitle>
        <DialogDescription>
          {s.integration_name} {s.model_name.toLowerCase()} for {s.end_user_id}. The last 50 runs.
        </DialogDescription>
      </DialogHeader>
      {runs.error ? (
        <ErrorState error={runs.error} />
      ) : runs.isPending ? (
        <Skeleton className="h-24 w-full" />
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No runs yet.</p>
      ) : (
        <div className="max-h-96 overflow-y-auto rounded-lg border">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="w-28">When</TableHead>
                <TableHead>Result</TableHead>
                <TableHead className="w-20">Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {paged.items.map((r) => (
                <TableRow key={r.id}>
                  <TableCell className="align-top text-xs whitespace-normal text-muted-foreground" title={dateTime(r.started_at)}>
                    {timeAgo(r.started_at)}
                  </TableCell>
                  <TableCell className="align-top text-xs whitespace-normal">
                    {r.status === 'running' ? (
                      'running…'
                    ) : (
                      <>
                        {r.fetched} read · <span className="font-medium">{r.created} new</span> · <span className="font-medium">{r.updated} changed</span>
                      </>
                    )}
                    {r.error && (
                      <div className="mt-0.5 line-clamp-3 break-words text-red-700 dark:text-red-400" title={r.error}>
                        {r.error}
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="align-top">
                    <Badge variant="outline" className={r.status === 'ok' ? green : r.status === 'error' ? red : grey}>
                      {r.status}
                    </Badge>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <Pager paged={paged} noun="runs" />
        </div>
      )}
    </div>
  )
}
