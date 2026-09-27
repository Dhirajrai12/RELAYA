import { useQueryClient } from '@tanstack/react-query'
import { ExternalLinkIcon, LinkIcon, PencilIcon, PlugIcon, PlusIcon, RefreshCwIcon, TerminalIcon, Trash2Icon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { toast } from 'sonner'

import { CopyButton, CopyField, EmptyState, ErrorState, PageHeader } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { SimpleSelect } from '@/components/simple-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { dateTime, timeAgo } from '@/lib/format'
import {
  useConnectProviders,
  useConnections,
  useCreateConnectSession,
  useCreateIntegration,
  useDeleteConnection,
  useDeleteIntegration,
  useIntegrations,
  useProxyCalls,
  useRefreshConnection,
  useUpdateIntegration,
} from '@/lib/queries'
import { useCanManage } from '@/lib/role'
import { cn } from '@/lib/utils'
import type { Connection, ConnectProvider, Integration } from '@/lib/types'
import { SyncsCard } from '@/pages/syncs-card'

/** How to create the OAuth app (or API login) at each provider. `{callback}` is replaced. */
const setupSteps: Record<string, string[]> = {
  zoho: [
    'Open the Zoho API Console (api-console.zoho.com, or api-console.zoho.in for India) → Add Client → Server-based Applications.',
    'Authorized Redirect URI: {callback}',
    'Copy the Client ID and Client Secret below. If your users are in several regions, turn on Multi-DC in the client’s settings.',
  ],
  hubspot: [
    'HubSpot developer account → Apps → Create app → Auth.',
    'Redirect URL: {callback}',
    'Tick the same scopes as below (they must match exactly), then copy the Client ID and Client secret.',
  ],
  google: [
    'Google Cloud console → APIs & Services → Credentials → Create credentials → OAuth client ID → Web application.',
    'Authorized redirect URI: {callback}',
    'Set up the OAuth consent screen and enable the APIs you will call (e.g. Google Sheets API), then copy the Client ID and secret.',
  ],
  shiprocket: [
    'No app to create. Each of your users connects with a Shiprocket API user.',
    'They make one in Shiprocket → Settings → API → Configure → Create an API user (its email must differ from their main login).',
  ],
}

export function ConnectionsPage() {
  const integrations = useIntegrations()
  const canManage = useCanManage()
  const [editing, setEditing] = useState<Integration | 'new' | null>(null)
  const [linkFor, setLinkFor] = useState<{ integration: string; endUser: string } | null>(null)
  const [tryFor, setTryFor] = useState<Connection | null>(null)
  const list = integrations.data?.data ?? []

  return (
    <>
      <PageHeader
        title="Connections"
        description="Let your users connect their Zoho, HubSpot, Google or Shiprocket accounts. Relaya stores their tokens encrypted, keeps them fresh, and alerts you when one breaks."
        actions={
          canManage &&
          list.length > 0 && (
            <>
              <Button variant="outline" onClick={() => setEditing('new')}>
                <PlusIcon /> Add integration
              </Button>
              <Button onClick={() => setLinkFor({ integration: list[0].key, endUser: '' })}>
                <LinkIcon /> Create connect link
              </Button>
            </>
          )
        }
      />

      {integrations.error ? (
        <ErrorState error={integrations.error} />
      ) : integrations.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : list.length === 0 ? (
        <EmptyState
          title="No integrations yet"
          icon={PlugIcon}
          action={
            canManage && (
              <Button onClick={() => setEditing('new')}>
                <PlusIcon /> Add integration
              </Button>
            )
          }
        >
          Add the apps your users should connect. For Zoho, HubSpot and Google you use your own OAuth app, so users see your name when they approve.
          {!canManage && ' Ask an admin to add one.'}
        </EmptyState>
      ) : (
        <>
          <ul className="divide-y rounded-lg border">
            {list.map((i) => (
              <IntegrationRow key={i.id} i={i} onEdit={() => setEditing(i)} />
            ))}
          </ul>
          <ConnectionsCard onReconnect={(c) => setLinkFor({ integration: c.integration_key, endUser: c.end_user_id })} onTry={setTryFor} />
          <SyncsCard />
          <RecentCallsCard />
          <UsageCard />
        </>
      )}

      <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          {editing && <IntegrationForm key={editing === 'new' ? 'new' : editing.id} integration={editing} onClose={() => setEditing(null)} />}
        </DialogContent>
      </Dialog>
      <Dialog open={linkFor !== null} onOpenChange={(o) => !o && setLinkFor(null)}>
        <DialogContent className="sm:max-w-lg">
          {linkFor && <ConnectLinkForm key={linkFor.integration + linkFor.endUser} initial={linkFor} integrations={list} />}
        </DialogContent>
      </Dialog>
      <Dialog open={tryFor !== null} onOpenChange={(o) => !o && setTryFor(null)}>
        <DialogContent className="sm:max-w-2xl">{tryFor && <TryCallForm key={tryFor.id} c={tryFor} />}</DialogContent>
      </Dialog>
    </>
  )
}

function IntegrationRow({ i, onEdit }: { i: Integration; onEdit: () => void }) {
  const canManage = useCanManage()
  const remove = useDeleteIntegration()
  return (
    <li className="flex flex-wrap items-start gap-3 p-4">
      <div className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted/40 text-sm font-semibold text-muted-foreground">
        {i.provider_name.slice(0, 1)}
      </div>
      <div className="min-w-0 flex-1 basis-48">
        <div className="flex flex-wrap items-center gap-2">
          <span className="min-w-0 break-words font-medium">{i.name}</span>
          <Badge variant="secondary">{i.provider_name}</Badge>
          <code className="rounded border px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">{i.key}</code>
        </div>
        {i.auth === 'oauth2' && (
          <div className="mt-0.5 truncate font-mono text-xs text-muted-foreground" title={i.client_id}>
            client {i.client_id}
          </div>
        )}
        {i.scopes.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {i.scopes.map((s) => (
              <span key={s} className="max-w-full truncate rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground" title={s}>
                {s}
              </span>
            ))}
          </div>
        )}
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
          <span className="text-muted-foreground">
            {i.connections} connected account{i.connections === 1 ? '' : 's'}
          </span>
          {i.broken > 0 && <span className="text-red-700 dark:text-red-400">{i.broken} broken</span>}
        </div>
      </div>
      {canManage && (
        <div className="flex items-center gap-1">
          <Button size="sm" variant="ghost" aria-label="Edit" onClick={onEdit}>
            <PencilIcon />
          </Button>
          <ConfirmButton
            variant="ghost"
            destructive
            title={`Delete “${i.name}”?`}
            description={
              i.connections > 0
                ? `Its ${i.connections} connected account${i.connections === 1 ? ' is' : 's are'} deleted too, with their tokens. Users would have to connect again.`
                : 'Nobody has connected with it yet.'
            }
            confirmLabel="Delete integration"
            onConfirm={() => remove.mutateAsync(i.id).then(() => toast.success('Integration deleted'))}
          >
            <Trash2Icon />
          </ConfirmButton>
        </div>
      )}
    </li>
  )
}

