import {
  CheckCircle2Icon,
  CircleDashedIcon,
  GraduationCapIcon,
  Loader2Icon,
  PlusCircleIcon,
  ShieldAlertIcon,
  TriangleAlertIcon,
  type LucideIcon,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { ContractState, EventContractStatus } from '@/lib/types'

// Contract status UI. Every state pairs an icon with a word; color is never the only signal.

export function ContractStateBadge({ state, version }: { state: ContractState; version?: number | null }) {
  if (state === 'active')
    return (
      <Badge variant="outline" className="border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400">
        active{version ? ` · v${version}` : ''}
      </Badge>
    )
  if (state === 'proposed')
    return (
      <Badge variant="outline" className="border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-400">
        ready to review
      </Badge>
    )
  return <Badge variant="outline">learning</Badge>
}

const eventStatus: Record<EventContractStatus, { label: string; icon: LucideIcon; cls: string } | null> = {
  none: null,
  pending: { label: 'checking', icon: Loader2Icon, cls: 'text-muted-foreground' },
  learning: { label: 'learning', icon: GraduationCapIcon, cls: 'text-muted-foreground' },
  ok: { label: 'matches', icon: CheckCircle2Icon, cls: 'text-emerald-700 dark:text-emerald-400' },
  compatible: { label: 'new fields', icon: PlusCircleIcon, cls: 'text-sky-700 dark:text-sky-400' },
  suspicious: { label: 'warning', icon: TriangleAlertIcon, cls: 'text-amber-700 dark:text-amber-400' },
  breaking: { label: 'breaking', icon: ShieldAlertIcon, cls: 'text-red-700 dark:text-red-400' },
}

/** An event's contract check result, compact. Renders a dash when not checked. */
export function EventContractStatusLabel({ status }: { status: EventContractStatus }) {
  const m = eventStatus[status]
  if (!m) return <span className="text-xs text-muted-foreground">—</span>
  const Icon = m.icon
  return (
    <span className={cn('inline-flex items-center gap-1 text-xs', m.cls)}>
      <Icon className={cn('size-3.5', status === 'pending' && 'motion-safe:animate-spin')} />
      {m.label}
    </span>
  )
}

export function SeverityBadge({ severity }: { severity: string }) {
  return severity === 'breaking' ? (
    <Badge variant="outline" className="gap-1 border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400">
      <ShieldAlertIcon className="size-3" /> breaking
    </Badge>
  ) : (
    <Badge variant="outline" className="gap-1 border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400">
      <TriangleAlertIcon className="size-3" /> warning
    </Badge>
  )
}

/** "integer → string" style diff for a finding. */
export function FindingDiff({ expected, actual }: { expected: string; actual: string }) {
  return (
    <span className="font-mono text-xs">
      {expected && <span className="text-muted-foreground">{expected}</span>}
      {expected && actual && <span className="px-1 text-muted-foreground">→</span>}
      {actual && <span className="font-medium">{actual}</span>}
    </span>
  )
}

export function NotCheckedIcon() {
  return <CircleDashedIcon className="size-3.5 text-muted-foreground" />
}
