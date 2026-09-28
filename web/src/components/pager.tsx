import { ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export const PAGE_SIZE = 20

/**
 * Splits a list into pages. `resetKey` sends it back to page 1 (e.g. when a
 * filter changes); if the list shrinks, the page stays within range.
 */
export function usePaged<T>(items: T[], pageSize = PAGE_SIZE, resetKey?: unknown) {
  const [page, setPage] = useState(0)
  const [key, setKey] = useState(resetKey)
  if (key !== resetKey) {
    // Reset during render (not in an effect) so the old page never flashes.
    setKey(resetKey)
    setPage(0)
  }
  const pageCount = Math.max(1, Math.ceil(items.length / pageSize))
  const current = Math.min(page, pageCount - 1)
  const start = current * pageSize
  return {
    page: current,
    setPage,
    pageCount,
    pageSize,
    total: items.length,
    items: items.slice(start, start + pageSize),
    from: items.length ? start + 1 : 0,
    to: Math.min(start + pageSize, items.length),
  }
}

type Paged = ReturnType<typeof usePaged>

/**
 * Previous / next with "21–40 of 134". For server-paged lists pass `hasMore`
 * and `loadMore`: the last loaded page's "Next" fetches the next batch first,
 * and the total shows as "134+" until everything is loaded.
 */
export function Pager({
  paged,
  hasMore = false,
  loadMore,
  noun = 'items',
  className,
}: {
  paged: Paged
  hasMore?: boolean
  loadMore?: () => Promise<unknown>
  noun?: string
  className?: string
}) {
  const [loading, setLoading] = useState(false)
  const { page, pageCount, setPage, from, to, total } = paged
  const lastLoaded = page >= pageCount - 1
  const canNext = !lastLoaded || (hasMore && !!loadMore)

  async function next() {
    if (!lastLoaded) return setPage(page + 1)
    if (!hasMore || !loadMore) return
    // A partly filled last page is completed by the next batch first, so no
    // rows are skipped when batches aren't a multiple of the page size.
    const full = to - from + 1 >= paged.pageSize
    setLoading(true)
    try {
      await loadMore()
      setPage(full ? page + 1 : page)
    } finally {
      setLoading(false)
    }
  }

  if (total <= paged.pageSize && !hasMore) return null
  return (
    <div className={cn('flex flex-wrap items-center justify-between gap-2 border-t px-3 py-2 text-xs text-muted-foreground', className)}>
      <span>
        {from}–{to} of {total}
        {hasMore ? '+' : ''} {noun}
      </span>
      <div className="flex items-center gap-1">
        <Button size="sm" variant="ghost" disabled={page === 0} onClick={() => setPage(page - 1)} aria-label="Previous page">
          <ChevronLeftIcon /> <span className="hidden sm:inline">Previous</span>
        </Button>
        <span className="px-1 tabular-nums">
          {page + 1} / {pageCount}
          {hasMore ? '+' : ''}
        </span>
        <Button size="sm" variant="ghost" disabled={!canNext || loading} onClick={next} aria-label="Next page">
          <span className="hidden sm:inline">{loading ? 'Loading…' : 'Next'}</span> <ChevronRightIcon />
        </Button>
      </div>
    </div>
  )
}
