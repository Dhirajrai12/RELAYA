import {
  ActivityIcon,
  BellIcon,
  FolderIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LogOutIcon,
  MenuIcon,
  PlugIcon,
  RocketIcon,
  ScrollTextIcon,
  SendIcon,
  UsersIcon,
  WebhookIcon,
  ShieldAlertIcon,
  FileCheck2Icon,
} from 'lucide-react'
import { Suspense, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'

import { LiveIndicator } from '@/components/live-indicator'
import { Logo, LogoMark } from '@/components/logo'
import { SimpleSelect } from '@/components/simple-select'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { post } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { useIncidents } from '@/lib/queries'
import { useRealtime } from '@/lib/realtime'
import { cn } from '@/lib/utils'
import type { Org } from '@/lib/types'

const nav = [
  { to: '/overview', label: 'Overview', icon: LayoutDashboardIcon },
  { to: '/quickstart', label: 'Quickstart', icon: RocketIcon },
  { to: '/events', label: 'Events', icon: ActivityIcon },
  { to: '/incidents', label: 'Incidents', icon: ShieldAlertIcon, badge: 'incidents' as const },
  { to: '/webhooks', label: 'Webhooks', icon: WebhookIcon },
  { to: '/contracts', label: 'Contracts', icon: FileCheck2Icon },
  { to: '/connections', label: 'Connections', icon: PlugIcon },
  { to: '/outbound', label: 'Outbound', icon: SendIcon },
  { to: '/projects', label: 'Projects', icon: FolderIcon },
]

const settingsNav = [
  { to: '/settings/members', label: 'Members', icon: UsersIcon },
  { to: '/settings/alerts', label: 'Alerts', icon: BellIcon },
  { to: '/settings/api-keys', label: 'API keys', icon: KeyRoundIcon, minRole: 'admin' },
  { to: '/settings/audit', label: 'Audit log', icon: ScrollTextIcon, minRole: 'admin' },
]

function NavItem({
  to,
  label,
  icon: Icon,
  onNavigate,
  badge,
}: {
  to: string
  label: string
  icon: typeof ActivityIcon
  onNavigate?: () => void
  badge?: 'incidents'
}) {
  const incidents = useIncidents('open')
  const count = badge === 'incidents' ? (incidents.data?.data.length ?? 0) : 0
  return (
    <NavLink
      to={to}
      onClick={onNavigate}
      className={({ isActive }) =>
        cn(
          'relative flex items-center gap-2.5 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground',
          isActive &&
            'bg-muted font-medium text-foreground before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-brand',
        )
      }
    >
      <Icon className="size-4" />
      {label}
      {count > 0 && (
        <span className="ml-auto rounded-full bg-red-600 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white" aria-label={`${count} open`}>
          {count}
        </span>
      )}
    </NavLink>
  )
}

/** Everything inside the sidebar; shared by the desktop rail and the mobile drawer. */
function SidebarContent({ orgs, onNavigate }: { orgs: Org[]; onNavigate?: () => void }) {
  const { user, orgId, setOrg, signOut } = useAuth()
  const qc = useQueryClient()
  const org = orgs.find((o) => o.id === orgId)
  const isAdmin = org?.role === 'owner' || org?.role === 'admin'

  async function logout() {
    await post('/auth/logout').catch(() => {}) // session may already be gone
    signOut()
    qc.clear()
  }

  return (
    <div className="flex h-full flex-col p-3">
      <Logo className="px-2 pb-2 pt-1" />
      <LiveIndicator className="mb-4 px-2" />

      {/* Organization switcher: only when the user belongs to more than one. */}
      {orgs.length > 1 && (
        <SimpleSelect
          className="mb-4 w-full"
          value={orgId ?? ''}
          onChange={(v) => {
            setOrg(v)
            qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'me' })
          }}
          options={orgs.map((o) => ({ value: o.id, label: o.name }))}
        />
      )}

      {/* The menu scrolls on short screens; the account block stays at the bottom. */}
      <div className="-mx-3 min-h-0 flex-1 overflow-y-auto px-3 pb-3">
        <nav className="flex flex-col gap-0.5">
          {nav.map((n) => (
            <NavItem key={n.to} {...n} onNavigate={onNavigate} />
          ))}
        </nav>

        <div className="mt-6 px-3 pb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">Settings</div>
        <nav className="flex flex-col gap-0.5">
          {settingsNav
            .filter((n) => !n.minRole || isAdmin)
            .map((n) => (
              <NavItem key={n.to} {...n} onNavigate={onNavigate} />
            ))}
        </nav>
      </div>

      <div className="shrink-0 space-y-3 border-t pt-3">
        <div className="flex items-center gap-2.5 px-1">
          <div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-brand/15 text-sm font-semibold text-brand-foreground dark:text-brand">
            {(user?.name || user?.email || '?').charAt(0).toUpperCase()}
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-medium">{user?.name || user?.email}</div>
            <div className="truncate text-xs text-muted-foreground">
              {user?.email} · {org?.role}
            </div>
          </div>
        </div>
        <div className="flex items-center justify-between gap-2 px-1">
          <ThemeToggle />
          <Button variant="ghost" size="sm" onClick={logout}>
            <LogOutIcon />
            Sign out
          </Button>
        </div>
      </div>
    </div>
  )
}

export function AppShell({ orgs }: { orgs: Org[] }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const orgId = useAuth((s) => s.orgId)
  useRealtime(orgId) // one live connection for the whole app
  return (
    <div className="min-h-svh md:flex">
      {/* Desktop: fixed sidebar */}
      <aside className="sticky top-0 hidden h-svh w-64 shrink-0 border-r bg-sidebar md:block">
        <SidebarContent orgs={orgs} />
      </aside>

      {/* Mobile: top bar + drawer */}
      <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b bg-background/85 px-4 backdrop-blur md:hidden">
        <Button variant="ghost" size="icon" aria-label="Open menu" onClick={() => setMenuOpen(true)}>
          <MenuIcon />
        </Button>
        <LogoMark className="size-7" />
        <span className="text-sm font-semibold tracking-[0.18em]">RELAYA</span>
        <LiveIndicator className="ml-auto" />
      </header>
      <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
        <SheetContent side="left" className="w-72 p-0 data-[side=left]:w-72">
          <SheetTitle className="sr-only">Menu</SheetTitle>
          <SidebarContent orgs={orgs} onNavigate={() => setMenuOpen(false)} />
        </SheetContent>
      </Sheet>

      <main className="min-w-0 flex-1 px-4 py-5 md:px-8 md:py-7">
        <div className="mx-auto max-w-7xl">
          {/* Page chunks load behind the shell, which stays visible. */}
          <Suspense fallback={<PageFallback />}>
            <Outlet />
          </Suspense>
        </div>
      </main>
    </div>
  )
}

function PageFallback() {
  return (
    <div className="space-y-4" aria-busy="true">
      <div className="h-8 w-48 animate-pulse rounded-md bg-muted" />
      <div className="h-40 animate-pulse rounded-xl bg-muted" />
    </div>
  )
}
