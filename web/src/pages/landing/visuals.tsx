import { CheckCircle2Icon, CircleAlertIcon, RotateCcwIcon, ShieldCheckIcon, UsersIcon } from 'lucide-react'
import type { CSSProperties, ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * Hero illustration: an incident as Relaya shows it, walking through the four
 * questions. Pure HTML so it stays crisp, themeable and accessible.
 */
export function IncidentCard({ bare = false }: { bare?: boolean }) {
  const steps: { icon: typeof CircleAlertIcon; tone: string; title: string; body: ReactNode }[] = [
    {
      icon: CircleAlertIcon,
      tone: 'text-[#ff7a7a]',
      title: 'What broke?',
      body: (
        <>
          <Mono>payment.captured</Mono> → CRM returned <Mono>500</Mono> · retried 5×
        </>
      ),
    },
    {
      icon: UsersIcon,
      tone: 'text-[#f5b94a]',
      title: 'Who is affected?',
      body: '12 events since 09:41 · 1 destination (CRM)',
    },
    {
      icon: RotateCcwIcon,
      tone: 'text-[#7fb4ff]',
      title: 'Is it safe to fix?',
      body: 'Replay preview: 12 deliveries · same Idempotency-Key',
    },
    {
      icon: CheckCircle2Icon,
      tone: 'text-[#3ee0a8]',
      title: 'Did the fix work?',
      body: 'Replayed 12/12 · CRM answered 200 OK',
    },
  ]

  // bare: no glow, border or footer, for embedding inside another window.
  return (
    <div className="relative">
      {!bare && (
        <div className="absolute -inset-6 rounded-[2rem] bg-[radial-gradient(ellipse_at_top,rgba(20,184,134,0.25),transparent_65%)] blur-2xl" aria-hidden="true" />
      )}
      <div className={cn('relative overflow-hidden', !bare && 'rounded-2xl border border-white/10 bg-[#101a26] shadow-2xl shadow-black/40')}>
        <div className={cn("flex items-center justify-between border-b border-white/10", bare ? "pb-3" : "px-4 py-3")}>
          <div className="flex items-center gap-2 text-sm font-medium text-white">
            <span className="relative flex size-2">
              <span className="absolute inline-flex size-full rounded-full bg-[#14b886] opacity-60 motion-safe:animate-ping" />
              <span className="relative inline-flex size-2 rounded-full bg-[#14b886]" />
            </span>
            Incident #1042 · Razorpay → CRM
          </div>
          <span className="rounded-full bg-[#14b886]/15 px-2 py-0.5 text-xs font-medium text-[#3ee0a8]">Resolved</span>
        </div>
        <ol className={cn("space-y-1", bare ? "pt-2" : "p-3")}>
          {steps.map((s, i) => (
            <li
              key={s.title}
              className={cn("flex gap-3 rounded-xl motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2 motion-safe:fill-mode-both", bare ? "px-1 py-2" : "px-3 py-3")}
              style={{ animationDelay: `${150 + i * 220}ms`, animationDuration: '500ms' }}
            >
              <s.icon className={cn('mt-0.5 size-5 shrink-0', s.tone)} aria-hidden="true" />
              <div className="min-w-0">
                <div className="text-sm font-medium text-white">{s.title}</div>
                <div className="mt-0.5 text-sm text-slate-400">{s.body}</div>
              </div>
            </li>
          ))}
        </ol>
        {!bare && (
          <div className="flex items-center gap-2 border-t border-white/10 px-4 py-3 text-xs text-slate-400">
            <ShieldCheckIcon className="size-3.5 text-[#3ee0a8]" aria-hidden="true" />
            Full audit trail: who replayed what, when, and the result
          </div>
        )}
      </div>
    </div>
  )
}

function Mono({ children }: { children: ReactNode }) {
  return <code className="rounded bg-white/5 px-1 py-0.5 font-mono text-[0.8em] text-slate-200">{children}</code>
}

/** A dark code window with hand-colored tokens (no highlighter dependency). */
export function CodeWindow({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="overflow-hidden rounded-xl border border-white/10 bg-[#0b1520] shadow-xl shadow-black/30">
      <div className="flex items-center gap-2 border-b border-white/10 px-4 py-2.5">
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="ml-2 font-mono text-xs text-slate-500">{title}</span>
      </div>
      <pre className="overflow-x-auto p-4 font-mono text-[13px] leading-relaxed text-slate-300">{children}</pre>
    </div>
  )
}

// Token colors for CodeWindow content.
export const K = ({ children }: { children: ReactNode }) => <span className="text-[#7fb4ff]">{children}</span>
export const S = ({ children }: { children: ReactNode }) => <span className="text-[#3ee0a8]">{children}</span>
export const C = ({ children }: { children: ReactNode }) => <span className="text-slate-500">{children}</span>
export const N = ({ children }: { children: ReactNode }) => <span className="text-[#f5b94a]">{children}</span>

/** Mini hourly chart for "How it works" step 3; bars grow in when revealed. */
export function MiniChart() {
  const bars = [30, 42, 38, 55, 48, 62, 58, 20, 66, 72, 64, 80]
  return (
    <div className="rounded-xl border border-white/10 bg-[#0b1520] p-4">
      <div className="flex h-24 items-end gap-1.5" aria-hidden="true">
        {bars.map((h, i) => (
          <div key={i} className="grow-bar flex flex-1 flex-col justify-end gap-0.5" style={{ '--i': i } as CSSProperties}>
            {i === 7 && <div className="h-3 rounded-t-sm bg-[#d03b3b]" />}
            <div className={cn('bg-[#3987e5]', i !== 7 && 'rounded-t-sm')} style={{ height: h }} />
          </div>
        ))}
      </div>
      <div className="mt-3 flex items-center gap-2 text-xs text-slate-400">
        <span className="relative flex size-2">
          <span className="absolute inline-flex size-full rounded-full bg-[#ff8a8a] opacity-70 motion-safe:animate-ping" />
          <span className="relative inline-flex size-2 rounded-full bg-[#ff8a8a]" />
        </span>
        Razorpay production · signature failures
      </div>
    </div>
  )
}

/** Mini event list used in "How it works" step 2. */
export function MiniEvents() {
  const rows = [
    { type: 'payment.captured', ok: true, t: '2s' },
    { type: 'orders/create', ok: true, t: '9s' },
    { type: 'payment.captured', ok: false, t: '14s' },
    { type: 'refund.processed', ok: true, t: '31s' },
  ]
  return (
    <div className="divide-y divide-white/5 rounded-xl border border-white/10 bg-[#0b1520] text-sm">
      {rows.map((r, i) => (
        <div key={i} className="slide-row flex items-center gap-3 px-4 py-2.5" style={{ '--i': i } as CSSProperties}>
          <span className="min-w-0 flex-1 truncate font-mono text-xs text-slate-200">{r.type}</span>
          <span
            className={cn(
              'rounded-full px-2 py-0.5 text-[11px] font-medium',
              r.ok ? 'bg-[#14b886]/15 text-[#3ee0a8]' : 'bg-[#d03b3b]/20 text-[#ff8a8a]',
            )}
          >
            {r.ok ? 'verified' : 'bad signature'}
          </span>
          <span className="w-8 text-right text-xs text-slate-500">{r.t}</span>
        </div>
      ))}
    </div>
  )
}
