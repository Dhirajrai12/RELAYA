import { ArrowLeftIcon, RotateCcwIcon, SparklesIcon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { toast } from 'sonner'

import { ErrorState, PageHeader } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { ContractStateBadge, FindingDiff, SeverityBadge } from '@/components/contract'
import { Pager, usePaged } from '@/components/pager'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage } from '@/lib/api'
import { dateTime, timeAgo, kindLabel } from '@/lib/format'
import { useContract, useContractFindings, useCreateContractVersion, useRelearnContract } from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { ContractDetail, ContractField } from '@/lib/types'

export function ContractDetailPage() {
  const { id = '' } = useParams()
  const q = useContract(id)
  if (q.error) return <ErrorState error={q.error} />
  if (q.isPending) return <Skeleton className="h-96 w-full" />
  return <ContractView d={q.data} />
}

// Field names that usually matter for money/customer flows: suggested as critical.
const SUGGEST = /(^|\.)(amount|total|status|currency|id|order_id|payment_id|email|customer_id|state)$/

function ContractView({ d }: { d: ContractDetail }) {
  const c = d.contract
  const canManage = useCanManage()
  const create = useCreateContractVersion(c.id)
  const relearn = useRelearnContract(c.id)
  const reviewing = c.status !== 'active'

  // Critical selection: the active version's, or suggestions while reviewing.
  const initial = useMemo(
    () =>
      new Set(
        d.fields
          .filter((f) => (reviewing ? f.required && SUGGEST.test(f.path) && !f.path.includes('[]') : f.critical))
          .map((f) => f.path),
      ),
    [d.fields, reviewing],
  )
  const [critical, setCritical] = useState<Set<string>>(initial)
  const [synced, setSynced] = useState(initial)
  if (synced !== initial) {
    // server data changed (e.g. a new version was saved): reset the selection
    setSynced(initial)
    setCritical(initial)
  }
  const dirty = critical.size !== initial.size || [...critical].some((p) => !initial.has(p))
  const toggle = (p: string) =>
    setCritical((s) => {
      const n = new Set(s)
      if (n.has(p)) n.delete(p)
      else n.add(p)
      return n
    })

  const save = (source: 'observed' | 'active') =>
    create
      .mutateAsync({ critical_fields: [...critical], source })
      .then((r) => toast.success(reviewing ? `Contract activated (v${r.version})` : `Saved as v${r.version}`), (e) => toast.error(errorMessage(e)))

  const newFields = Object.entries(d.new_fields)
  const newPaged = usePaged(newFields, 10)
  const versionsPaged = usePaged(d.versions, 10)
  const visibleFields = reviewing ? d.fields : d.fields.filter((f) => f.in_version || f.observed_types)

  return (
    <>
      <Link to="/contracts" className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeftIcon className="size-4" /> Contracts
      </Link>
      <PageHeader
        title={c.event_type}
        description={`${c.webhook_name} · first event ${timeAgo(c.first_seen_at)} · last ${timeAgo(c.last_seen_at)}${c.fingerprint ? ` · ${c.fingerprint}` : ''}`}
        actions={
          <>
            <ContractStateBadge state={c.status} version={c.active_version} />
            {canManage && c.status !== 'learning' && (
              <ConfirmButton
                variant="ghost"
                title="Relearn this contract?"
                description="The active version is dropped and Relaya learns the shape again from new events. Open incidents for it are resolved."
                confirmLabel="Relearn"
                onConfirm={() => relearn.mutateAsync().then(() => toast.success('Learning again from new events'))}
              >
                <RotateCcwIcon /> Relearn
              </ConfirmButton>
            )}
          </>
        }
      />

      <div className="grid gap-6">
        {reviewing ? (
          <ReviewCard d={d} criticalCount={critical.size} canManage={canManage} busy={create.isPending} onActivate={() => save('observed')} />
        ) : (
          newFields.length > 0 && (
            <Card className="border-sky-500/30">
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <SparklesIcon className="size-4 text-sky-600" /> New fields seen
                </CardTitle>
                <CardDescription>
                  Events now include fields that aren't in v{c.active_version}. That's compatible; accept them to include them in
                  the contract.
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="rounded-lg border">
                <ul className="divide-y text-sm">
                  {newPaged.items.map(([path, nf]) => (
                    <li key={path} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3 py-2">
                      <FieldPath path={path} />
                      <span className="font-mono text-xs text-muted-foreground">{nf.types}</span>
                      <span className="ml-auto text-xs text-muted-foreground">
                        {nf.count} events · since {timeAgo(nf.first_seen)}
                      </span>
                    </li>
                  ))}
                </ul>
                <Pager paged={newPaged} noun="new fields" />
                </div>
                {canManage && (
                  <ConfirmButton
                    title="Accept the current shape as a new version?"
                    description={`A new version is built from the ${d.observed_samples} events received since v${c.active_version}, with your critical fields. Open incidents for this contract are resolved.`}
                    confirmLabel="Accept changes"
                    onConfirm={() => save('observed')}
                  >
                    Accept changes
                  </ConfirmButton>
                )}
              </CardContent>
            </Card>
          )
        )}

        <Card>
          <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle>{reviewing ? 'Learned fields' : `Fields in v${c.active_version}`}</CardTitle>
              <CardDescription className="mt-1.5">
                {canManage
                  ? 'Tick the fields your integration depends on. If one goes missing, changes type or becomes null, an incident opens.'
                  : 'Critical fields open an incident when they go missing, change type or become null.'}
              </CardDescription>
            </div>
            {canManage && !reviewing && dirty && (
              <div className="flex gap-2">
                <Button variant="ghost" size="sm" onClick={() => setCritical(initial)}>
                  Undo
                </Button>
                <Button size="sm" disabled={create.isPending} onClick={() => save('active')}>
                  Save critical fields
                </Button>
              </div>
            )}
          </CardHeader>
          <CardContent>
            <FieldsTable fields={visibleFields} critical={critical} editable={canManage} onToggle={toggle} active={!reviewing} />
          </CardContent>
        </Card>

        {!reviewing && <FindingsCard contractId={c.id} total={d.findings_total} />}

        {d.versions.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle>Versions</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="rounded-lg border">
              <ul className="divide-y text-sm">
                {versionsPaged.items.map((v) => (
                  <li key={v.version} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-3 py-2">
                    <span className="font-medium">v{v.version}</span>
                    {v.version === c.active_version && <Badge variant="secondary">active</Badge>}
                    <span className="font-mono text-xs text-muted-foreground">{v.fingerprint}</span>
                    <span className="text-xs text-muted-foreground">{v.critical_count} critical</span>
                    <span className="ml-auto text-xs text-muted-foreground" title={dateTime(v.created_at)}>
                      {timeAgo(v.created_at)}
                    </span>
                  </li>
                ))}
              </ul>
              <Pager paged={versionsPaged} noun="versions" />
              </div>
            </CardContent>
          </Card>
        )}
      </div>
    </>
  )
}

