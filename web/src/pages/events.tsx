import { PauseIcon, PlayIcon, SearchIcon, SlidersHorizontalIcon, XIcon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { CopyButton, EmptyState, ErrorState, PageHeader, SignatureLabel, StatusBadge } from '@/components/common'
import { SimpleSelect } from '@/components/simple-select'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { bytes, dateTime, timeAgo } from '@/lib/format'
import { useEvent, useEvents, useWebhooks, type EventFilters } from '@/lib/queries'
import type { EventDetail, EventSummary } from '@/lib/types'
import { cn } from '@/lib/utils'


const FILTER_KEYS = ['webhook_id', 'status', 'signature', 'type', 'dedup_key'] as const

export function EventsPage() {
  const [params, setParams] = useSearchParams()
  const [live, setLive] = useState(true)
  const [search, setSearch] = useState(params.get('dedup_key') ?? '')
  const selected = params.get('event')

  const filters: EventFilters = useMemo(
    () => Object.fromEntries(FILTER_KEYS.map((k) => [k, params.get(k) ?? ''])),
    [params],
  )
  const setFilter = (key: string, value: string) =>
    setParams((p) => {
      if (value) p.set(key, value)
      else p.delete(key)
      return p
    })
  const activeCount = FILTER_KEYS.filter((k) => params.get(k)).length
  const hasFilters = activeCount > 0
  const [showFilters, setShowFilters] = useState(hasFilters)

  const webhooks = useWebhooks()
  const events = useEvents(filters, live)
  const rows = events.data?.pages.flatMap((p) => p.data) ?? []
  const webhookName = (id: string) => webhooks.data?.data.find((w) => w.id === id)?.name ?? '—'

  return (
    <>
      <PageHeader
        title="Events"
        description="Every webhook delivery received by the gateway, newest first."
        actions={
          <Button variant="outline" size="sm" onClick={() => setLive(!live)}>
            {live ? <PauseIcon /> : <PlayIcon />}
            {live ? 'Live' : 'Paused'}
            {live && <span className="size-2 animate-pulse rounded-full bg-emerald-500" />}
          </Button>
        }
      />

      <div className="mb-3 flex items-center gap-2 sm:hidden">
        <Button variant="outline" size="sm" onClick={() => setShowFilters(!showFilters)}>
          <SlidersHorizontalIcon />
          Filters{activeCount ? ` (${activeCount})` : ''}
        </Button>
      </div>

      <div className={cn('mb-4 grid-cols-2 gap-2 sm:flex sm:flex-wrap sm:items-center', showFilters ? 'grid' : 'hidden')}>
        <SimpleSelect
          className="col-span-2 w-full sm:w-52"
          value={filters.webhook_id ?? ''}
          onChange={(v) => setFilter('webhook_id', v)}
          options={[{ value: '', label: 'All webhooks' }, ...(webhooks.data?.data ?? []).map((w) => ({ value: w.id, label: w.name }))]}
        />
        <SimpleSelect
          className="w-full sm:w-40"
          value={filters.status ?? ''}
          onChange={(v) => setFilter('status', v)}
          options={[
            { value: '', label: 'Any status' },
            { value: 'received', label: 'Received' },
            { value: 'rejected', label: 'Rejected' },
          ]}
        />
        <SimpleSelect
          className="w-full sm:w-44"
          value={filters.signature ?? ''}
          onChange={(v) => setFilter('signature', v)}
          options={[
            { value: '', label: 'Any signature' },
            { value: 'valid', label: 'Valid' },
            { value: 'invalid', label: 'Invalid' },
            { value: 'missing', label: 'Missing' },
            { value: 'not_configured', label: 'Not verified' },
          ]}
        />
        <Input
          key={filters.type /* remount so "Clear" empties it */}
          className="col-span-2 w-full sm:w-52"
          placeholder="Event type, e.g. payment.captured"
          defaultValue={filters.type}
          onKeyDown={(e) => e.key === 'Enter' && setFilter('type', e.currentTarget.value.trim())}
          onBlur={(e) => setFilter('type', e.currentTarget.value.trim())}
        />
        <form
          className="relative col-span-2"
          onSubmit={(e) => {
            e.preventDefault()
            setFilter('dedup_key', search.trim())
          }}
        >
          <SearchIcon className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="w-full pl-8 sm:w-56" placeholder="Provider event ID" value={search} onChange={(e) => setSearch(e.target.value)} />
        </form>
        {hasFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setSearch('')
              setParams({})
            }}
          >
            <XIcon />
            Clear
          </Button>
        )}
      </div>

      {events.error ? (
        <ErrorState error={events.error} />
      ) : events.isPending ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          title={hasFilters ? 'No events match these filters' : 'No events yet'}
          action={
            !hasFilters && (
              <Button variant="outline" render={<Link to="/webhooks" />}>
                Create a webhook URL
              </Button>
            )
          }
        >
          {!hasFilters && 'Point a provider at one of your webhook URLs; deliveries show up here within seconds.'}
        </EmptyState>
      ) : (
        <>
          <ul className="divide-y rounded-lg border md:hidden">
            {rows.map((e) => (
              <EventCard key={e.id} e={e} webhookName={webhookName} onOpen={() => setFilter('event', e.id)} />
            ))}
          </ul>
          <div className="hidden rounded-lg border md:block">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Type</TableHead>
                  <TableHead>Webhook</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Signature</TableHead>
                  <TableHead>Event ID</TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead className="text-right">Received</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((e) => (
                  <TableRow
                    key={e.id}
                    className="cursor-pointer"
                    data-state={selected === e.id ? 'selected' : undefined}
                    onClick={() => setFilter('event', e.id)}
                  >
                    <TableCell className="font-mono text-xs">{e.type || <span className="text-muted-foreground">—</span>}</TableCell>
                    <TableCell className="max-w-40 truncate">{webhookName(e.webhook_id)}</TableCell>
                    <TableCell>
                      <StatusBadge status={e.status} />
                    </TableCell>
                    <TableCell>
                      <SignatureLabel value={e.signature} />
                    </TableCell>
                    <TableCell className="max-w-48 truncate font-mono text-xs text-muted-foreground">{e.dedup_key}</TableCell>
                    <TableCell className="text-right text-xs text-muted-foreground">{bytes(e.payload_size)}</TableCell>
                    <TableCell className="text-right text-xs text-muted-foreground" title={dateTime(e.received_at)}>
                      {timeAgo(e.received_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {events.hasNextPage && (
            <div className="mt-4 flex justify-center">
              <Button variant="outline" onClick={() => events.fetchNextPage()} disabled={events.isFetchingNextPage}>
                {events.isFetchingNextPage ? 'Loading…' : 'Load older events'}
              </Button>
            </div>
          )}
        </>
      )}

      <EventSheet id={selected} webhookName={webhookName} onClose={() => setFilter('event', '')} />
    </>
  )
}

function EventCard({ e, webhookName, onOpen }: { e: EventSummary; webhookName: (id: string) => string; onOpen: () => void }) {
  return (
    <li>
      <button type="button" onClick={onOpen} className="w-full px-3 py-3 text-left active:bg-muted">
        <div className="flex items-center justify-between gap-3">
          <span className="min-w-0 truncate font-mono text-xs font-medium">{e.type || '—'}</span>
          <span className="shrink-0 text-xs text-muted-foreground">{timeAgo(e.received_at)}</span>
        </div>
        <div className="mt-1.5 flex items-center gap-2 text-xs">
          <StatusBadge status={e.status} />
          <SignatureLabel value={e.signature} />
          <span className="min-w-0 truncate text-muted-foreground">· {webhookName(e.webhook_id)}</span>
        </div>
      </button>
    </li>
  )
}

function EventSheet({ id, webhookName, onClose }: { id: string | null; webhookName: (id: string) => string; onClose: () => void }) {
  const { data, error, isPending } = useEvent(id)
  return (
    <Sheet open={!!id} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full overflow-y-auto data-[side=right]:w-full sm:max-w-2xl data-[side=right]:sm:w-3/4 data-[side=right]:sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle className="font-mono">{data?.type || 'Event'}</SheetTitle>
          <SheetDescription>{data ? `${webhookName(data.webhook_id)} · ${dateTime(data.received_at)}` : ' '}</SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-6">
          {error ? <ErrorState error={error} /> : isPending || !data ? <Skeleton className="h-64 w-full" /> : <EventBody e={data} />}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function EventBody({ e }: { e: EventDetail }) {
  const payload =
    e.payload_json !== undefined
      ? JSON.stringify(e.payload_json, null, 2)
      : e.payload_text ?? (e.payload_base64 ? `base64: ${e.payload_base64}` : '')

  return (
    <div className="space-y-6">
      {e.status === 'rejected' && (
        <div className="rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 text-sm text-red-700 dark:text-red-400">
          Rejected: signature {e.signature}. The sender got HTTP 401. Check that the signing secret on this webhook matches
          the one configured at the provider.
        </div>
      )}

      <dl className="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2 text-sm">
        <dt className="text-muted-foreground">Status</dt>
        <dd>
          <StatusBadge status={e.status} />
        </dd>
        <dt className="text-muted-foreground">Signature</dt>
        <dd>
          <SignatureLabel value={e.signature} />
        </dd>
        <dt className="text-muted-foreground">Event ID</dt>
        <dd className="break-all font-mono text-xs">{e.dedup_key}</dd>
        <dt className="text-muted-foreground">Platform ID</dt>
        <dd className="break-all font-mono text-xs">{e.id}</dd>
        <dt className="text-muted-foreground">Source IP</dt>
        <dd className="font-mono text-xs">{e.source_ip ?? '—'}</dd>
        <dt className="text-muted-foreground">Size</dt>
        <dd>
          {bytes(e.payload_size)} · {e.content_type || 'no content type'}
        </dd>
      </dl>

      <section>
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-medium">Payload</h3>
          {payload && <CopyButton value={payload} />}
        </div>
        <pre className="max-h-[28rem] overflow-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed">
          {payload || '(empty body)'}
        </pre>
        <p className="mt-1 text-xs text-muted-foreground">Sensitive fields (card numbers, tokens, passwords…) are masked.</p>
      </section>

      <section>
        <h3 className="mb-2 text-sm font-medium">Headers</h3>
        <div className="overflow-hidden rounded-md border">
          <table className="w-full text-xs">
            <tbody>
              {Object.entries(e.headers)
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([k, v]) => (
                  <tr key={k} className="border-b last:border-0">
                    <td className="w-1/3 bg-muted/50 px-3 py-1.5 align-top font-mono">{k}</td>
                    <td className="break-all px-3 py-1.5 font-mono">{v}</td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  )
}
