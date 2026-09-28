import { useState } from 'react'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

/**
 * Which event types an endpoint receives: all of them (empty list), or a
 * chosen few from the known types, plus any typed in.
 */
export function EventTypePicker({
  known,
  value,
  onChange,
}: {
  known: { name: string; description?: string }[]
  value: string[]
  onChange: (v: string[]) => void
}) {
  const [custom, setCustom] = useState('')
  const all = value.length === 0
  const names = [...new Set([...known.map((k) => k.name), ...value])].sort()
  const toggle = (n: string) => onChange(value.includes(n) ? value.filter((x) => x !== n) : [...value, n])

  return (
    <div className="grid min-w-0 gap-2">
      <label className="flex cursor-pointer items-center gap-2 text-sm">
        <input type="checkbox" className="size-4 accent-[var(--brand)]" checked={all} onChange={(e) => e.target.checked && onChange([])} />
        All event types
      </label>
      {names.length > 0 && (
        <div className={cn('grid max-h-48 min-w-0 gap-1 overflow-y-auto rounded-md border p-2', all && 'opacity-60')}>
          {names.map((n) => {
            const desc = known.find((k) => k.name === n)?.description
            return (
              <label key={n} className="flex min-w-0 cursor-pointer items-start gap-2 rounded px-1 py-0.5 hover:bg-muted/50">
                <input type="checkbox" className="mt-0.5 size-4 shrink-0 accent-[var(--brand)]" checked={value.includes(n)} onChange={() => toggle(n)} />
                <span className="min-w-0">
                  <span className="block truncate font-mono text-xs">{n}</span>
                  {desc && <span className="block text-xs text-muted-foreground">{desc}</span>}
                </span>
              </label>
            )
          })}
        </div>
      )}
      <div className="flex gap-2">
        <Input
          value={custom}
          onChange={(e) => setCustom(e.target.value)}
          placeholder="Another type, e.g. invoice.paid"
          className="font-mono text-xs"
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              const t = custom.trim()
              if (t && !value.includes(t)) onChange([...value, t])
              setCustom('')
            }
          }}
        />
      </div>
      <p className="text-xs text-muted-foreground">{all ? 'Receives every event type, including new ones.' : `Receives ${value.length} type${value.length === 1 ? '' : 's'} only.`}</p>
    </div>
  )
}
