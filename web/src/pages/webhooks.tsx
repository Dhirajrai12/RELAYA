import { PlusIcon, ShieldCheckIcon, ShieldOffIcon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { toast } from 'sonner'

import { EmptyState, ErrorState, PageHeader } from '@/components/common'
import { SimpleSelect } from '@/components/simple-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { errorMessage } from '@/lib/api'
import { providerLabel, timeAgo } from '@/lib/format'
import { useCreateWebhook, useProjects, useProviders, useWebhooks } from '@/lib/queries'
import { useCanManage } from '@/lib/role'

export function WebhooksPage() {
  const webhooks = useWebhooks()
  const projects = useProjects()
  const canManage = useCanManage()
  const [open, setOpen] = useState(false)
  const projectName = (id: string) => projects.data?.data.find((p) => p.id === id)?.name ?? '—'

  const createButton = canManage && (
    <Button onClick={() => setOpen(true)}>
      <PlusIcon />
      New webhook
    </Button>
  )

  return (
    <>
      <PageHeader
        title="Webhooks"
        description="Each webhook is a URL you give to a provider (Razorpay, Stripe, Shopify…). Deliveries are verified and stored."
        actions={createButton}
      />

      {webhooks.error ? (
        <ErrorState error={webhooks.error} />
      ) : webhooks.isPending ? (
        <Skeleton className="h-40 w-full" />
      ) : webhooks.data.data.length === 0 ? (
        <EmptyState title="No webhooks yet" action={createButton}>
          Create a webhook URL, paste it into your provider's dashboard, and every delivery will show up in Events.
        </EmptyState>
      ) : (
        <>
        <ul className="divide-y rounded-lg border md:hidden">
          {webhooks.data.data.map((w) => (
            <li key={w.id}>
              <Link to={`/webhooks/${w.id}`} className="flex items-center gap-3 px-3 py-3 active:bg-muted">
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{w.name}</div>
                  <div className="truncate text-xs text-muted-foreground">
                    {providerLabel(w.provider)} · {projectName(w.project_id)}
                  </div>
                </div>
                <SecretState on={w.has_signing_secret} />
              </Link>
            </li>
          ))}
        </ul>
        <div className="hidden rounded-lg border md:block">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Provider</TableHead>
                <TableHead>Project</TableHead>
                <TableHead>Signature check</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {webhooks.data.data.map((w) => (
                <TableRow key={w.id}>
                  <TableCell>
                    <Link to={`/webhooks/${w.id}`} className="font-medium hover:underline">
                      {w.name}
                    </Link>
                  </TableCell>
                  <TableCell>{providerLabel(w.provider)}</TableCell>
                  <TableCell>{projectName(w.project_id)}</TableCell>
                  <TableCell>
                    <SecretState on={w.has_signing_secret} />
                  </TableCell>
                  <TableCell>
                    <Badge variant={w.status === 'active' ? 'secondary' : 'outline'}>{w.status}</Badge>
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">{timeAgo(w.created_at)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        </>
      )}

      <CreateWebhookDialog open={open} onOpenChange={setOpen} />
    </>
  )
}

/** Whether signature checks are on: icon + label, never color alone. */
function SecretState({ on }: { on: boolean }) {
  return on ? (
    <span className="inline-flex shrink-0 items-center gap-1 text-xs text-emerald-700 dark:text-emerald-400">
      <ShieldCheckIcon className="size-3.5" /> Verified
    </span>
  ) : (
    <span className="inline-flex shrink-0 items-center gap-1 text-xs text-amber-700 dark:text-amber-400">
      <ShieldOffIcon className="size-3.5" /> Unverified
    </span>
  )
}

function CreateWebhookDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const projects = useProjects()
  const providers = useProviders()
  const create = useCreateWebhook()
  const navigate = useNavigate()
  const [projectId, setProjectId] = useState('')
  const [provider, setProvider] = useState('razorpay')
  const [error, setError] = useState('')

  const projectOptions = (projects.data?.data ?? []).map((p) => ({ value: p.id, label: p.name }))
  const effectiveProject = projectId || projectOptions[0]?.value || ''

  async function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault()
    setError('')
    const f = new FormData(e.currentTarget)
    try {
      const w = await create.mutateAsync({
        project_id: effectiveProject,
        name: String(f.get('name')),
        provider,
        signing_secret: String(f.get('signing_secret') ?? '') || undefined,
        signature_header: provider === 'generic' ? String(f.get('signature_header') ?? '') || undefined : undefined,
      })
      toast.success('Webhook created')
      onOpenChange(false)
      navigate(`/webhooks/${w.id}`)
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New webhook</DialogTitle>
          <DialogDescription>You'll get a URL to paste into the provider's webhook settings.</DialogDescription>
        </DialogHeader>

        {projectOptions.length === 0 && !projects.isPending ? (
          <p className="text-sm">
            Create a project first.{' '}
            <Link to="/projects" className="underline" onClick={() => onOpenChange(false)}>
              Go to Projects
            </Link>
          </p>
        ) : (
          <form onSubmit={onSubmit} className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="name">Name</Label>
              <Input id="name" name="name" placeholder="Razorpay production" required autoFocus />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="project">Project</Label>
              <SimpleSelect id="project" value={effectiveProject} onChange={setProjectId} options={projectOptions} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="provider">Provider</Label>
              <SimpleSelect
                id="provider"
                value={provider}
                onChange={setProvider}
                options={(providers.data?.data ?? ['razorpay']).map((p) => ({ value: p, label: providerLabel(p) }))}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="signing_secret">Signing secret (recommended)</Label>
              <Input id="signing_secret" name="signing_secret" type="password" autoComplete="off" placeholder="From the provider's webhook settings" />
              <p className="text-xs text-muted-foreground">
                Used to verify every delivery really came from the provider. Stored encrypted; never shown again.
              </p>
            </div>
            {provider === 'generic' && (
              <div className="grid gap-2">
                <Label htmlFor="signature_header">Signature header</Label>
                <Input id="signature_header" name="signature_header" placeholder="X-Signature" />
              </div>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="submit" disabled={create.isPending || !effectiveProject}>
                {create.isPending ? 'Creating…' : 'Create webhook'}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
