import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { Logo } from '@/components/logo'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage, post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import type { Session } from '@/lib/types'

function AuthLayout({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-6 bg-muted/30 p-4">
      <Logo />
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-lg">{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </div>
  )
}

function Field({ id, label, ...props }: { id: string; label: string } & React.ComponentProps<typeof Input>) {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} name={id} {...props} />
    </div>
  )
}

function useSubmit(path: string, toBody: (f: FormData) => unknown) {
  const signIn = useAuth((s) => s.signIn)
  const navigate = useNavigate()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const s = await post<Session>(path, toBody(new FormData(e.currentTarget)))
      signIn(s.token, s.user)
      navigate('/overview', { replace: true })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return { onSubmit, error, busy }
}

export function LoginPage() {
  const { onSubmit, error, busy } = useSubmit('/auth/login', (f) => ({
    email: f.get('email'),
    password: f.get('password'),
  }))
  return (
    <AuthLayout title="Sign in" description="Watch, debug and recover your integrations.">
      <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4">
        <Field id="email" label="Email" type="email" autoComplete="email" required autoFocus />
        <Field id="password" label="Password" type="password" autoComplete="current-password" required />
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button type="submit" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </Button>
        <p className="text-center text-sm text-muted-foreground">
          No account?{' '}
          <Link to="/signup" className="font-medium text-foreground underline underline-offset-4">
            Create one
          </Link>
        </p>
      </form>
    </AuthLayout>
  )
}

export function SignupPage() {
  const { onSubmit, error, busy } = useSubmit('/auth/signup', (f) => ({
    name: f.get('name'),
    email: f.get('email'),
    password: f.get('password'),
    org_name: f.get('org_name'),
  }))
  return (
    <AuthLayout title="Create your account" description="You'll be the owner of a new organization.">
      <form onSubmit={onSubmit} className="grid grid-cols-1 gap-4">
        <Field id="name" label="Your name" autoComplete="name" autoFocus />
        <Field id="email" label="Work email" type="email" autoComplete="email" required />
        <Field id="password" label="Password (10+ characters)" type="password" autoComplete="new-password" minLength={10} maxLength={72} required />
        <Field id="org_name" label="Company name" autoComplete="organization" required />
        {error && <p className="text-sm text-destructive">{error}</p>}
        <Button type="submit" disabled={busy}>
          {busy ? 'Creating…' : 'Create account'}
        </Button>
        <p className="text-center text-xs text-muted-foreground">
          By creating an account you agree to the{' '}
          <Link to="/terms" target="_blank" className="underline underline-offset-4 hover:text-foreground">
            Terms of Service
          </Link>{' '}
          and{' '}
          <Link to="/privacy" target="_blank" className="underline underline-offset-4 hover:text-foreground">
            Privacy Policy
          </Link>
          .
        </p>
        <p className="text-center text-sm text-muted-foreground">
          Already have an account?{' '}
          <Link to="/login" className="font-medium text-foreground underline underline-offset-4">
            Sign in
          </Link>
        </p>
      </form>
    </AuthLayout>
  )
}
