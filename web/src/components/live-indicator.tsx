import { useRealtimeStatus } from '@/lib/realtime'
import { cn } from '@/lib/utils'

/** Shows whether the realtime stream is connected (icon-free: dot + word). */
export function LiveIndicator({ className }: { className?: string }) {
  const status = useRealtimeStatus((s) => s.status)
  const label = status === 'live' ? 'Live' : status === 'connecting' ? 'Connecting…' : 'Reconnecting…'
  return (
    <span
      className={cn('inline-flex items-center gap-1.5 text-xs text-muted-foreground', className)}
      title={status === 'live' ? 'Updates appear as they happen' : 'Live updates paused; refreshing periodically until reconnected'}
      role="status"
    >
      <span className="relative flex size-2">
        {status === 'live' && <span className="absolute inline-flex size-full rounded-full bg-emerald-500 opacity-60 motion-safe:animate-ping" />}
        <span className={cn('relative inline-flex size-2 rounded-full', status === 'live' ? 'bg-emerald-500' : 'bg-amber-500')} />
      </span>
      {label}
    </span>
  )
}
