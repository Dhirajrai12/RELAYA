import { FileCode2Icon, SirenIcon, TerminalIcon, type LucideIcon } from 'lucide-react'
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { IncidentCard } from './visuals'

// "Code in motion": code that types itself, then shows its output. Used in the
// hero (auto-cycling tabs) and the developer section (types once when visible).

// ---- tokens ---------------------------------------------------------------------------

import { deliver, events, type Kind, type Line } from './code-samples'

const tone: Record<Kind, string> = {
  p: 'text-slate-300', // plain
  k: 'text-[#7fb4ff]', // key / property
  s: 'text-[#3ee0a8]', // string
  c: 'text-slate-500', // comment
  n: 'text-[#f5b94a]', // number / boolean
  f: 'text-[#c4a7ff]', // function
  x: 'text-[#ff8fb1]', // keyword
}

const lineLength = (l: Line) => l.reduce((n, [t]) => n + t.length, 0)

const prefersReducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

// ---- typing -------------------------------------------------------------------------------

/**
 * Renders `lines`, revealing them character by character once `start` is true.
 * Calls onDone when fully typed. Reduced motion: shows everything at once.
 */
export function TypedCode({
  lines,
  start,
  cps = 70,
  onDone,
  output,
}: {
  lines: Line[]
  start: boolean
  cps?: number // characters per second
  onDone?: () => void
  output?: ReactNode // shown (animated in) after typing finishes
}) {
  const total = lines.reduce((n, l) => n + lineLength(l) + 1, 0)
  const [shown, setShown] = useState(() => (prefersReducedMotion() ? total : 0))
  const done = shown >= total
  const doneRef = useRef(onDone)
  useEffect(() => {
    doneRef.current = onDone
  })

  useEffect(() => {
    if (!start || shown >= total) return
    let frame = 0
    const t0 = performance.now() - (shown / cps) * 1000
    const tick = (now: number) => {
      const n = Math.min(total, Math.floor(((now - t0) / 1000) * cps))
      setShown(n)
      if (n < total) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- restart only when start flips
  }, [start, total, cps])

  useEffect(() => {
    if (done) doneRef.current?.()
  }, [done])

  // Walk the lines, spending the character budget (each newline costs one).
  // The caret sits on the first line that isn't fully typed yet.
  let budget = shown
  let caretPlaced = false
  const rendered = lines.map((line, li) => {
    if (budget < 0) return null // not reached yet
    const len = lineLength(line)
    const parts: ReactNode[] = []
    let left = budget
    for (let ti = 0; ti < line.length && left > 0; ti++) {
      const [text, kind = 'p'] = line[ti]
      parts.push(
        <span key={ti} className={tone[kind]}>
          {text.slice(0, left)}
        </span>,
      )
      left -= text.length
    }
    const caret = !caretPlaced && budget <= len
    if (caret) caretPlaced = true
    budget -= len + 1
    return (
      <div key={li} className="min-h-[1.6em] whitespace-pre">
        {parts}
        {caret && !done && <span className="l-caret -mb-0.5 ml-px inline-block h-[1.05em] w-[7px] translate-y-[2px] bg-[#3ee0a8]" />}
      </div>
    )
  })

  return (
    <div className="font-mono text-[12.5px] leading-[1.6] sm:text-[13px]">
      <div className="overflow-x-auto">{rendered}</div>
      {done && output && (
        <div className="mt-3 border-t border-white/10 pt-3 motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1">
          {output}
        </div>
      )}
    </div>
  )
}

/** A line of program output that fades in after `delay` ms. */
export function OutLine({ children, delay = 0, className }: { children: ReactNode; delay?: number; className?: string }) {
  return (
    <div
      className={cn('whitespace-pre font-mono text-[12.5px] leading-[1.6] motion-safe:animate-in motion-safe:fade-in motion-safe:fill-mode-both sm:text-[13px]', className)}
      style={{ animationDelay: `${delay}ms` }}
    >
      {children}
    </div>
  )
}

// ---- hero demo ------------------------------------------------------------------------------

type Tab = { id: string; label: string; icon: LucideIcon; hold: number }
const tabs: Tab[] = [
  { id: 'deliver', label: 'deliver.sh', icon: TerminalIcon, hold: 3800 },
  { id: 'events', label: 'events.ts', icon: FileCode2Icon, hold: 4200 },
  { id: 'incident', label: 'incident', icon: SirenIcon, hold: 6500 },
]

/** Hero visual: tabs that type themselves and auto-advance until the visitor clicks one. */
export function CodeDemo() {
  const [active, setActive] = useState(0)
  const [autoplay, setAutoplay] = useState(() => !prefersReducedMotion())
  const [holding, setHolding] = useState(false) // typing finished, counting down to next tab
  const [visible, setVisible] = useState(true)
  const ref = useRef<HTMLDivElement>(null)

  // Only animate while on screen.
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const io = new IntersectionObserver(([e]) => setVisible(e.isIntersecting), { threshold: 0.2 })
    io.observe(el)
    return () => io.disconnect()
  }, [])

  // Incident tab has no typing: start its countdown once shown.
  const tab = tabs[active]
  const typed = tab.id !== 'incident'

  useEffect(() => {
    if (!holding || !autoplay || !visible) return
    const t = setTimeout(() => {
      setHolding(false)
      setActive((a) => (a + 1) % tabs.length)
    }, tab.hold)
    return () => clearTimeout(t)
  }, [holding, autoplay, visible, tab.hold])

  function select(i: number) {
    setAutoplay(false)
    setHolding(false)
    setActive(i)
  }

  return (
    <div ref={ref} className="overflow-hidden rounded-2xl border border-white/10 bg-[#0d1722] shadow-2xl shadow-black/40">
      {/* title bar + tabs */}
      <div className="flex items-center border-b border-white/10 bg-[#0b1520]">
        <div className="hidden items-center gap-1.5 px-4 sm:flex" aria-hidden="true">
          <span className="size-2.5 rounded-full bg-[#ff6b6b]/70" />
          <span className="size-2.5 rounded-full bg-[#f5b94a]/70" />
          <span className="size-2.5 rounded-full bg-[#3ee0a8]/70" />
        </div>
        <div role="tablist" aria-label="Examples" className="flex min-w-0 flex-1 overflow-x-auto">
          {tabs.map((t, i) => (
            <button
              key={t.id}
              role="tab"
              type="button"
              aria-selected={i === active}
              onClick={() => select(i)}
              className={cn(
                'relative flex shrink-0 items-center gap-1.5 border-r border-white/5 px-3.5 py-2.5 font-mono text-xs transition-colors',
                i === active ? 'bg-[#0d1722] text-white' : 'text-slate-500 hover:text-slate-300',
              )}
            >
              <t.icon className={cn('size-3.5', i === active ? 'text-[#3ee0a8]' : '')} />
              {t.label}
              {i === active && (
                <span className="absolute inset-x-0 bottom-0 h-0.5 bg-white/5">
                  {autoplay && holding && visible && (
                    <span key={`${active}-${holding}`} className="l-progress block h-full bg-[#14b886]" style={{ '--l-progress-ms': `${t.hold}ms` } as CSSProperties} />
                  )}
                  {(!autoplay || !holding) && <span className="block h-full bg-[#14b886]/60" />}
                </span>
              )}
            </button>
          ))}
        </div>
        {!autoplay && (
          <button
            type="button"
            onClick={() => {
              setAutoplay(true)
              setHolding(true)
            }}
            className="shrink-0 px-3 text-[11px] text-slate-500 transition hover:text-[#3ee0a8]"
          >
            ▶ autoplay
          </button>
        )}
      </div>

      {/* body: fixed height so tabs don't make the page jump */}
      <div className="h-[340px] p-4 sm:h-[330px] sm:p-5" role="tabpanel">
        {typed ? (
          <TypedCode
            key={active}
            lines={tab.id === 'deliver' ? deliver : events}
            start={visible}
            onDone={() => setHolding(true)}
            output={tab.id === 'deliver' ? <DeliverOutput /> : <EventsOutput />}
          />
        ) : (
          <IncidentTab key={active} onShown={() => setHolding(true)} />
        )}
      </div>
    </div>
  )
}

