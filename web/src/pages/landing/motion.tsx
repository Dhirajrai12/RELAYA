import { useEffect, useRef, useState, type CSSProperties, type PointerEvent, type ReactNode } from 'react'

import { cn } from '@/lib/utils'

// Small motion primitives for the landing page. All of them are no-ops for
// visitors with "reduce motion" on (handled in index.css).

const reducedMotion = () => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches

/** Fades/slides its children in the first time they scroll into view. */
export function Reveal({ children, delay = 0, className }: { children: ReactNode; delay?: number; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const io = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          el.dataset.visible = 'true'
          io.disconnect()
        }
      },
      { rootMargin: '0px 0px -8% 0px', threshold: 0.12 },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [])
  return (
    <div ref={ref} className={cn('reveal', className)} style={{ '--reveal-delay': `${delay}ms` } as CSSProperties}>
      {children}
    </div>
  )
}

/** Card that lifts on hover, with a green glow following the pointer. */
export function SpotlightCard({ children, className }: { children: ReactNode; className?: string }) {
  function onMove(e: PointerEvent<HTMLDivElement>) {
    const r = e.currentTarget.getBoundingClientRect()
    e.currentTarget.style.setProperty('--mx', `${e.clientX - r.left}px`)
    e.currentTarget.style.setProperty('--my', `${e.clientY - r.top}px`)
  }
  return (
    <div
      onPointerMove={onMove}
      className={cn(
        'spotlight group relative h-full overflow-hidden rounded-2xl border border-l-border bg-l-surface transition duration-300 ease-out',
        'hover:-translate-y-1 hover:border-[#14b886]/45 hover:shadow-[0_18px_40px_-18px_rgb(20_184_134_/_0.45)] motion-reduce:hover:translate-y-0',
        className,
      )}
    >
      <div className="relative h-full">{children}</div>
    </div>
  )
}

/** Tilts toward the pointer (max ±6°) and settles back on leave. */
export function TiltCard({ children, className }: { children: ReactNode; className?: string }) {
  const [style, setStyle] = useState<CSSProperties>({})
  function onMove(e: PointerEvent<HTMLDivElement>) {
    if (reducedMotion() || e.pointerType !== 'mouse') return
    const r = e.currentTarget.getBoundingClientRect()
    const x = (e.clientX - r.left) / r.width - 0.5
    const y = (e.clientY - r.top) / r.height - 0.5
    setStyle({ transform: `perspective(1000px) rotateX(${(-y * 6).toFixed(2)}deg) rotateY(${(x * 6).toFixed(2)}deg)` })
  }
  return (
    <div
      onPointerMove={onMove}
      onPointerLeave={() => setStyle({ transform: 'perspective(1000px) rotateX(0) rotateY(0)' })}
      className={cn('transition-transform duration-300 ease-out will-change-transform', className)}
      style={style}
    >
      {children}
    </div>
  )
}

/** Thin accent bar across the top showing reading progress. */
export function ScrollProgress({ progress }: { progress: number }) {
  return (
    <div className="fixed inset-x-0 top-0 z-50 h-0.5" aria-hidden="true">
      <div
        className="h-full origin-left bg-gradient-to-r from-[#14b886] to-[#3ee0a8]"
        style={{ transform: `scaleX(${progress})` }}
      />
    </div>
  )
}