function IntegrationForm({ integration, onClose }: { integration: Integration | 'new'; onClose: () => void }) {
  const isNew = integration === 'new'
  const providers = useConnectProviders()
  const create = useCreateIntegration()
  const update = useUpdateIntegration()
  const all = providers.data?.data ?? []
  const [providerKey, setProviderKey] = useState(isNew ? '' : integration.provider)
  const provider: ConnectProvider | undefined = all.find((p) => p.key === providerKey)
  const [name, setName] = useState(isNew ? '' : integration.name)
  const [key, setKey] = useState(isNew ? '' : integration.key)
  const [clientId, setClientId] = useState(isNew ? '' : integration.client_id)
  const [clientSecret, setClientSecret] = useState('')
  const [scopes, setScopes] = useState(isNew ? '' : integration.scopes.join('\n'))
  const [error, setError] = useState('')
  const pending = create.isPending || update.isPending
  const oauth = (provider?.auth ?? (isNew ? undefined : integration.auth)) === 'oauth2'
  const callback = providers.data?.callback_url ?? ''

  function pick(p: ConnectProvider) {
    setProviderKey(p.key)
    setName(p.name)
    setKey(p.key)
    setScopes((p.default_scopes ?? []).join('\n'))
  }

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setError('')
    const scopeList = scopes.split(/[\s,]+/).filter(Boolean)
    try {
      if (isNew) {
        await create.mutateAsync({
          provider: providerKey,
          key: key.trim(),
          name: name.trim(),
          ...(oauth ? { client_id: clientId.trim(), client_secret: clientSecret.trim(), scopes: scopeList } : {}),
        })
        toast.success('Integration added. Create a connect link to try it.')
      } else {
        await update.mutateAsync({
          id: integration.id,
          name: name.trim(),
          ...(oauth ? { client_id: clientId.trim(), scopes: scopeList, ...(clientSecret.trim() ? { client_secret: clientSecret.trim() } : {}) } : {}),
        })
        toast.success('Integration updated')
      }
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <form onSubmit={onSubmit} className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>{isNew ? 'Add an integration' : `Edit “${integration.name}”`}</DialogTitle>
        <DialogDescription>{isNew ? 'Which app your users will connect, and your OAuth app for it.' : 'Change its name, OAuth client or scopes.'}</DialogDescription>
      </DialogHeader>

      {isNew && (
        <div className="grid gap-2">
          <Label>App</Label>
          {providers.isPending ? (
            <Skeleton className="h-10 w-full" />
          ) : (
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-4" role="radiogroup">
              {all.map((p) => (
                <button
                  key={p.key}
                  type="button"
                  role="radio"
                  aria-checked={providerKey === p.key}
                  onClick={() => pick(p)}
                  className={cn(
                    'min-w-0 truncate rounded-md border px-2 py-2 text-sm transition-colors',
                    providerKey === p.key ? 'border-brand bg-brand/10 font-medium text-foreground' : 'text-muted-foreground hover:bg-muted',
                  )}
                >
                  {p.name}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {(provider || !isNew) && (
        <>
          {isNew && provider && (
            <div className="min-w-0 rounded-md border bg-muted/30 p-3 text-xs">
              <ol className="list-decimal space-y-1.5 pl-4 text-muted-foreground">
                {(setupSteps[provider.key] ?? []).map((s) =>
                  s.includes('{callback}') ? (
                    <li key={s}>
                      {s.split('{callback}')[0]}
                      <CopyField value={callback} className="mt-1" />
                    </li>
                  ) : (
                    <li key={s} className="break-words">
                      {s}
                    </li>
                  ),
                )}
              </ol>
              {provider.docs_url && (
                <a href={provider.docs_url} target="_blank" rel="noreferrer" className="mt-2 inline-flex items-center gap-1 text-brand hover:underline">
                  {provider.name} docs <ExternalLinkIcon className="size-3" />
                </a>
              )}
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label htmlFor="in-name">Name</Label>
              <Input id="in-name" value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="in-key">Key</Label>
              <Input
                id="in-key"
                value={key}
                onChange={(e) => setKey(e.target.value.toLowerCase())}
                disabled={!isNew}
                required
                maxLength={40}
                pattern="[a-z0-9][a-z0-9_\-]*"
                className="font-mono"
              />
            </div>
          </div>
          {isNew && <p className="-mt-2 text-xs text-muted-foreground">Your code refers to the integration by its key, e.g. when creating connect links.</p>}

          {oauth && (
            <>
              <div className="grid gap-2">
                <Label htmlFor="in-cid">Client ID</Label>
                <Input id="in-cid" value={clientId} onChange={(e) => setClientId(e.target.value)} required autoComplete="off" className="font-mono" />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="in-secret">Client secret</Label>
                <Input
                  id="in-secret"
                  type="password"
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  required={isNew}
                  autoComplete="new-password"
                  placeholder={isNew ? '' : 'Leave empty to keep the current secret'}
                />
                <p className="text-xs text-muted-foreground">Stored encrypted. Never shown again.</p>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="in-scopes">Scopes</Label>
                <textarea
                  id="in-scopes"
                  value={scopes}
                  onChange={(e) => setScopes(e.target.value)}
                  rows={3}
                  className="min-w-0 rounded-md border bg-transparent px-3 py-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                />
                <p className="text-xs text-muted-foreground">One per line. What your app may do with each user’s account.</p>
              </div>
            </>
          )}
        </>
      )}

      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={pending || (isNew && !provider)}>
          {pending ? 'Saving…' : isNew ? 'Add integration' : 'Save'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function ConnectLinkForm({ initial, integrations }: { initial: { integration: string; endUser: string }; integrations: Integration[] }) {
  const create = useCreateConnectSession()
  const [integration, setIntegration] = useState(initial.integration)
  const [endUser, setEndUser] = useState(initial.endUser)
  const [returnUrl, setReturnUrl] = useState('')
  const [link, setLink] = useState<{ url: string; expires_at: string } | null>(null)
  const [error, setError] = useState('')

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setError('')
    try {
      setLink(await create.mutateAsync({ integration, end_user_id: endUser.trim(), ...(returnUrl.trim() ? { return_url: returnUrl.trim() } : {}) }))
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  if (link) {
    return (
      <div className="grid min-w-0 grid-cols-1 gap-4">
        <DialogHeader>
          <DialogTitle>Connect link ready</DialogTitle>
          <DialogDescription>
            Send it to {endUser.trim()}. It works once, until {dateTime(link.expires_at)}.
          </DialogDescription>
        </DialogHeader>
        <CopyField value={link.url} />
        <DialogFooter>
          <Button variant="outline" onClick={() => window.open(link.url, '_blank', 'noopener')}>
            <ExternalLinkIcon /> Open it myself
          </Button>
        </DialogFooter>
      </div>
    )
  }

  return (
    <form onSubmit={onSubmit} className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>{initial.endUser ? `Reconnect ${initial.endUser}` : 'Create a connect link'}</DialogTitle>
        <DialogDescription>A one-time link where your user signs in to the app. Your backend can make these with the API too.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor="cl-int">Integration</Label>
        <SimpleSelect
          id="cl-int"
          className="w-full"
          value={integration}
          onChange={setIntegration}
          options={integrations.map((i) => ({ value: i.key, label: `${i.name} (${i.provider_name})` }))}
        />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="cl-user">End user ID</Label>
        <Input id="cl-user" value={endUser} onChange={(e) => setEndUser(e.target.value)} required maxLength={200} placeholder="customer-123" className="font-mono" />
        <p className="text-xs text-muted-foreground">Your ID for this user or account. Connecting again with the same ID replaces their old tokens.</p>
      </div>
      <div className="grid gap-2">
        <Label htmlFor="cl-return">Return URL (optional)</Label>
        <Input id="cl-return" type="url" value={returnUrl} onChange={(e) => setReturnUrl(e.target.value)} placeholder="https://yourapp.com/settings/integrations" />
        <p className="text-xs text-muted-foreground">Where to send the user afterwards, with ?status=connected&amp;connection_id=…</p>
      </div>
      {error && <p className="break-words text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="submit" disabled={create.isPending || !endUser.trim()}>
          {create.isPending ? 'Creating…' : 'Create link'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function ConnectionsCard({ onReconnect, onTry }: { onReconnect: (c: Connection) => void; onTry: (c: Connection) => void }) {
  const conns = useConnections()
  const rows = conns.data?.data ?? []
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Connected accounts</CardTitle>
        <CardDescription>Tokens are renewed before they expire. A connection breaks when the user revokes access or changes their login.</CardDescription>
      </CardHeader>
      <CardContent>
        {conns.error ? (
          <ErrorState error={conns.error} />
        ) : conns.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nobody has connected yet. Create a connect link and open it to try.</p>
        ) : (
          <div className="rounded-lg border">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow>
                  <TableHead>Account</TableHead>
                  <TableHead className="hidden w-24 sm:table-cell">Status</TableHead>
                  <TableHead className="hidden w-44 md:table-cell">Token</TableHead>
                  <TableHead className="w-20 text-right sm:w-36" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((c) => (
                  <ConnectionRow key={c.id} c={c} onReconnect={() => onReconnect(c)} onTry={() => onTry(c)} />
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function ConnectionRow({ c, onReconnect, onTry }: { c: Connection; onReconnect: () => void; onTry: () => void }) {
  const canManage = useCanManage()
  const refresh = useRefreshConnection()
  const remove = useDeleteConnection()
  const warning = typeof c.metadata.warning === 'string' ? c.metadata.warning : ''
  const token = c.status === 'broken' ? '' : c.expires_at ? `expires ${timeAgo(c.expires_at)}` : 'no expiry'
  const badge = (
    <Badge
      variant="outline"
      className={
        c.status === 'active'
          ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
          : 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400'
      }
    >
      {c.status === 'active' ? 'working' : 'broken'}
    </Badge>
  )

  return (
    <TableRow>
      <TableCell className="align-top whitespace-normal">
        <div className="break-all font-mono text-sm sm:truncate sm:break-normal" title={c.end_user_id}>
          {c.end_user_id}
        </div>
        <div className="text-xs text-muted-foreground sm:truncate">
          {c.integration_name} · connected {timeAgo(c.created_at)}
          {token && <span className="md:hidden"> · {token}</span>}
        </div>
        <div className="mt-1 sm:hidden">{badge}</div>
        {(c.last_error || warning) && (
          <div className="mt-1 line-clamp-3 break-words text-xs text-red-700 sm:line-clamp-2 dark:text-red-400" title={c.last_error || warning}>
            {c.last_error || warning}
          </div>
        )}
      </TableCell>
      <TableCell className="hidden align-top sm:table-cell">{badge}</TableCell>
      <TableCell className="hidden align-top text-xs whitespace-normal text-muted-foreground md:table-cell">
        <div title={c.expires_at ? dateTime(c.expires_at) : ''}>{token || 'needs reconnecting'}</div>
        {c.last_refreshed_at && <div>renewed {timeAgo(c.last_refreshed_at)}</div>}
      </TableCell>
      <TableCell className="align-top">
        <div className="flex flex-wrap justify-end gap-1">
          {canManage && c.status === 'broken' && (
            <Button size="sm" variant="outline" onClick={onReconnect}>
              <LinkIcon /> <span className="hidden sm:inline">Reconnect</span>
            </Button>
          )}
          {canManage && c.status === 'active' && (
            <Button size="sm" variant="ghost" aria-label="Try a call" title="Try a call" onClick={onTry}>
              <TerminalIcon />
            </Button>
          )}
          {canManage && (
            <Button
              size="sm"
              variant="ghost"
              aria-label="Renew token now"
              title="Renew token now"
              disabled={refresh.isPending}
              onClick={() =>
                refresh.mutateAsync(c.id).then(
                  (r) => (r.refreshed ? toast.success('Token renewed: the connection works') : toast.error(`Could not renew: ${r.error}`)),
                  (e) => toast.error(errorMessage(e)),
                )
              }
            >
              <RefreshCwIcon className={cn(refresh.isPending && 'animate-spin')} />
            </Button>
          )}
          {canManage && (
            <ConfirmButton
              variant="ghost"
              destructive
              title={`Delete ${c.end_user_id}’s connection?`}
              description="Its tokens are deleted. The account at the provider is not touched; the user can connect again."
              confirmLabel="Delete connection"
              onConfirm={() => remove.mutateAsync(c.id).then(() => toast.success('Connection deleted'))}
            >
              <Trash2Icon />
            </ConfirmButton>
          )}
        </div>
      </TableCell>
    </TableRow>
  )
}

function UsageCard() {
  const orgId = useAuth((s) => s.orgId)
  const api = `${location.origin}/api/v1/orgs/${orgId}`
  const snippet = `# 1. Your backend creates a link for a user (API key with admin role)
curl -X POST ${api}/connect-sessions \\
  -H "Authorization: Bearer $RELAYA_API_KEY" \\
  -d '{"integration":"zoho","end_user_id":"customer-123","return_url":"https://yourapp.com/done"}'

# 2a. Call the app through Relaya: the user's token is added, renewed and retried for you
curl "${api}/connections/$CONNECTION_ID/proxy/crm/v2/Leads?per_page=10" \\
  -H "Authorization: Bearer $RELAYA_API_KEY"
#     Another API host of the same provider: -H "Relaya-Proxy-Base-Url: https://sheets.googleapis.com"
#     Extra headers for the provider: -H "Relaya-Proxy-X-Some-Header: value"

# 2b. Or take a fresh token and call the app yourself
curl ${api}/connections/$CONNECTION_ID/token -H "Authorization: Bearer $RELAYA_API_KEY"
# → {"access_token":"…","api_base":"https://www.zohoapis.in","expires_at":"…"}`
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Use it from your backend</CardTitle>
        <CardDescription>
          Call through Relaya, or ask for the token right before each call; either way it is renewed when needed. Find connections by user with
          ?end_user_id=…
        </CardDescription>
      </CardHeader>
      <CardContent className="min-w-0">
        <pre className="overflow-x-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed">{snippet}</pre>
        <div className="mt-2 flex justify-end">
          <CopyButton value={snippet} />
        </div>
      </CardContent>
    </Card>
  )
}

/** Example calls that work with the default scopes, per provider. */
const tryExamples: Record<string, { base?: string; path: string; note: string }> = {
  google: { base: 'https://openidconnect.googleapis.com', path: '/v1/userinfo', note: 'Example: your Google profile.' },
  hubspot: { path: '/crm/v3/objects/contacts?limit=5', note: 'Example: the first 5 contacts.' },
  zoho: { path: '/crm/v2/users?type=CurrentUser', note: 'Example: the connected Zoho CRM user.' },
  shiprocket: { path: '/orders?per_page=5', note: 'Example: the latest 5 orders.' },
}

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

interface TryResult {
  status: number
  attempts: string
  ms: number
  relayaError: boolean
  text: string
}

function TryCallForm({ c }: { c: Connection }) {
  const orgId = useAuth((s) => s.orgId)
  const token = useAuth((s) => s.token)
  const providers = useConnectProviders()
  const qc = useQueryClient()
  const example = tryExamples[c.provider]
  const storedBase = typeof c.metadata.api_base === 'string' ? c.metadata.api_base : ''
  const apiBase = storedBase || providers.data?.data.find((p) => p.key === c.provider)?.api_base || ''
  const [base, setBase] = useState(example?.base ?? '')
  const [method, setMethod] = useState('GET')
  const [path, setPath] = useState(example?.path ?? '/')
  const [body, setBody] = useState('')
  const [pending, setPending] = useState(false)
  const [result, setResult] = useState<TryResult | null>(null)
  const hasBody = method !== 'GET' && method !== 'DELETE'

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault()
    setPending(true)
    setResult(null)
    const started = performance.now()
    try {
      const headers: Record<string, string> = { Authorization: `Bearer ${token}`, Accept: 'application/json' }
      if (base.trim()) headers['Relaya-Proxy-Base-Url'] = base.trim()
      const send = hasBody && body.trim() !== ''
      if (send) headers['Content-Type'] = 'application/json'
      const res = await fetch(`/api/v1/orgs/${orgId}/connections/${c.id}/proxy/${path.trim().replace(/^\/+/, '')}`, {
        method,
        headers,
        body: send ? body : undefined,
      })
      let text = await res.text()
      try {
        text = JSON.stringify(JSON.parse(text), null, 2)
      } catch {
        // not JSON: shown as is
      }
      if (text.length > 20_000) text = text.slice(0, 20_000) + '\n… (cut at 20 KB)'
      setResult({
        status: res.status,
        attempts: res.headers.get('Relaya-Proxy-Attempts') ?? '',
        ms: Math.round(performance.now() - started),
        relayaError: res.headers.get('Relaya-Proxy-Error') === 'true',
        text,
      })
      qc.invalidateQueries({ queryKey: ['proxy-calls', orgId] })
      qc.invalidateQueries({ queryKey: ['connections', orgId] })
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setPending(false)
    }
  }

  const ok = result !== null && result.status < 300
  return (
    <form onSubmit={onSubmit} className="grid min-w-0 grid-cols-1 gap-4">
      <DialogHeader>
        <DialogTitle>Try a call</DialogTitle>
        <DialogDescription>
          Calls {c.integration_name} as {c.end_user_id} through Relaya; the token is added for you. {example?.note}
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-2">
        <Label htmlFor="tc-base">Base URL</Label>
        <Input id="tc-base" value={base} onChange={(e) => setBase(e.target.value)} placeholder={apiBase} className="font-mono text-xs" />
        <p className="text-xs text-muted-foreground">Empty uses {apiBase || 'the provider’s API'}. Only the provider’s own API hosts are allowed.</p>
      </div>
      <div className="grid grid-cols-[6.5rem_minmax(0,1fr)] gap-2">
        <SimpleSelect id="tc-method" className="w-full" value={method} onChange={setMethod} options={METHODS.map((m) => ({ value: m, label: m }))} />
        <Input aria-label="Path" value={path} onChange={(e) => setPath(e.target.value)} className="font-mono text-xs" required />
      </div>
      {hasBody && (
        <textarea
          aria-label="JSON body"
          value={body}
          onChange={(e) => setBody(e.target.value)}
          rows={4}
          placeholder="JSON body"
          className="min-w-0 rounded-md border bg-transparent px-3 py-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
        />
      )}
      {result && (
        <div className="grid min-w-0 gap-2">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge
              variant="outline"
              className={
                ok
                  ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
                  : 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400'
              }
            >
              HTTP {result.status}
            </Badge>
            <span className="text-muted-foreground">
              {result.relayaError ? 'from Relaya: the call was not made' : `from ${c.integration_name}`} · {result.ms} ms
              {result.attempts && result.attempts !== '1' && ` · ${result.attempts} attempts`}
            </span>
          </div>
          <pre className="max-h-72 overflow-auto rounded-md border bg-muted p-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap">
            {result.text || '(empty response)'}
          </pre>
        </div>
      )}
      <DialogFooter>
        <Button type="submit" disabled={pending}>
          {pending ? 'Calling…' : 'Send'}
        </Button>
      </DialogFooter>
    </form>
  )
}

const callOK = 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
const callBad = 'border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-400'

function RecentCallsCard() {
  const calls = useProxyCalls()
  const rows = calls.data?.data ?? []
  if (!calls.isPending && !calls.error && rows.length === 0) return null
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Recent API calls</CardTitle>
        <CardDescription>The last 100 calls made through connections. Query strings and bodies aren’t kept.</CardDescription>
      </CardHeader>
      <CardContent>
        {calls.error ? (
          <ErrorState error={calls.error} />
        ) : calls.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : (
          <div className="rounded-lg border">
            <Table className="table-fixed">
              <TableHeader>
                <TableRow>
                  <TableHead className="hidden w-28 sm:table-cell">When</TableHead>
                  <TableHead>Call</TableHead>
                  <TableHead className="w-20">Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="hidden align-top text-xs whitespace-normal text-muted-foreground sm:table-cell" title={dateTime(r.created_at)}>
                      {timeAgo(r.created_at)}
                    </TableCell>
                    <TableCell className="align-top whitespace-normal">
                      <div className="truncate font-mono text-xs" title={`${r.method} ${r.host}${r.path}`}>
                        <span className="font-semibold">{r.method}</span> {r.host}
                        {r.path}
                      </div>
                      <div className="truncate text-xs text-muted-foreground">
                        <span className="sm:hidden">{timeAgo(r.created_at)} · </span>
                        {r.integration_name} · {r.end_user_id} · {r.duration_ms} ms{r.attempts > 1 && ` · ${r.attempts} attempts`}
                      </div>
                      {r.error && (
                        <div className="mt-0.5 line-clamp-2 break-words text-xs text-red-700 dark:text-red-400" title={r.error}>
                          {r.error}
                        </div>
                      )}
                    </TableCell>
                    <TableCell className="align-top">
                      <Badge variant="outline" className={r.status > 0 && r.status < 400 ? callOK : callBad}>
                        {r.status || 'failed'}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
