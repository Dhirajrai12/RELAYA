import { FlaskConicalIcon, RefreshCwIcon, SendIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { SimpleSelect } from '@/components/simple-select'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { errorMessage } from '@/lib/api'
import { providerLabel } from '@/lib/format'
import { useSimulateEvent, useSimulationSamples } from '@/lib/queries'
import { cn } from '@/lib/utils'
import type { SimulateResult, Webhook } from '@/lib/types'

/**
 * Sends a sample event, signed with this webhook's own secret exactly as the
 * provider signs it, through the whole pipeline: signature check, duplicates,
 * destinations. Simulated events are labelled and never teach contracts.
 */
export function SimulatorCard({ w }: { w: Webhook }) {
  const samples = useSimulationSamples(w.id)
  const send = useSimulateEvent(w.id)
  const list = samples.data?.data ?? []
  const [type, setType] = useState('')
  const [payload, setPayload] = useState('')
  const [result, setResult] = useState<SimulateResult | null>(null)
  const [error, setError] = useState('')

  // Load the chosen sample into the editor (and fresh IDs after "New IDs").
  useEffect(() => {
    const s = list.find((x) => x.type === type) ?? list[0]
    if (s) {
      setType(s.type)
      setPayload(s.payload)
    }
  }, [samples.data]) // eslint-disable-line react-hooks/exhaustive-deps

  function pick(t: string) {
    setType(t)
    setPayload(list.find((x) => x.type === t)?.payload ?? '')
    setResult(null)
    setError('')
  }

  async function onSend() {
    setError('')
    setResult(null)
    try {
      setResult(await send.mutateAsync({ event_type: type, payload }))
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const description = list.find((x) => x.type === type)?.description
  return (
    <Card className="lg:col-span-2">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <FlaskConicalIcon className="size-4 text-brand" /> Send a test event
        </CardTitle>
        <CardDescription>
          A sample {providerLabel(w.provider)} event,{' '}
          {samples.data?.signed === false ? 'unsigned (this webhook has no signing secret)' : 'signed with this webhook’s secret the way ' + providerLabel(w.provider) + ' signs it'}
          . It goes through everything a real one does: signature check, duplicates and your destinations. It’s labelled “simulated” and never teaches
          contracts.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid min-w-0 gap-3">
        {samples.isPending ? (
          <Skeleton className="h-40 w-full" />
        ) : samples.error ? (
          <p className="text-sm text-destructive">{errorMessage(samples.error)}</p>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <SimpleSelect
                className="w-full sm:w-72"
                value={type}
                onChange={pick}
                options={list.map((s) => ({ value: s.type, label: s.type }))}
              />
              <Button variant="ghost" size="sm" onClick={() => samples.refetch()} disabled={samples.isFetching} title="Reload the sample with new IDs">
                <RefreshCwIcon className={cn(samples.isFetching && 'animate-spin')} /> New IDs
              </Button>
              {description && <span className="min-w-0 text-sm text-muted-foreground">{description}</span>}
            </div>
            <textarea
              aria-label="Payload"
              value={payload}
              onChange={(e) => setPayload(e.target.value)}
              spellCheck={false}
              rows={12}
              className="min-w-0 rounded-md border bg-transparent px-3 py-2 font-mono text-xs leading-relaxed outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
            />
            <div className="flex flex-wrap items-center gap-3">
              <Button onClick={onSend} disabled={send.isPending || !payload.trim()}>
                <SendIcon /> {send.isPending ? 'Sending…' : 'Send test event'}
              </Button>
              <span className="text-xs text-muted-foreground">Edit anything first, e.g. a wrong amount, to see how your app reacts.</span>
            </div>
            {error && <p className="break-words text-sm text-destructive">{error}</p>}
            {result && <SimResult r={result} />}
          </>
        )}
      </CardContent>
    </Card>
  )
}

function SimResult({ r }: { r: SimulateResult }) {
  const ok = r.status === 'received'
  let text: string
  if (!ok) text = `Rejected: signature ${r.signature}. A real event like this would get HTTP 401.`
  else if (r.duplicate) text = 'Duplicate: an event with the same ID was already received, so nothing new was sent.'
  else
    text = `Accepted (${r.signature === 'valid' ? 'signature valid' : 'no signature check'}) as ${r.event_type || 'an event without a type'}, ${
      r.deliveries === 0 ? 'but no destination takes it yet' : `forwarded to ${r.deliveries} destination${r.deliveries === 1 ? '' : 's'}`
    }.`
  return (
    <div
      className={cn(
        'flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-sm',
        ok && !r.duplicate ? 'border-emerald-500/30 bg-emerald-500/5 text-emerald-800 dark:text-emerald-300' : 'border-amber-500/30 bg-amber-500/5 text-amber-800 dark:text-amber-300',
      )}
    >
      <span className="min-w-0 break-words">{text}</span>
      <Link to={`/events?event=${r.id}`} className="shrink-0 font-medium text-brand hover:underline">
        Open event
      </Link>
    </div>
  )
}
