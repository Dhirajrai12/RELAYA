import { useEffect, useMemo, useState, type SubmitEvent } from 'react'
import { Navigate, Route, Routes } from 'react-router-dom'

import { AppShell } from '@/components/app-shell'
import { ErrorState } from '@/components/common'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { useCreateOrg, useMe } from '@/lib/queries'
import { useHasRole } from '@/lib/role'
import { LoginPage, SignupPage } from '@/pages/auth'
import { EventsPage } from '@/pages/events'
import { OverviewPage } from '@/pages/overview'
import { ProjectsPage } from '@/pages/projects'
import { ApiKeysPage, AuditPage, MembersPage } from '@/pages/settings'
import { WebhookDetailPage } from '@/pages/webhook-detail'
import { WebhooksPage } from '@/pages/webhooks'

export default function App() {
  const token = useAuth((s) => s.token)
  return (
    <Routes>
      <Route path="/login" element={token ? <Navigate to="/overview" replace /> : <LoginPage />} />
      <Route path="/signup" element={token ? <Navigate to="/overview" replace /> : <SignupPage />} />
      <Route path="/*" element={token ? <SignedIn /> : <Navigate to="/login" replace />} />
    </Routes>
  )
}

/** Loads the user's orgs and makes sure a valid one is selected before rendering pages. */
function SignedIn() {
  const me = useMe()
  const { orgId, setOrg } = useAuth()
  const orgs = useMemo(() => me.data?.orgs ?? [], [me.data])
  const valid = orgs.some((o) => o.id === orgId)

  useEffect(() => {
    if (me.data && !valid && orgs.length > 0) setOrg(orgs[0].id)
  }, [me.data, valid, orgs, setOrg])

  if (me.error) return <div className="p-8"><ErrorState error={me.error} /></div>
  if (me.isPending) return <div className="p-8 text-sm text-muted-foreground">Loading…</div>
  if (orgs.length === 0) return <CreateFirstOrg />
  if (!valid) return null // effect above selects one

  return (
    <Routes>
      <Route element={<AppShell orgs={orgs} />}>
        <Route index element={<Navigate to="/overview" replace />} />
        <Route path="overview" element={<OverviewPage />} />
        <Route path="events" element={<EventsPage />} />
        <Route path="webhooks" element={<WebhooksPage />} />
        <Route path="webhooks/:id" element={<WebhookDetailPage />} />
        <Route path="projects" element={<ProjectsPage />} />
        <Route path="settings/members" element={<MembersPage />} />
        <Route path="settings/api-keys" element={<AdminOnly><ApiKeysPage /></AdminOnly>} />
        <Route path="settings/audit" element={<AdminOnly><AuditPage /></AdminOnly>} />
        <Route path="*" element={<Navigate to="/overview" replace />} />
      </Route>
    </Routes>
  )
}

function AdminOnly({ children }: { children: React.ReactNode }) {
  return useHasRole('admin') ? children : <Navigate to="/events" replace />
}

/** Shown if the user belongs to no organization (e.g. they left their only one). */
function CreateFirstOrg() {
  const create = useCreateOrg()
  const signOut = useAuth((s) => s.signOut)
  const [name, setName] = useState('')
  const [error, setError] = useState('')

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    try {
      await create.mutateAsync(name.trim())
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <div className="flex min-h-svh items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Create an organization</CardTitle>
          <CardDescription>You're not a member of any organization yet.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSubmit} className="grid gap-3">
            <Input placeholder="Company name" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" disabled={create.isPending}>Create</Button>
            <Button type="button" variant="ghost" onClick={signOut}>Sign out</Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
