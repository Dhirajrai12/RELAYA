import { PlusIcon, Trash2Icon } from 'lucide-react'
import { useState, type SubmitEvent } from 'react'
import { toast } from 'sonner'

import { EmptyState, ErrorState, PageHeader } from '@/components/common'
import { ConfirmButton } from '@/components/confirm'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { errorMessage } from '@/lib/api'
import { timeAgo } from '@/lib/format'
import { useCreateProject, useDeleteProject, useProjects, useWebhooks } from '@/lib/queries'
import { useCanManage, useHasRole } from '@/lib/role'

export function ProjectsPage() {
  const projects = useProjects()
  const webhooks = useWebhooks()
  const create = useCreateProject()
  const remove = useDeleteProject()
  const canManage = useCanManage()
  const isOwner = useHasRole('owner')
  const [name, setName] = useState('')

  async function onCreate(e: SubmitEvent) {
    e.preventDefault()
    try {
      await create.mutateAsync(name.trim())
      setName('')
      toast.success('Project created')
    } catch (err) {
      toast.error(errorMessage(err))
    }
  }

  const webhookCount = (id: string) => webhooks.data?.data.filter((w) => w.project_id === id).length ?? 0

  return (
    <>
      <PageHeader title="Projects" description="Group webhooks by product or environment, e.g. “Payments” or “Staging”." />

      {canManage && (
        <form onSubmit={onCreate} className="mb-6 flex w-full max-w-md gap-2">
          <Input placeholder="New project name" value={name} onChange={(e) => setName(e.target.value)} />
          <Button type="submit" disabled={!name.trim() || create.isPending}>
            <PlusIcon /> Create
          </Button>
        </form>
      )}

      {projects.error ? (
        <ErrorState error={projects.error} />
      ) : projects.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : projects.data.data.length === 0 ? (
        <EmptyState title="No projects yet">Create your first project above, then add webhooks to it.</EmptyState>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead className="hidden sm:table-cell">Webhooks</TableHead>
                <TableHead>Created</TableHead>
                {isOwner && <TableHead className="w-24" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {projects.data.data.map((p) => (
                <TableRow key={p.id}>
                  <TableCell className="font-medium">{p.name}</TableCell>
                  <TableCell className="hidden sm:table-cell">{webhookCount(p.id)}</TableCell>
                  <TableCell className="text-muted-foreground">{timeAgo(p.created_at)}</TableCell>
                  {isOwner && (
                    <TableCell className="text-right">
                      <ConfirmButton
                        variant="ghost"
                        destructive
                        title={`Delete “${p.name}”?`}
                        description={`Its ${webhookCount(p.id)} webhook URL(s) stop working immediately. Events already received are kept.`}
                        confirmLabel="Delete project"
                        onConfirm={() => remove.mutateAsync(p.id).then(() => toast.success('Project deleted'))}
                      >
                        <Trash2Icon />
                      </ConfirmButton>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </>
  )
}
