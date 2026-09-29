import { useQuery } from '@tanstack/react-query'
import { CheckCircle2Icon, CircleHelpIcon, TriangleAlertIcon, XCircleIcon } from 'lucide-react'

import { API_BASE } from '@/lib/api'
import { cn } from '@/lib/utils'
import { PublicPage } from './layout'

interface StatusPageData {
  status: 'operational' | 'degraded' | 'outage'
  components: { id: string; name: string; help: string; status: 'operational' | 'down' | 'unknown' }[]
  days: { date: string; uptime: Record<string, number | null> }[]
  updated_at: string
}

const overall = {
  operational: { text: 'All systems operational', cls: 'border-[#14b886]/40 bg-[#14b886]/10 text-l-accent', icon: CheckCircle2Icon },
  degraded: { text: 'Some systems are having problems', cls: 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400', icon: TriangleAlertIcon },
  outage: { text: 'Major outage', cls: 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-400', icon: XCircleIcon },
}

const state = {
  operational: { text: 'Operational', cls: 'text-l-accent', icon: CheckCircle2Icon },
  down: { text: 'Down', cls: 'text-red-700 dark:text-red-400', icon: XCircleIcon },
  unknown: { text: 'Unknown', cls: 'text-l-subtle', icon: CircleHelpIcon },
}

/** Bar color for a day's uptime. Color is never the only signal: each bar has a text tooltip and label. */
function barClass(pct: number | null) {
  if (pct === null) return 'bg-l-border'
  if (pct >= 99.9) return 'bg-[#14b886]'
  if (pct >= 99) return 'bg-lime-500'
  if (pct >= 95) return 'bg-amber-500'
  return 'bg-red-500'
}

export function StatusPage() {
  const { data, error, isPending } = useQuery({
    queryKey: ['public-status'],
    queryFn: async () => {
      const res = await fetch(`${API_BASE}/status`)
      if (!res.ok) throw new Error(`Status is unavailable right now (HTTP ${res.status}).`)
      return (await res.json()) as StatusPageData
    },
    refetchInterval: 60_000,
  })

  return (
    <PublicPage
      eyebrow="Status"
      title="System status"
      intro="Live health of Relaya, checked every minute. Shows the last 90 days on larger screens, 30 on phones."
    >
      {error ? (
        <div className="rounded-xl border border-red-500/40 bg-red-500/10 p-4 text-sm text-red-700 dark:text-red-400">{(error as Error).message}</div>
      ) : isPending ? (
        <div className="h-64 animate-pulse rounded-xl bg-l-soft" />
      ) : (
        <>
          <Banner status={data.status} />
          <ul className="mt-8 divide-y divide-l-border rounded-xl border border-l-border">
            {data.components.map((c) => (
              <ComponentRow key={c.id} c={c} days={data.days} />
            ))}
          </ul>
          <p className="mt-4 text-xs text-l-subtle">
            Updated {new Date(data.updated_at).toLocaleTimeString()} · refreshes every minute · days are in UTC.
          </p>
        </>
      )}
    </PublicPage>
  )
}

function Banner({ status }: { status: StatusPageData['status'] }) {
  const o = overall[status]
  const Icon = o.icon
  return (
    <div className={cn('flex items-center gap-3 rounded-xl border px-5 py-4 text-base font-semibold', o.cls)} role="status">
      <Icon className="size-5 shrink-0" />
      {o.text}
    </div>
  )
}

function ComponentRow({ c, days }: { c: StatusPageData['components'][number]; days: StatusPageData['days'] }) {
  const s = state[c.status]
  const Icon = s.icon
  const known = days.map((d) => d.uptime[c.id]).filter((v): v is number => v !== null && v !== undefined)
  const avg = known.length ? known.reduce((a, b) => a + b, 0) / known.length : null
  return (
    <li className="p-4 sm:p-5">
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1">
        <div className="min-w-0">
          <div className="font-medium text-l-text">{c.name}</div>
          <div className="text-sm text-l-subtle">{c.help}</div>
        </div>
        <span className={cn('inline-flex shrink-0 items-center gap-1.5 text-sm font-medium', s.cls)}>
          <Icon className="size-4" />
          {s.text}
        </span>
      </div>
      <div className="mt-3 flex h-8 items-end gap-[2px]" aria-label={`${c.name}: daily uptime`}>
        {days.map((d, i) => {
          const v = d.uptime[c.id] ?? null
          return (
            <span
              key={d.date}
              title={`${d.date}: ${v === null ? 'no data' : `${v.toFixed(2)}% up`}`}
              className={cn('h-full min-w-0 flex-1 rounded-[2px] transition hover:opacity-70', barClass(v), i < days.length - 30 && 'hidden sm:block')}
            />
          )
        })}
      </div>
      <div className="mt-1.5 flex justify-between text-xs text-l-subtle">
        <span className="hidden sm:inline">90 days ago</span>
        <span className="sm:hidden">30 days ago</span>
        <span>{avg === null ? 'no data yet' : `${avg.toFixed(2)}% uptime`}</span>
        <span>Today</span>
      </div>
    </li>
  )
}
