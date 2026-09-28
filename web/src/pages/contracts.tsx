import { FileCheck2Icon } from 'lucide-react'
import { Link } from 'react-router-dom'

import { EmptyState, ErrorState, PageHeader } from '@/components/common'
import { ContractStateBadge } from '@/components/contract'
import { LiveIndicator } from '@/components/live-indicator'
import { Pager, usePaged } from '@/components/pager'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { timeAgo } from '@/lib/format'
import { useContracts } from '@/lib/queries'
import type { Contract } from '@/lib/types'

export function ContractsPage() {
  const { data, error, isPending } = useContracts()
  const contracts = data?.data ?? []
  const paged = usePaged(contracts)

  return (
    <>
      <PageHeader
        title="Contracts"
        description="Relaya learns the shape of each event type and flags changes: new fields are fine, a missing or retyped critical field opens an incident."
        actions={<LiveIndicator />}
      />
      {error ? (
        <ErrorState error={error} />
      ) : isPending ? (
        <Skeleton className="h-48 w-full" />
      ) : contracts.length === 0 ? (
        <EmptyState title="No contracts yet" icon={FileCheck2Icon}>
          Contracts are created automatically when JSON events with a type (like payment.captured) arrive on a webhook.
          Send a few events and they'll appear here.
        </EmptyState>
      ) : (
        <>
          <ul className="divide-y rounded-lg border md:hidden">
            {paged.items.map((c) => (
              <li key={c.id}>
                <Link to={`/contracts/${c.id}`} className="block px-3 py-3 active:bg-muted">
                  <div className="flex items-center justify-between gap-2">
                    <span className="min-w-0 truncate font-mono text-sm">{c.event_type}</span>
                    <ContractStateBadge state={c.status} version={c.active_version} />
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">
                    {c.webhook_name} · <Health c={c} />
                  </div>
                </Link>
              </li>
            ))}
          </ul>
          <div className="hidden rounded-lg border md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Event type</TableHead>
                  <TableHead>Webhook</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Fields</TableHead>
                  <TableHead>Last 24h</TableHead>
                  <TableHead className="text-right">Last event</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {paged.items.map((c) => (
                  <TableRow key={c.id}>
                    <TableCell>
                      <Link to={`/contracts/${c.id}`} className="font-mono text-sm hover:underline">
                        {c.event_type}
                      </Link>
                    </TableCell>
                    <TableCell className="max-w-40 truncate">{c.webhook_name}</TableCell>
                    <TableCell>
                      <ContractStateBadge state={c.status} version={c.active_version} />
                    </TableCell>
                    <TableCell className="text-sm text-muted-foreground">
                      {c.field_count}
                      {c.critical_count > 0 && ` · ${c.critical_count} critical`}
                    </TableCell>
                    <TableCell className="text-sm">
                      <Health c={c} />
                    </TableCell>
                    <TableCell className="text-right text-xs text-muted-foreground">{timeAgo(c.last_seen_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <Pager paged={paged} noun="contracts" className="mt-3 rounded-lg border" />
        </>
      )}
    </>
  )
}

function Health({ c }: { c: Contract }) {
  if (c.status === 'learning')
    return (
      <span className="text-muted-foreground">
        learning {Math.min(c.samples, c.min_samples)}/{c.min_samples} events
      </span>
    )
  if (c.status === 'proposed') return <span className="text-sky-700 dark:text-sky-400">review and activate</span>
  const parts = []
  if (c.open_incidents) parts.push(<span key="i" className="font-medium text-red-700 dark:text-red-400">{c.open_incidents} open incident{c.open_incidents > 1 ? 's' : ''}</span>)
  if (c.breaking_24h) parts.push(<span key="b" className="text-red-700 dark:text-red-400">{c.breaking_24h} breaking</span>)
  if (c.suspicious_24h) parts.push(<span key="s" className="text-amber-700 dark:text-amber-400">{c.suspicious_24h} warnings</span>)
  if (c.repaired_24h) parts.push(<span key="r" className="text-violet-700 dark:text-violet-400">{c.repaired_24h} repaired</span>)
  if (c.new_fields) parts.push(<span key="n" className="text-sky-700 dark:text-sky-400">{c.new_fields} new fields</span>)
  if (parts.length === 0) return <span className="text-emerald-700 dark:text-emerald-400">all events match</span>
  return <span className="inline-flex flex-wrap gap-x-2">{parts}</span>
}
