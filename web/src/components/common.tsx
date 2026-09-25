import { CheckIcon, CopyIcon, InboxIcon, type LucideIcon } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type { EventStatus, SignatureResult } from '@/lib/types'

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight md:text-2xl">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

export function EmptyState({
  title,
  children,
  action,
  icon: Icon = InboxIcon,
}: {
  title: string
  children?: ReactNode
  action?: ReactNode
  icon?: LucideIcon
}) {
  return (
    <div className="flex flex-col items-center justify-center rounded-xl border border-dashed px-6 py-14 text-center">
      <div className="mb-3 flex size-11 items-center justify-center rounded-full bg-brand/10 text-brand">
        <Icon className="size-5" />
      </div>
      <p className="font-medium">{title}</p>
      {children && <div className="mt-1 max-w-md text-sm text-muted-foreground">{children}</div>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  )
}

/** Stat tile: label, value (auto-compacted by the caller), optional sub-line. */
export function StatTile({ label, value, sub, icon: Icon }: { label: string; value: ReactNode; sub?: ReactNode; icon?: LucideIcon }) {
  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="flex items-center justify-between gap-2 text-sm text-muted-foreground">
        {label}
        {Icon && <Icon className="size-4" />}
      </div>
      <div className="mt-2 text-xl font-semibold tracking-tight sm:text-2xl">{value}</div>
      {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
    </div>
  )
}

export function ErrorState({ error }: { error: unknown }) {
  return (
    <div className="rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3 text-sm text-destructive">
      {error instanceof Error ? error.message : 'Could not load data.'}
    </div>
  )
}

export function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={async () => {
        await navigator.clipboard.writeText(value)
        setCopied(true)
        toast.success('Copied to clipboard')
        setTimeout(() => setCopied(false), 1500)
      }}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
      {label}
    </Button>
  )
}

/** A monospace value with a copy button, e.g. a webhook URL or API key. */
export function CopyField({ value, className }: { value: string; className?: string }) {
  return (
    <div className={cn('flex items-center gap-2', className)}>
      <code className="min-w-0 flex-1 truncate rounded-md border bg-muted px-3 py-2 font-mono text-xs">{value}</code>
      <CopyButton value={value} />
    </div>
  )
}

const statusStyle: Record<EventStatus, string> = {
  received: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  rejected: 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400',
}

export function StatusBadge({ status }: { status: EventStatus }) {
  return (
    <Badge variant="outline" className={statusStyle[status]}>
      {status}
    </Badge>
  )
}

const sigLabel: Record<SignatureResult, string> = {
  valid: 'valid',
  invalid: 'invalid',
  missing: 'missing',
  not_configured: 'not verified',
}

const sigStyle: Record<SignatureResult, string> = {
  valid: 'text-emerald-700 dark:text-emerald-400',
  invalid: 'text-red-700 dark:text-red-400',
  missing: 'text-amber-700 dark:text-amber-400',
  not_configured: 'text-muted-foreground',
}

export function SignatureLabel({ value }: { value: SignatureResult }) {
  return <span className={cn('text-xs font-medium', sigStyle[value])}>{sigLabel[value]}</span>
}

