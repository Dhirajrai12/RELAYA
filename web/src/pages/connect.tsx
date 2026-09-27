import { useQuery } from '@tanstack/react-query'
import { CheckCircle2Icon, LockIcon, TriangleAlertIcon } from 'lucide-react'
import { useEffect, useState, type ReactNode, type SubmitEvent } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'

import { LogoMark } from '@/components/logo'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { ApiError, errorMessage, get, post } from '@/lib/api'
import type { ConnectSessionInfo } from '@/lib/types'

// The Connect page is opened by an organization's own users (not Relaya
// users), from a one-time link. It keeps to the point: who is asking, for
// which app, and one button.

function Shell({ children }: { children: ReactNode }) {
  useEffect(() => {
    document.title = 'Connect your account'
    return () => {
      document.title = 'Relaya'
    }
  }, [])
  return (
    <div className="flex min-h-svh flex-col items-center justify-center bg-muted/30 px-4 py-10">
      <div className="w-full max-w-md rounded-xl border bg-card p-6 shadow-sm sm:p-8">{children}</div>
      <p className="mt-6 flex items-center gap-1.5 text-xs text-muted-foreground">
        <LockIcon className="size-3" /> Secured by <LogoMark className="size-4" /> Relaya
      </p>
    </div>
  )
}

function Message({ icon, tone, title, children }: { icon: ReactNode; tone: 'ok' | 'bad'; title: string; children?: ReactNode }) {
  return (
    <div className="text-center">
      <div
        className={
          tone === 'ok'
            ? 'mx-auto mb-4 flex size-12 items-center justify-center rounded-full bg-emerald-500/10 text-emerald-600'
            : 'mx-auto mb-4 flex size-12 items-center justify-center rounded-full bg-red-500/10 text-red-600'
        }
      >
        {icon}
      </div>
      <h1 className="text-lg font-semibold">{title}</h1>
      {children && <div className="mt-2 text-sm break-words text-muted-foreground">{children}</div>}
    </div>
  )
}

export function ConnectPage() {
  const { token = '' } = useParams()
  const session = useQuery({
    queryKey: ['connect-session', token],
    queryFn: () => get<ConnectSessionInfo>(`/connect/sessions/${encodeURIComponent(token)}`),
    retry: false,
    refetchOnWindowFocus: false,
  })

  if (session.isPending) {
    return (
      <Shell>
        <Skeleton className="mx-auto mb-4 h-6 w-2/3" />
        <Skeleton className="h-24 w-full" />
      </Shell>
    )
  }
  if (session.error) {
    const notFound = session.error instanceof ApiError && session.error.status === 404
    return (
      <Shell>
        <Message icon={<TriangleAlertIcon />} tone="bad" title={notFound ? 'This link isn’t valid' : 'Something went wrong'}>
          {notFound ? 'Check you copied the whole link, or ask for a new one.' : errorMessage(session.error)}
        </Message>
      </Shell>
    )
  }
  const s = session.data
  if (s.status === 'completed') {
    return (
      <Shell>
        <Message icon={<CheckCircle2Icon />} tone="ok" title="Already connected">
          Your {s.provider_name} account is connected to {s.org_name}. You can close this page.
        </Message>
      </Shell>
    )
  }
  if (s.status === 'expired') {
    return (
      <Shell>
        <Message icon={<TriangleAlertIcon />} tone="bad" title="This link has expired">
          Links work for 30 minutes. Ask {s.org_name} for a new one.
        </Message>
      </Shell>
    )
  }
  return <Shell>{s.auth === 'oauth2' ? <OAuthStart token={token} s={s} /> : <LoginForm token={token} s={s} />}</Shell>
}

function Intro({ s }: { s: ConnectSessionInfo }) {
  return (
    <>
      <h1 className="text-center text-lg font-semibold">Connect {s.provider_name}</h1>
      <p className="mt-2 text-center text-sm text-muted-foreground">
        <span className="font-medium text-foreground">{s.org_name}</span> is asking to connect your {s.provider_name} account.
      </p>
      {s.error && (
        <p className="mt-4 rounded-md border border-red-500/30 bg-red-500/5 p-3 text-sm break-words text-red-700 dark:text-red-400">
          The last try didn’t work: {s.error}
        </p>
      )}
    </>
  )
}

function OAuthStart({ token, s }: { token: string; s: ConnectSessionInfo }) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  async function go() {
    setPending(true)
    setError('')
    try {
      const r = await post<{ redirect_url: string }>(`/connect/sessions/${encodeURIComponent(token)}/authorize`)
      window.location.assign(r.redirect_url)
    } catch (err) {
      setError(errorMessage(err))
      setPending(false)
    }
  }
  return (
    <>
      <Intro s={s} />
      <ul className="mt-5 space-y-2 text-sm text-muted-foreground">
        <li>• You’ll sign in at {s.provider_name} and see exactly what {s.org_name} may access.</li>
        <li>• Your password stays with {s.provider_name}; {s.org_name} only gets an access key, stored encrypted.</li>
        <li>• You can remove access at any time in your {s.provider_name} account.</li>
      </ul>
      {error && <p className="mt-4 text-sm break-words text-destructive">{error}</p>}
      <Button className="mt-6 w-full" size="lg" onClick={go} disabled={pending}>
        {pending ? 'Opening…' : `Continue to ${s.provider_name}`}
      </Button>
    </>
  )
}

function LoginForm({ token, s }: { token: string; s: ConnectSessionInfo }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setPending(true)
    setError('')
    try {
      const r = await post<{ status: string; redirect_url: string }>(`/connect/sessions/${encodeURIComponent(token)}/login`, { email: email.trim(), password })
      setPassword('')
      if (r.redirect_url) window.location.assign(r.redirect_url)
      else setDone(true)
    } catch (err) {
      setError(errorMessage(err))
      setPending(false)
    }
  }

  if (done) {
    return (
      <Message icon={<CheckCircle2Icon />} tone="ok" title="Connected">
        Your {s.provider_name} account is connected to {s.org_name}. You can close this page.
      </Message>
    )
  }
  return (
    <form onSubmit={onSubmit}>
      <Intro s={s} />
      {s.login_label && <p className="mt-4 text-xs text-muted-foreground">Use your {s.login_label}.</p>}
      <div className="mt-4 grid gap-4">
        <div className="grid gap-2">
          <Label htmlFor="cx-email">Email</Label>
          <Input id="cx-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoComplete="username" />
        </div>
        <div className="grid gap-2">
          <Label htmlFor="cx-pw">Password</Label>
          <Input id="cx-pw" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required autoComplete="current-password" />
        </div>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">Checked with {s.provider_name} right away and stored encrypted, only to renew access.</p>
      {error && <p className="mt-4 text-sm break-words text-destructive">{error}</p>}
      <Button type="submit" className="mt-6 w-full" size="lg" disabled={pending}>
        {pending ? 'Connecting…' : `Connect ${s.provider_name}`}
      </Button>
    </form>
  )
}

export function ConnectResultPage() {
  const [q] = useSearchParams()
  const provider = q.get('provider') || 'your'
  const org = q.get('org') || 'the app'
  if (q.get('status') === 'connected') {
    return (
      <Shell>
        <Message icon={<CheckCircle2Icon />} tone="ok" title="Connected">
          Your {provider} account is connected to {org}. You can close this window.
        </Message>
      </Shell>
    )
  }
  return (
    <Shell>
      <Message icon={<TriangleAlertIcon />} tone="bad" title="Couldn’t connect">
        {q.get('error') || 'Something went wrong.'}
        <br />
        Open your link again to retry.
      </Message>
    </Shell>
  )
}