function ReviewCard({
  d,
  criticalCount,
  canManage,
  busy,
  onActivate,
}: {
  d: ContractDetail
  criticalCount: number
  canManage: boolean
  busy: boolean
  onActivate: () => void
}) {
  const c = d.contract
  const pct = Math.min(100, Math.round((c.samples / c.min_samples) * 100))
  const learning = c.status === 'learning'
  return (
    <Card className={cn(learning ? '' : 'border-sky-500/40 bg-sky-500/5')}>
      <CardHeader>
        <CardTitle>{learning ? 'Learning the shape of this event' : 'Ready to review'}</CardTitle>
        <CardDescription>
          {learning
            ? `Relaya proposes a contract after ${c.min_samples} events (or 24 hours with at least 3). You can activate earlier if the fields below look complete.`
            : `Learned from ${c.samples} events. Mark the critical fields below, then activate: from then on every event is checked.`}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div>
          <div className="mb-1 flex justify-between text-xs text-muted-foreground">
            <span>
              {c.samples} of {c.min_samples} events
            </span>
            <span>{pct}%</span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
            <div className="h-full rounded-full bg-brand transition-[width] duration-500" style={{ width: `${pct}%` }} />
          </div>
        </div>
        {canManage && (
          <div className="flex flex-wrap items-center gap-3">
            <Button onClick={onActivate} disabled={busy || c.samples === 0}>
              {busy ? 'Activating…' : `Activate contract${criticalCount ? ` with ${criticalCount} critical field${criticalCount > 1 ? 's' : ''}` : ''}`}
            </Button>
            {criticalCount === 0 && <span className="text-xs text-amber-700 dark:text-amber-400">No critical fields ticked: changes will only raise warnings.</span>}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function FieldPath({ path }: { path: string }) {
  const i = path.lastIndexOf('.')
  return (
    <span className="min-w-0 break-all font-mono text-xs">
      {i > 0 && <span className="text-muted-foreground">{path.slice(0, i + 1)}</span>}
      <span className="font-medium">{i > 0 ? path.slice(i + 1) : path}</span>
    </span>
  )
}

function FieldsTable({
  fields,
  critical,
  editable,
  onToggle,
  active,
}: {
  fields: ContractField[]
  critical: Set<string>
  editable: boolean
  onToggle: (p: string) => void
  active: boolean
}) {
  const paged = usePaged(fields, 25)
  if (fields.length === 0) return <p className="text-sm text-muted-foreground">No fields yet.</p>
  return (
    <div className="rounded-lg border">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
        <thead className="bg-muted/50 text-xs text-muted-foreground">
          <tr>
            <th className="w-20 px-3 py-2 text-left font-medium">Critical</th>
            <th className="px-3 py-2 text-left font-medium">Field</th>
            <th className="px-3 py-2 text-left font-medium">Type</th>
            <th className="hidden px-3 py-2 text-left font-medium sm:table-cell">Required</th>
            <th className="hidden px-3 py-2 text-left font-medium md:table-cell">Allowed values</th>
            {active && <th className="hidden px-3 py-2 text-left font-medium lg:table-cell">Since this version</th>}
          </tr>
        </thead>
        <tbody className="divide-y">
          {paged.items.map((f) => {
            const drift = active && f.in_version && f.observed_types?.some((t) => !f.types?.includes(t))
            return (
              <tr key={f.path} className={cn(critical.has(f.path) && 'bg-brand/5')}>
                <td className="px-3 py-2">
                  <input
                    type="checkbox"
                    className="size-4 accent-[var(--brand)]"
                    aria-label={`Mark ${f.path} as critical`}
                    checked={critical.has(f.path)}
                    disabled={!editable || !f.types}
                    onChange={() => onToggle(f.path)}
                  />
                </td>
                <td className="px-3 py-2">
                  <FieldPath path={f.path} />
                </td>
                <td className="px-3 py-2 font-mono text-xs">{f.types?.join(' | ') ?? '—'}</td>
                <td className="hidden px-3 py-2 text-xs sm:table-cell">{f.required ? 'always' : <span className="text-muted-foreground">sometimes</span>}</td>
                <td className="hidden px-3 py-2 md:table-cell">
                  {f.enum?.length ? (
                    <span className="flex flex-wrap gap-1">
                      {f.enum.map((v) => (
                        <Badge key={v} variant="secondary" className="font-mono text-[11px]">
                          {v}
                        </Badge>
                      ))}
                    </span>
                  ) : (
                    <span className="text-xs text-muted-foreground">any</span>
                  )}
                </td>
                {active && (
                  <td className="hidden px-3 py-2 text-xs lg:table-cell">
                    {!f.in_version ? (
                      <span className="text-sky-700 dark:text-sky-400">new field</span>
                    ) : drift ? (
                      <span className="text-amber-700 dark:text-amber-400">also seen as {f.observed_types?.filter((t) => !f.types?.includes(t)).join(', ')}</span>
                    ) : f.observed_seen ? (
                      <span className="text-muted-foreground">in {f.observed_seen} events</span>
                    ) : (
                      <span className="text-muted-foreground">not seen yet</span>
                    )}
                  </td>
                )}
              </tr>
            )
          })}
        </tbody>
        </table>
      </div>
      <Pager paged={paged} noun="fields" />
    </div>
  )
}

/** Every finding for the contract, 10 per page, fetched from the server 100 at a time. */
function FindingsCard({ contractId, total }: { contractId: string; total: number }) {
  const findings = useContractFindings(contractId)
  const rows = findings.data?.pages.flatMap((p) => p.data) ?? []
  const paged = usePaged(rows, 10)
  return (
    <Card>
      <CardHeader>
        <CardTitle>Findings</CardTitle>
        <CardDescription>
          Warnings and breaking changes found in events, newest first{total > 0 ? ` (${total} in all)` : ''}.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {findings.error ? (
          <ErrorState error={findings.error} />
        ) : findings.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">No warnings or breaking changes. Every checked event matches.</p>
        ) : (
          <div className="rounded-lg border">
            <ul className="divide-y">
              {paged.items.map((v) => (
                <li key={v.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
                  <SeverityBadge severity={v.severity} />
                  <span>{kindLabel(v.kind)}</span>
                  <FieldPath path={v.path} />
                  <FindingDiff expected={v.expected} actual={v.actual} />
                  <Link to={`/events?event=${v.event_id}`} className="ml-auto text-xs text-muted-foreground underline-offset-4 hover:underline">
                    {timeAgo(v.created_at)} · view event
                  </Link>
                </li>
              ))}
            </ul>
            <Pager paged={paged} hasMore={!!findings.hasNextPage} loadMore={() => findings.fetchNextPage()} noun="findings" />
          </div>
        )}
      </CardContent>
    </Card>
  )
}
