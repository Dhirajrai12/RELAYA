import { lazy, Suspense, useEffect, useMemo, useState, type SubmitEvent } from 'react'
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

// Each area is its own chunk: visitors to the landing page don't download the
// dashboard, and the dashboard doesn't download the landing page or charts
// until they're needed.
const pages = {
  landing: () => import('@/pages/landing/landing'),
  overview: () => import('@/pages/overview'),
  events: () => import('@/pages/events'),
  webhooks: () => import('@/pages/webhooks'),
  webhookDetail: () => import('@/pages/webhook-detail'),
  projects: () => import('@/pages/projects'),
  settings: () => import('@/pages/settings'),
  contracts: () => import('@/pages/contracts'),
  contractDetail: () => import('@/pages/contract-detail'),
  incidents: () => import('@/pages/incidents'),
  alerts: () => import('@/pages/alerts'),
}
const LandingPage = lazy(() => pages.landing().then((m) => ({ default: m.LandingPage })))
const OverviewPage = lazy(() => pages.overview().then((m) => ({ default: m.OverviewPage })))
const EventsPage = lazy(() => pages.events().then((m) => ({ default: m.EventsPage })))
const WebhooksPage = lazy(() => pages.webhooks().then((m) => ({ default: m.WebhooksPage })))
const WebhookDetailPage = lazy(() => pages.webhookDetail().then((m) => ({ default: m.WebhookDetailPage })))
const ProjectsPage = lazy(() => pages.projects().then((m) => ({ default: m.ProjectsPage })))
const MembersPage = lazy(() => pages.settings().then((m) => ({ default: m.MembersPage })))
const ApiKeysPage = lazy(() => pages.settings().then((m) => ({ default: m.ApiKeysPage })))
const AlertsPage = lazy(() => pages.alerts().then((m) => ({ default: m.AlertsPage })))
const AuditPage = lazy(() => pages.settings().then((m) => ({ default: m.AuditPage })))
const ContractsPage = lazy(() => pages.contracts().then((m) => ({ default: m.ContractsPage })))
const ContractDetailPage = lazy(() => pages.contractDetail().then((m) => ({ default: m.ContractDetailPage })))
const IncidentsPage = lazy(() => pages.incidents().then((m) => ({ default: m.IncidentsPage })))

/** Once signed in and idle, fetch the other dashboard pages so navigating never waits. */
function usePreloadDashboard() {
  useEffect(() => {
    const run = () => {
      for (const [name, load] of Object.entries(pages)) if (name !== 'landing') void load()
    }
    const id = setTimeout(run, 1200) // after the first page has rendered
    return () => clearTimeout(id)
  }, [])
}

export default function App() {
  const token = useAuth((s) => s.token)
  return (
    <Suspense fallback={<div className="min-h-svh" />}>
      <Routes>
        <Route path="/login" element={token ? <Navigate to="/overview" replace /> : <LoginPage />} />
        <Route path="/signup" element={token ? <Navigate to="/overview" replace /> : <SignupPage />} />
        {/* Signed-out visitors land on the marketing page; signed-in users go to the app. */}
        <Route path="/" element={token ? <Navigate to="/overview" replace /> : <LandingPage />} />
        <Route path="/*" element={token ? <SignedIn /> : <Navigate to="/login" replace />} />
      </Routes>
    </Suspense>
  )
}

/** Loads the user's orgs and makes sure a valid one is selected before rendering pages. */
function SignedIn() {
  usePreloadDashboard()
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
        <Route path="contracts" element={<ContractsPage />} />
        <Route path="contracts/:id" element={<ContractDetailPage />} />
        <Route path="incidents" element={<IncidentsPage />} />
        <Route path="settings/members" element={<MembersPage />} />
        <Route path="settings/api-keys" element={<AdminOnly><ApiKeysPage /></AdminOnly>} />
        <Route path="settings/alerts" element={<AlertsPage />} />
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
