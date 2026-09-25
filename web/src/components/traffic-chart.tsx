import { ShieldAlertIcon } from 'lucide-react'
import { useState } from 'react'
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { compact } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { HourBucket } from '@/lib/types'

// Stacked hourly columns: received (categorical slot 1, blue) under rejected
// (status "critical" red). Colors come from --viz-* tokens in index.css, which
// have separate, validated light and dark steps.

interface Row extends HourBucket {
  label: string
}

const hourLabel = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

export function TrafficChart({ hours }: { hours: HourBucket[] }) {
  const [view, setView] = useState<'chart' | 'table'>('chart')
  const rows: Row[] = hours.map((h) => ({ ...h, label: hourLabel(h.hour) }))
  const total = hours.reduce((n, h) => n + h.received + h.rejected, 0)

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <Legend />
        <div role="radiogroup" aria-label="View" className="inline-flex rounded-lg border p-0.5 text-xs">
          {(['chart', 'table'] as const).map((v) => (
            <button
              key={v}
              type="button"
              role="radio"
              aria-checked={view === v}
              onClick={() => setView(v)}
              className={cn('rounded-md px-2.5 py-1 capitalize text-muted-foreground', view === v && 'bg-muted text-foreground')}
            >
              {v}
            </button>
          ))}
        </div>
      </div>

      {view === 'table' ? (
        <TableView rows={rows} />
      ) : (
        <div
          className="h-56 w-full md:h-64"
          role="img"
          aria-label={`Webhook deliveries per hour over the last 24 hours: ${total} in total.`}
        >
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={rows} margin={{ top: 8, right: 4, bottom: 0, left: 0 }} barCategoryGap={2}>
              <CartesianGrid vertical={false} stroke="var(--viz-grid)" strokeWidth={1} />
              <XAxis
                dataKey="label"
                interval={5}
                tickLine={false}
                axisLine={{ stroke: 'var(--viz-grid)' }}
                tick={{ fill: 'var(--viz-axis)', fontSize: 11 }}
                tickMargin={8}
              />
              <YAxis
                allowDecimals={false}
                width={36}
                tickLine={false}
                axisLine={false}
                tick={{ fill: 'var(--viz-axis)', fontSize: 11, style: { fontVariantNumeric: 'tabular-nums' } }}
                tickFormatter={(v: number) => compact(v)}
              />
              <Tooltip cursor={{ fill: 'var(--muted)', opacity: 0.7 }} content={<ChartTooltip />} isAnimationActive={false} />
              <Bar dataKey="received" stackId="a" maxBarSize={24} shape={segment('received')} isAnimationActive={false} />
              <Bar dataKey="rejected" stackId="a" maxBarSize={24} shape={segment('rejected')} isAnimationActive={false} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
    </div>
  )
}

function Legend() {
  return (
    <div className="flex items-center gap-4 text-xs text-muted-foreground">
      <span className="inline-flex items-center gap-1.5">
        <span className="size-2.5 rounded-sm" style={{ background: 'var(--viz-received)' }} />
        Received
      </span>
      <span className="inline-flex items-center gap-1.5">
        <span className="size-2.5 rounded-sm" style={{ background: 'var(--viz-rejected)' }} />
        <ShieldAlertIcon className="size-3.5" style={{ color: 'var(--viz-rejected)' }} />
        Rejected (bad signature)
      </span>
    </div>
  )
}

interface ShapeProps {
  x?: number
  y?: number
  width?: number
  height?: number
  payload?: Row
}

/**
 * One stacked segment. The topmost segment of a column gets 4px rounded
 * corners (square at the baseline); a 2px surface gap separates the two.
 */
function segment(kind: 'received' | 'rejected') {
  return function Segment({ x = 0, y = 0, width = 0, height = 0, payload }: ShapeProps) {
    if (height <= 0 || !payload) return null
    const isTop = kind === 'rejected' || payload.rejected === 0
    let h = height
    if (kind === 'rejected' && payload.received > 0) h = Math.max(height - 2, 1) // 2px gap above "received"
    const r = isTop ? Math.min(4, h, width / 2) : 0
    const d = `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + width - r}Q${x + width},${y} ${x + width},${y + r}V${y + h}Z`
    return <path d={d} style={{ fill: `var(--viz-${kind})` }} />
  }
}

interface TooltipProps {
  active?: boolean
  payload?: { payload: Row }[]
}

function ChartTooltip({ active, payload }: TooltipProps) {
  const row = payload?.[0]?.payload
  if (!active || !row) return null
  const end = new Date(new Date(row.hour).getTime() + 3600_000)
  return (
    <div className="min-w-40 rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md">
      <div className="mb-1.5 font-medium">
        {row.label}–{hourLabel(end.toISOString())}
      </div>
      <TooltipRow color="var(--viz-received)" label="Received" value={row.received} />
      <TooltipRow color="var(--viz-rejected)" label="Rejected" value={row.rejected} />
    </div>
  )
}

function TooltipRow({ color, label, value }: { color: string; label: string; value: number }) {
  return (
    <div className="flex items-center gap-2 py-0.5">
      <span className="size-2 rounded-sm" style={{ background: color }} />
      <span className="text-muted-foreground">{label}</span>
      <span className="ml-auto font-medium tabular-nums">{value.toLocaleString()}</span>
    </div>
  )
}

function TableView({ rows }: { rows: Row[] }) {
  return (
    <div className="max-h-64 overflow-auto rounded-lg border">
      <table className="w-full text-sm">
        <thead className="sticky top-0 bg-muted text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 text-left font-medium">Hour</th>
            <th className="px-3 py-2 text-right font-medium">Received</th>
            <th className="px-3 py-2 text-right font-medium">Rejected</th>
          </tr>
        </thead>
        <tbody className="tabular-nums">
          {[...rows].reverse().map((r) => (
            <tr key={r.hour} className="border-t">
              <td className="px-3 py-1.5">{r.label}</td>
              <td className="px-3 py-1.5 text-right">{r.received.toLocaleString()}</td>
              <td className="px-3 py-1.5 text-right">{r.rejected.toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

