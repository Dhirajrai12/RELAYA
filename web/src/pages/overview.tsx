import {
  ActivityIcon,
  ArrowRightIcon,
  CheckCircle2Icon,
  CircleDashedIcon,
  CircleIcon,
  SendIcon,
  ShieldAlertIcon,
} from 'lucide-react'
import { Link } from 'react-router-dom'

import { ErrorState, PageHeader, StatTile } from '@/components/common'
import { TrafficChart } from '@/components/traffic-chart'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { compact, providerLabel, timeAgo } from '@/lib/format'
import { useEventStats, usePrefetchWebhook, useProjects, useWebhooks } from '@/lib/queries'
import { cn } from '@/lib/utils'
import type { Webhook, WebhookHealth } from '@/lib/types'

export function OverviewPage() {
  const stats = useEventStats()
  const webhooks = useWebhooks()
  const projects = useProjects()

  if (stats.error) return <ErrorState error={stats.error} />
  if (stats.isPending || webhooks.isPending || projects.isPending) return <OverviewSkeleton />

  const s = stats.data
  const hooks = (webhooks.data?.data ?? []).filter((w) => w.kind !== 'outbound')
  const total = s.totals.received + s.totals.rejected
  const everReceived = s.webhooks.some((w) => w.last_received_at)
  const lastAt = s.webhooks.map((w) => w.last_received_at).filter(Boolean).sort().at(-1) ?? null
  const failing = s.webhooks.filter((w) => w.rejected > 0).length

  return (
    <>
      <PageHeader title="Overview" description="Webhook traffic and integration health over the last 24 hours." />

      {!everReceived && (
        <GettingStarted hasProject={(projects.data?.data.length ?? 0) > 0} hasWebhook={hooks.length > 0} />
      )}

      <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatTile label="Deliveries (24h)" value={compact(total)} icon={ActivityIcon} sub={`${compact(s.totals.received)} accepted`} />
        <StatTile
          label="Rejected (24h)"
          value={compact(s.totals.rejected)}
          icon={ShieldAlertIcon}
          sub={total ? `${((s.totals.rejected / total) * 100).toFixed(1)}% of deliveries` : 'No deliveries yet'}
        />
        <StatTile
          label="Forwarded (24h)"
          value={compact(s.forwarded.succeeded)}
          icon={SendIcon}
          sub={
            s.forwarded.failed || s.forwarded.in_progress
              ? `${s.forwarded.failed} failed · ${s.forwarded.in_progress} in progress`
              : failing
                ? `${failing} webhook${failing > 1 ? 's' : ''} with signature failures`
                : 'No failed deliveries'
          }
        />
        <Link to="/incidents" className="rounded-xl transition hover:ring-2 hover:ring-brand/30">
          <StatTile
            label="Open incidents"
            value={s.contracts.open_incidents}
            icon={ShieldAlertIcon}
            sub={
              s.contracts.breaking_24h || s.contracts.suspicious_24h || s.contracts.repaired_24h
                ? `${s.contracts.breaking_24h} breaking · ${s.contracts.suspicious_24h} warnings${s.contracts.repaired_24h ? ` · ${s.contracts.repaired_24h} repaired` : ''} (24h)`
                : lastAt
                  ? `Last delivery ${timeAgo(lastAt)}`
                  : 'Contracts learn from your first events'
            }
          />
        </Link>
      </div>

      <div className="grid gap-6 xl:grid-cols-5">
        <Card className="xl:col-span-3">
          <CardHeader>
            <CardTitle>Deliveries per hour</CardTitle>
            <CardDescription>Last 24 hours, your local time</CardDescription>
          </CardHeader>
          <CardContent>
            <TrafficChart hours={s.hours} />
          </CardContent>
        </Card>

        <Card className="xl:col-span-2">
          <CardHeader>
            <CardTitle>Webhook health</CardTitle>
            <CardDescription>Based on the last 24 hours</CardDescription>
          </CardHeader>
          <CardContent>
            {hooks.length === 0 ? (
              <p className="text-sm text-muted-foreground">No webhooks yet.</p>
            ) : (
              <ul className="-mx-2 divide-y">
                {hooks.map((w) => (
                  <HealthRow key={w.id} w={w} h={s.webhooks.find((x) => x.webhook_id === w.id)} />
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  )
}

type Health = { label: string; icon: typeof CircleIcon; tone: string }

function healthOf(h?: WebhookHealth): Health {
  if (!h?.last_received_at) return { label: 'No deliveries yet', icon: CircleDashedIcon, tone: 'text-muted-foreground' }
  if (h.rejected > 0) return { label: 'Signature failures', icon: ShieldAlertIcon, tone: 'text-[#d03b3b]' }
  if (h.received > 0) return { label: 'Healthy', icon: CheckCircle2Icon, tone: 'text-[#0ca30c]' }
  return { label: 'Quiet for 24h', icon: CircleIcon, tone: 'text-muted-foreground' }
}

function HealthRow({ w, h }: { w: Webhook; h?: WebhookHealth }) {
  const health = healthOf(h)
  const prefetch = usePrefetchWebhook()
  const Icon = health.icon
  return (
    <li>
      <Link to={`/webhooks/${w.id}`} onMouseEnter={() => prefetch(w.id)} onFocus={() => prefetch(w.id)} className="flex items-center gap-3 rounded-md px-2 py-2.5 hover:bg-muted/60">
        <Icon className={cn('size-4 shrink-0', health.tone)} aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{w.name}</div>
          <div className="truncate text-xs text-muted-foreground">
            {providerLabel(w.provider)} · {health.label}
            {h?.last_received_at && ` · last ${timeAgo(h.last_received_at)}`}
          </div>
        </div>
        <div className="text-right text-xs tabular-nums">
          <div>{compact(h?.received ?? 0)} ok</div>
          {!!h?.rejected && <div className="text-muted-foreground">{compact(h.rejected)} rejected</div>}
        </div>
      </Link>
    </li>
  )
}

function GettingStarted({ hasProject, hasWebhook }: { hasProject: boolean; hasWebhook: boolean }) {
  const steps = [
    { done: hasProject, title: 'Create a project', body: 'Group webhooks by product or environment.', to: '/projects', cta: 'Projects' },
    { done: hasWebhook, title: 'Create a webhook URL', body: 'Pick the provider and paste its signing secret.', to: '/webhooks', cta: 'Webhooks' },
    { done: false, title: 'Receive your first delivery', body: 'Paste the URL into the provider, or send a test with curl.', to: '/webhooks', cta: 'Get the URL' },
  ]
  const next = steps.findIndex((s) => !s.done)
  return (
    <Card className="mb-6 border-brand/40 bg-brand/5">
      <CardHeader>
        <CardTitle>Get your first webhook flowing</CardTitle>
        <CardDescription>Three steps, about five minutes.</CardDescription>
      </CardHeader>
      <CardContent>
        <ol className="grid gap-3 md:grid-cols-3">
          {steps.map((s, i) => (
            <li key={s.title} className={cn('rounded-lg border bg-background p-3', i === next && 'ring-2 ring-brand/60')}>
              <div className="flex items-center gap-2 text-sm font-medium">
                {s.done ? (
                  <CheckCircle2Icon className="size-4 text-[#0ca30c]" />
                ) : (
                  <span className="flex size-4 items-center justify-center rounded-full border text-[10px]">{i + 1}</span>
                )}
                {s.title}
              </div>
              <p className="mt-1 text-xs text-muted-foreground">{s.body}</p>
              {i === next && (
                <Button size="sm" className="mt-3" render={<Link to={s.to} />}>
                  {s.cta} <ArrowRightIcon />
                </Button>
              )}
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  )
}

function OverviewSkeleton() {
  return (
    <>
      <Skeleton className="mb-6 h-10 w-64" />
      <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <Skeleton key={i} className="h-24" />
        ))}
      </div>
      <Skeleton className="h-80 w-full" />
    </>
  )
}