function DeliverOutput() {
  return (
    <>
      <OutLine delay={0}>
        <span className="text-[#3ee0a8]">HTTP/1.1 200 OK</span>
        <span className="text-slate-500"> · 11 ms</span>
      </OutLine>
      <OutLine delay={250} className="text-slate-300">
        {'{ '}
        <span className="text-[#7fb4ff]">"id"</span>: <span className="text-[#3ee0a8]">"c55817d8-…"</span>, <span className="text-[#7fb4ff]">"duplicate"</span>:{' '}
        <span className="text-[#f5b94a]">false</span>
        {' }'}
      </OutLine>
      <OutLine delay={550} className="text-slate-400">
        <span className="text-[#3ee0a8]">✓</span> signature valid <span className="text-slate-600">·</span> stored{' '}
        <span className="text-slate-600">·</span> deduplicated
      </OutLine>
    </>
  )
}

function EventsOutput() {
  const rows = [
    ['payment.captured', 'invalid', '09:41:07'],
    ['refund.processed', 'missing', '09:12:55'],
  ]
  return (
    <>
      <OutLine className="text-slate-500">┌──────────────────┬───────────┬──────────┐</OutLine>
      {rows.map(([type, sig, at], i) => (
        <OutLine key={type} delay={200 + i * 220} className="text-slate-300">
          <span className="text-slate-500">│</span> {type.padEnd(16)} <span className="text-slate-500">│</span>{' '}
          <span className="text-[#ff8a8a]">{sig.padEnd(9)}</span> <span className="text-slate-500">│</span> {at} <span className="text-slate-500">│</span>
        </OutLine>
      ))}
      <OutLine delay={650} className="text-slate-500">└──────────────────┴───────────┴──────────┘</OutLine>
    </>
  )
}

function IncidentTab({ onShown }: { onShown: () => void }) {
  const cb = useRef(onShown)
  useEffect(() => {
    cb.current = onShown
  })
  useEffect(() => {
    const t = setTimeout(() => cb.current(), 1200) // after its steps animate in
    return () => clearTimeout(t)
  }, [])
  return <IncidentCard bare />

}

// ---- developer section ------------------------------------------------------------------

/** A code window whose contents type out the first time it scrolls into view. */
export function TypedWindow({ title, lines, output }: { title: string; lines: Line[]; output?: ReactNode }) {
  const [start, setStart] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const io = new IntersectionObserver(
      ([e]) => {
        if (e.isIntersecting) {
          setStart(true)
          io.disconnect()
        }
      },
      { threshold: 0.35 },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [])
  return (
    <div ref={ref} className="overflow-hidden rounded-xl border border-white/10 bg-[#0b1520] shadow-xl shadow-black/30">
      <div className="flex items-center gap-2 border-b border-white/10 px-4 py-2.5">
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="size-2.5 rounded-full bg-white/15" />
        <span className="ml-2 font-mono text-xs text-slate-500">{title}</span>
      </div>
      <div className="min-h-[15rem] p-4">
        <TypedCode lines={lines} start={start} cps={90} output={output} />
      </div>
    </div>
  )
}

