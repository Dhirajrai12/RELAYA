import { CheckIcon, CopyIcon } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'

import { Footer, Header } from '@/pages/landing/landing'
import { useScroll } from '@/pages/landing/use-scroll'
import { cn } from '@/lib/utils'

/** Public pages (docs, status, security) share the landing page's header, footer and colors. */
export function PublicPage({ eyebrow, title, intro, children, wide }: { eyebrow: string; title: string; intro: ReactNode; children: ReactNode; wide?: boolean }) {
  const { scrolled } = useScroll()
  useEffect(() => {
    document.title = `${title} · Relaya`
    return () => {
      document.title = 'Relaya'
    }
  }, [title])
  return (
    <div className="min-h-svh overflow-x-clip bg-l-bg text-l-muted antialiased transition-colors duration-300">
      <Header scrolled={scrolled} />
      <main className="px-4 pb-24 pt-10 sm:px-6 md:pt-16">
        <div className={cn('mx-auto', wide ? 'max-w-6xl' : 'max-w-4xl')}>
          <div className="mb-10 max-w-2xl md:mb-14">
            <div className="mb-3 text-xs font-semibold uppercase tracking-[0.2em] text-l-accent">{eyebrow}</div>
            <h1 className="text-balance text-3xl font-semibold tracking-tight text-l-text md:text-4xl">{title}</h1>
            <p className="mt-4 text-pretty text-base md:text-lg">{intro}</p>
          </div>
          {children}
        </div>
      </main>
      <Footer />
    </div>
  )
}

/** A dark code block with a copy button, like a screenshot in both themes. */
export function Code({ children, title }: { children: string; title?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="my-4 min-w-0 overflow-hidden rounded-xl border border-white/10 bg-[#0b1520] text-[13px] shadow-sm">
      <div className="flex items-center justify-between gap-2 border-b border-white/10 px-4 py-2 text-xs text-slate-400">
        <span className="truncate">{title ?? ''}</span>
        <button
          type="button"
          className="inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 transition hover:bg-white/10 hover:text-white"
          onClick={async () => {
            await navigator.clipboard.writeText(children)
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          }}
          aria-label="Copy code"
        >
          {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre className="overflow-x-auto p-4 font-mono leading-relaxed text-slate-200">{children}</pre>
    </div>
  )
}

/** Inline code. */
export const C = ({ children }: { children: ReactNode }) => (
  <code className="rounded bg-l-soft px-1.5 py-0.5 font-mono text-[0.85em] text-l-text [overflow-wrap:anywhere]">{children}</code>
)

/** A simple responsive table: scrolls sideways on small screens instead of squeezing. */
export function Table({ head, rows }: { head: string[]; rows: ReactNode[][] }) {
  return (
    <div className="my-4 overflow-x-auto rounded-xl border border-l-border">
      <table className="w-full min-w-[32rem] text-left text-sm">
        <thead className="bg-l-soft text-l-text">
          <tr>
            {head.map((h) => (
              <th key={h} className="px-4 py-2.5 font-medium">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-l-border">
          {rows.map((r, i) => (
            <tr key={i} className="align-top">
              {r.map((c, j) => (
                <td key={j} className="px-4 py-2.5">
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
