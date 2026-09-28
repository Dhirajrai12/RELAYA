import { CheckCircle2Icon, PlugIcon, RefreshCwIcon, SendIcon, type LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { StatusBadge } from '@/components/common'
import { SeverityBadge } from '@/components/contract'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { compact, dateTime, timeAgo } from '@/lib/format'
import { useConnections, useIncidents, useOutboundApps, useRecentEvents, useSyncs, useWebhooks } from '@/lib/queries'
import { cn } from '@/lib/utils'

// Extra overview cards: what is broken right now, what just arrived, and the
// state of connected apps, syncs and outbound webhooks.

function CardHead({ title, description, to }: { title: string; description: string; to: string }) {
  return (
    <CardHeader className="flex flex-row items-start justify-between gap-3">
      <div className="min-w-0 flex-1">
        <CardTitle>{title}</CardTitle>
        <CardDescription className="mt-1.5">{description}</CardDescription>
      </div>
      <Link to={to} className="shrink-0 text-sm text-brand hover:underline">
        View all
      </Link>
    </CardHeader>
  )
}

export function OpenIncidentsCard() {
  const q = useIncidents('open')
  const list = [...(q.data?.data ?? [])].sort(
    (a, b) => Number(b.severity === 'breaking') - Number(a.severity === 'breaking') || b.last_seen_at.localeCompare(a.last_seen_at),
  )
  return (
    <Card>
      <CardHead title="Open incidents" description="Payload changes that break or may break your integrations" to="/incidents" />
      <CardContent>
        {q.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : list.length === 0 ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <CheckCircle2Icon className="size-4 text-[#0ca30c]" /> Nothing open. Every contract matches.
          </p>
        ) : (
          <ul className="-mx-2 divide-y">
            {list.slice(0, 5).map((i) => (
              <li key={i.id}>
                <Link to="/incidents" className="block rounded-md px-2 py-2.5 hover:bg-muted/60">
                  <div className="flex min-w-0 items-center gap-2">
                    <SeverityBadge severity={i.severity} />
                    <span className="truncate text-sm font-medium">{i.title}</span>
                  </div>
                  <div className="mt-0.5 truncate text-xs text-muted-foreground">
                    {i.webhook_name} · <span className="font-mono">{i.event_type}</span> · {compact(i.event_count)} event{i.event_count === 1 ? '' : 's'} ·{' '}
                    <span title={dateTime(i.last_seen_at)}>{timeAgo(i.last_seen_at)}</span>
                  </div>
                </Link>
              </li>
            ))}
            {list.length > 5 && <li className="px-2 pt-2.5 text-xs text-muted-foreground">+{list.length - 5} more</li>}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

export function RecentEventsCard({ className }: { className?: string }) {
  const q = useRecentEvents(8)
  const webhooks = useWebhooks()
  const name = (id: string) => webhooks.data?.data.find((w) => w.id === id)?.name ?? '—'
  const list = q.data?.data ?? []
  return (
    <Card className={className}>
      <CardHead title="Latest events" description="The newest deliveries, updated live" to="/events" />
      <CardContent>
        {q.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">No events yet.</p>
        ) : (
          <ul className="-mx-2 divide-y">
            {list.map((e) => (
              <li key={e.id}>
                <Link to={`/events?event=${e.id}`} className="flex items-center gap-3 rounded-md px-2 py-2 hover:bg-muted/60">
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-mono text-xs">{e.type || '(no type)'}</div>
                    <div className="truncate text-xs text-muted-foreground">
                      {name(e.webhook_id)} · <span title={dateTime(e.received_at)}>{timeAgo(e.received_at)}</span>
                      {e.delivery === 'failed' && <span className="text-red-700 dark:text-red-400"> · forwarding failed</span>}
                      {(e.contract_status === 'breaking' || e.contract_status === 'suspicious') && (
                        <span className="text-amber-700 dark:text-amber-400"> · contract {e.contract_status}</span>
                      )}
                    </div>
                  </div>
                  <StatusBadge status={e.status} />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function Block({ icon: Icon, title, to, main, sub, bad }: { icon: LucideIcon; title: string; to: string; main: ReactNode; sub: ReactNode; bad?: boolean }) {
  return (
    <Link to={to} className="flex items-start gap-3 rounded-md px-2 py-2.5 hover:bg-muted/60">
      <div className={cn('mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md', bad ? 'bg-red-500/10 text-red-600' : 'bg-brand/10 text-brand')}>
        <Icon className="size-4" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline justify-between gap-x-2">
          <span className="text-sm font-medium">{title}</span>
          <span className="text-sm tabular-nums">{main}</span>
        </div>
        <div className="text-xs text-muted-foreground">{sub}</div>
      </div>
    </Link>
  )
}

export function IntegrationsCard({ className }: { className?: string }) {
  const conns = useConnections()
  const syncs = useSyncs()
  const apps = useOutboundApps()
  const c = conns.data?.data ?? []
  const s = syncs.data?.data ?? []
  const a = apps.data?.data ?? []
  const broken = c.filter((x) => x.status === 'broken').length
  const failing = s.filter((x) => x.enabled && x.last_status === 'error').length
  const paused = s.filter((x) => !x.enabled).length
  const msgs = a.reduce((n, x) => n + x.messages_24h, 0)
  const failed = a.reduce((n, x) => n + x.failed_24h, 0)
  const endpoints = a.reduce((n, x) => n + x.endpoints, 0)
  const loading = conns.isPending || syncs.isPending || apps.isPending

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>Connected apps & outbound</CardTitle>
        <CardDescription className="mt-1.5">Accounts your users connected, scheduled syncs and webhooks you send</CardDescription>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-32 w-full" />
        ) : (
          <div className="-mx-2 divide-y">
            <Block
              icon={PlugIcon}
              title="Connections"
              to="/connections"
              bad={broken > 0}
              main={compact(c.length)}
              sub={c.length === 0 ? 'No accounts connected yet' : broken ? <span className="text-red-700 dark:text-red-400">{broken} need reconnecting</span> : 'All working'}
            />
            <Block
              icon={RefreshCwIcon}
              title="Syncs"
              to="/connections"
              bad={failing > 0}
              main={compact(s.length)}
              sub={
                s.length === 0 ? (
                  'No syncs yet'
                ) : (
                  <>
                    {failing ? <span className="text-red-700 dark:text-red-400">{failing} failing</span> : 'All running'}
                    {paused > 0 && ` · ${paused} paused`}
                    {` · ${compact(s.reduce((n, x) => n + x.events, 0))} events made`}
                  </>
                )
              }
            />
            <Block
              icon={SendIcon}
              title="Outbound"
              to="/outbound"
              bad={failed > 0}
              main={`${compact(msgs)} sent`}
              sub={
                a.length === 0 ? (
                  'No apps yet'
                ) : (
                  <>
                    {a.length} app{a.length === 1 ? '' : 's'} · {endpoints} endpoint{endpoints === 1 ? '' : 's'}
                    {failed > 0 ? <span className="text-red-700 dark:text-red-400"> · {failed} failed (24h)</span> : ' · no failures (24h)'}
                  </>
                )
              }
            />
          </div>
        )}
      </CardContent>
    </Card>
  )
}
