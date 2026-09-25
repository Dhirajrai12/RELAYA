import { PlusIcon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { toast } from 'sonner'

import { CopyField, EmptyState, ErrorState, PageHeader } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { SimpleSelect } from '@/components/simple-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { dateTime, timeAgo } from '@/lib/format'
import {
  useAddMember,
  useApiKeys,
  useAuditLogs,
  useCreateApiKey,
  useMembers,
  useRemoveMember,
  useRevokeApiKey,
  useUpdateMember,
} from '@/lib/queries'
import { useCanManage, useRole } from '@/lib/role'
import type { Role } from '@/lib/types'

const roleOptions = [
  { value: 'member', label: 'Member: view only' },
  { value: 'admin', label: 'Admin: manage webhooks & keys' },
  { value: 'owner', label: 'Owner: everything' },
]

// ---- members --------------------------------------------------------------------

export function MembersPage() {
  const members = useMembers()
  const add = useAddMember()
  const updateRole = useUpdateMember()
  const remove = useRemoveMember()
  const myRole = useRole()
  const me = useAuth((s) => s.user)
  const canManage = useCanManage()
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('member')

  async function onAdd(e: SubmitEvent) {
    e.preventDefault()
    try {
      await add.mutateAsync({ email: email.trim(), role })
      setEmail('')
      toast.success('Member added')
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  // Admins can't grant or change owner.
  const assignable = roleOptions.filter((o) => myRole === 'owner' || o.value !== 'owner')

  return (
    <>
      <PageHeader title="Members" description="People who can access this organization." />

      {canManage && (
        <form onSubmit={onAdd} className="mb-2 flex flex-wrap gap-2">
          <Input className="w-full sm:w-72" type="email" placeholder="colleague@company.com" value={email} onChange={(e) => setEmail(e.target.value)} required />
          <SimpleSelect className="w-full sm:w-64" value={role} onChange={(v) => setRole(v as Role)} options={assignable} />
          <Button type="submit" disabled={add.isPending}>
            <PlusIcon /> Add member
          </Button>
        </form>
      )}
      {canManage && (
        <p className="mb-6 text-xs text-muted-foreground">They need to sign up first; email invitations are coming soon.</p>
      )}

      {members.error ? (
        <ErrorState error={members.error} />
      ) : members.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead className="hidden md:table-cell">Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead className="hidden sm:table-cell">Joined</TableHead>
                <TableHead className="w-28" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.data.data.map((m) => {
                const isMe = m.user_id === me?.id
                const canEdit = canManage && !isMe && (myRole === 'owner' || m.role !== 'owner')
                return (
                  <TableRow key={m.user_id}>
                    <TableCell className="font-medium">
                      {m.name || '—'} {isMe && <span className="text-xs text-muted-foreground">(you)</span>}
                    </TableCell>
                    <TableCell className="hidden md:table-cell">{m.email}</TableCell>
                    <TableCell>
                      {canEdit ? (
                        <SimpleSelect
                          className="w-32"
                          value={m.role}
                          onChange={(v) =>
                            updateRole
                              .mutateAsync({ userId: m.user_id, role: v as Role })
                              .then(() => toast.success('Role updated'), (err) => toast.error(errorMessage(err)))
                          }
                          options={assignable.map((o) => ({ value: o.value, label: o.value }))}
                        />
                      ) : (
                        <Badge variant="secondary">{m.role}</Badge>
                      )}
                    </TableCell>
                    <TableCell className="hidden text-muted-foreground sm:table-cell">{timeAgo(m.created_at)}</TableCell>
                    <TableCell className="text-right">
                      {(canEdit || isMe) && (
                        <ConfirmButton
                          variant="ghost"
                          destructive
                          title={isMe ? 'Leave this organization?' : `Remove ${m.email}?`}
                          description={isMe ? 'You will lose access immediately.' : 'They lose access immediately.'}
                          confirmLabel={isMe ? 'Leave' : 'Remove'}
                          onConfirm={() => remove.mutateAsync(m.user_id)}
                        >
                          {isMe ? 'Leave' : 'Remove'}
                        </ConfirmButton>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  )
}

// ---- API keys ---------------------------------------------------------------------

export function ApiKeysPage() {
  const keys = useApiKeys()
  const create = useCreateApiKey()
  const revoke = useRevokeApiKey()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [role, setRole] = useState<Role>('member')
  const [newKey, setNewKey] = useState<string | null>(null)

  async function onCreate(e: SubmitEvent) {
    e.preventDefault()
    try {
      const res = await create.mutateAsync({ name: name.trim(), role })
      setNewKey(res.key)
      setName('')
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  return (
    <>
      <PageHeader
        title="API keys"
        description="For scripts, CI and your backend. Send as: Authorization: Bearer rk_…"
        actions={
          <Button onClick={() => setOpen(true)}>
            <PlusIcon /> New key
          </Button>
        }
      />

      {keys.error ? (
        <ErrorState error={keys.error} />
      ) : keys.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : keys.data.data.length === 0 ? (
        <EmptyState title="No API keys">Create one to call the API from code.</EmptyState>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Key</TableHead>
                <TableHead>Role</TableHead>
                <TableHead className="hidden sm:table-cell">Last used</TableHead>
                <TableHead className="hidden md:table-cell">Created</TableHead>
                <TableHead className="w-24" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.data.data.map((k) => (
                <TableRow key={k.id} className={k.revoked_at ? 'opacity-50' : undefined}>
                  <TableCell className="font-medium">{k.name}</TableCell>
                  <TableCell className="font-mono text-xs">{k.prefix}…</TableCell>
                  <TableCell>
                    <Badge variant="secondary">{k.role}</Badge>
                  </TableCell>
                  <TableCell className="hidden text-muted-foreground sm:table-cell">{k.last_used_at ? timeAgo(k.last_used_at) : 'never'}</TableCell>
                  <TableCell className="hidden text-muted-foreground md:table-cell">{timeAgo(k.created_at)}</TableCell>
                  <TableCell className="text-right">
                    {k.revoked_at ? (
                      <span className="text-xs text-muted-foreground">revoked</span>
                    ) : (
                      <ConfirmButton
                        variant="ghost"
                        destructive
                        title={`Revoke “${k.name}”?`}
                        description="Anything using this key stops working immediately. This can't be undone."
                        confirmLabel="Revoke key"
                        onConfirm={() => revoke.mutateAsync(k.id).then(() => toast.success('Key revoked'))}
                      >
                        Revoke
                      </ConfirmButton>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog
        open={open}
        onOpenChange={(o) => {
          setOpen(o)
          if (!o) setNewKey(null)
        }}
      >
        <DialogContent className="sm:max-w-lg">
          {newKey ? (
            <>
              <DialogHeader>
                <DialogTitle>Copy your API key now</DialogTitle>
                <DialogDescription>It won't be shown again. Store it in your secrets manager.</DialogDescription>
              </DialogHeader>
              <CopyField value={newKey} />
              <DialogFooter>
                <Button onClick={() => setOpen(false)}>Done</Button>
              </DialogFooter>
            </>
          ) : (
            <form onSubmit={onCreate} className="grid gap-4">
              <DialogHeader>
                <DialogTitle>New API key</DialogTitle>
              </DialogHeader>
              <div className="grid gap-2">
                <Label htmlFor="key-name">Name</Label>
                <Input id="key-name" placeholder="e.g. CI, backend-prod" value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="key-role">Access</Label>
                <SimpleSelect id="key-role" value={role} onChange={(v) => setRole(v as Role)} options={roleOptions.filter((o) => o.value !== 'owner')} />
              </div>
              <DialogFooter>
                <Button type="submit" disabled={!name.trim() || create.isPending}>
                  Create key
                </Button>
              </DialogFooter>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}

// ---- audit log --------------------------------------------------------------------

export function AuditPage() {
  const logs = useAuditLogs()
  const members = useMembers()
  const rows = logs.data?.pages.flatMap((p) => p.data) ?? []
  const actorName = (type: string, id: string) =>
    type === 'user' ? (members.data?.data.find((m) => m.user_id === id)?.email ?? 'former member') : type === 'api_key' ? 'API key' : 'system'

  return (
    <>
      <PageHeader title="Audit log" description="Every change to this organization: who, what, when, and the result." />
      {logs.error ? (
        <ErrorState error={logs.error} />
      ) : logs.isPending ? (
        <Skeleton className="h-48 w-full" />
      ) : (
        <>
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>When</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead className="hidden lg:table-cell">Target</TableHead>
                  <TableHead className="hidden md:table-cell">Details</TableHead>
                  <TableHead>Result</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((a) => (
                  <TableRow key={a.id}>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground" title={dateTime(a.at)}>
                      {timeAgo(a.at)}
                    </TableCell>
                    <TableCell className="text-sm">{actorName(a.actor_type, a.actor_id)}</TableCell>
                    <TableCell className="font-mono text-xs">{a.action}</TableCell>
                    <TableCell className="hidden text-xs text-muted-foreground lg:table-cell">{a.target_type}</TableCell>
                    <TableCell className="hidden max-w-72 truncate font-mono text-xs text-muted-foreground md:table-cell">
                      {Object.keys(a.metadata).length ? JSON.stringify(a.metadata) : ''}
                    </TableCell>
                    <TableCell>
                      <Badge variant={a.result === 'success' ? 'secondary' : 'destructive'}>{a.result}</Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {logs.hasNextPage && (
            <div className="mt-4 flex justify-center">
              <Button variant="outline" onClick={() => logs.fetchNextPage()} disabled={logs.isFetchingNextPage}>
                Load older
              </Button>
            </div>
          )}
        </>
      )}
    </>
  )
}
