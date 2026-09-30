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

  // Walk the lines, spending the character budget (each newline costs one). Text not
  // typed yet is laid out but invisible, so long lines wrap in their final place and the
  // window has its full height from the start: typing never moves the page.
  // The caret sits on the first line that isn't fully typed yet.
  let budget = shown
  let caretPlaced = false
  const rendered = lines.map((line, li) => {
    const len = lineLength(line)
    const parts: ReactNode[] = []
    let left = Math.max(budget, 0)
    for (let ti = 0; ti < line.length; ti++) {
      const [text, kind = 'p'] = line[ti]
      const typed = text.slice(0, left)
      const rest = text.slice(typed.length)
      if (typed) parts.push(<span key={`t${ti}`} className={tone[kind]}>{typed}</span>)
      if (rest) parts.push(<span key={`r${ti}`} className="invisible">{rest}</span>)
      left = Math.max(left - text.length, 0)
    }
    const caret = !caretPlaced && budget >= 0 && budget <= len
    if (caret) {
      caretPlaced = true
      // The caret goes right after the typed characters, before the invisible rest.
      const at = parts.findIndex((p) => (p as { key?: string }).key?.startsWith('r'))
      // Drawn out of flow from an empty inline marker: an inline-block caret would add a line
      // break opportunity wherever it is, so wrapped lines would reflow as it moves.
      const mark = !done && (
        <span key="caret" className="relative">
          <span className="l-caret absolute left-px top-[0.2em] h-[1.1em] w-[7px] bg-[#3ee0a8]" />
        </span>
      )
      if (at === -1) parts.push(mark)
      else parts.splice(at, 0, mark)
    }
    budget -= len + 1
    return (
      <div key={li} className="min-h-[1.6em] whitespace-pre-wrap [overflow-wrap:anywhere]">
        {parts}
      </div>
    )
  })

  // Long lines wrap inside the window instead of scrolling sideways. The output is laid
  // out from the start too (invisible), and remounted when typing ends so it animates in.
  return (
    <div className="min-w-0 font-mono text-[12.5px] leading-[1.6] sm:text-[13px]">
      {rendered}
      {output && (
        <div
          key={done ? 'shown' : 'hidden'}
          className={cn('mt-3 border-t border-white/10 pt-3', done ? 'motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-1' : 'invisible')}
          aria-hidden={!done}
        >
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
      className={cn('whitespace-pre-wrap font-mono text-[12.5px] leading-[1.6] [overflow-wrap:anywhere] motion-safe:animate-in motion-safe:fade-in motion-safe:fill-mode-both sm:text-[13px]', className)}
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

  // Incident tab has no typing: it starts its countdown once shown.
  const tab = tabs[active]

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
                'relative flex shrink-0 items-center gap-1.5 border-r border-white/5 px-2.5 py-2.5 font-mono text-xs transition-colors sm:px-3.5',
                i === active ? 'bg-[#0d1722] text-white' : 'text-slate-500 hover:text-slate-300',
              )}
            >
              {/* Icons from tablet width up, so all tabs fit a phone without scrolling. */}
              <t.icon className={cn('hidden size-3.5 sm:block', i === active ? 'text-[#3ee0a8]' : '')} />
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
            aria-label="Resume autoplay"
          >
            ▶<span className="hidden sm:inline"> autoplay</span>
          </button>
        )}
      </div>

      {/* body: every tab is laid out in the same grid cell (the inactive ones invisible), so
          the box is as tall as the tallest tab at any width and switching tabs never moves the page */}
      <div className="grid min-w-0 p-4 sm:p-5" role="tabpanel">
        {tabs.map((t, i) => {
          const isActive = i === active
          const body =
            t.id === 'incident' ? (
              isActive ? <IncidentTab onShown={() => setHolding(true)} /> : <IncidentCard bare />
            ) : (
              <TypedCode
                lines={t.id === 'deliver' ? deliver : events}
                start={isActive && visible}
                onDone={isActive ? () => setHolding(true) : undefined}
                output={t.id === 'deliver' ? <DeliverOutput /> : <EventsOutput />}
              />
            )
          return (
            // The active tab remounts each time it's shown, so it types again.
            <div key={isActive ? `active-${active}` : t.id} className={cn('min-w-0 [grid-area:1/1]', !isActive && 'invisible')} aria-hidden={!isActive}>
              {body}
            </div>
          )
        })}
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
  // A box-drawn table can't wrap, so phones get plain rows.
  return (
    <>
      <div className="hidden sm:block">
        <OutLine className="whitespace-pre text-slate-500">┌──────────────────┬───────────┬──────────┐</OutLine>
        {rows.map(([type, sig, at], i) => (
          <OutLine key={type} delay={200 + i * 220} className="whitespace-pre text-slate-300">
            <span className="text-slate-500">│</span> {type.padEnd(16)} <span className="text-slate-500">│</span>{' '}
            <span className="text-[#ff8a8a]">{sig.padEnd(9)}</span> <span className="text-slate-500">│</span> {at} <span className="text-slate-500">│</span>
          </OutLine>
        ))}
        <OutLine delay={650} className="whitespace-pre text-slate-500">└──────────────────┴───────────┴──────────┘</OutLine>
      </div>
      <div className="sm:hidden">
        {rows.map(([type, sig, at], i) => (
          <OutLine key={type} delay={200 + i * 220} className="text-slate-300">
            {type} <span className="text-[#ff8a8a]">{sig}</span> <span className="text-slate-500">{at}</span>
          </OutLine>
        ))}
      </div>
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

