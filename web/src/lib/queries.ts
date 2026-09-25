import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { del, get, patch, post } from './api'
import { useAuth } from './auth'
import type {
  ApiKey,
  AuditEntry,
  EventDetail,
  EventSummary,
  List,
  Member,
  Org,
  Page,
  Project,
  Role,
  EventStats,
  User,
  Webhook,
} from './types'

/** The selected org ID. Only call inside pages rendered under RequireOrg. */
export function useOrgId(): string {
  const orgId = useAuth((s) => s.orgId)
  if (!orgId) throw new Error('no organization selected')
  return orgId
}

const orgPath = (orgId: string, rest = '') => `/orgs/${orgId}${rest}`

// ---- account ----------------------------------------------------------------

export function useMe() {
  const token = useAuth((s) => s.token)
  return useQuery({
    queryKey: ['me', token],
    queryFn: () => get<{ user: User; orgs: Org[] }>('/me'),
    enabled: !!token,
  })
}

export function useCreateOrg() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => post<Org>('/orgs', { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['me'] }),
  })
}

export function useProviders() {
  return useQuery({
    queryKey: ['providers'],
    queryFn: () => get<List<string>>('/providers'),
    staleTime: Infinity,
  })
}

// ---- projects ---------------------------------------------------------------

export function useProjects() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['projects', orgId],
    queryFn: () => get<List<Project>>(orgPath(orgId, '/projects')),
  })
}

export function useCreateProject() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => post<Project>(orgPath(orgId, '/projects'), { name }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['projects', orgId] }),
  })
}

export function useDeleteProject() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/projects/${id}`)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['projects', orgId] })
      qc.invalidateQueries({ queryKey: ['webhooks', orgId] })
    },
  })
}

// ---- webhooks ---------------------------------------------------------------

export function useWebhooks() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['webhooks', orgId],
    queryFn: () => get<List<Webhook>>(orgPath(orgId, '/webhooks')),
  })
}

export function useWebhook(id: string) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['webhooks', orgId, id],
    queryFn: () => get<Webhook>(orgPath(orgId, `/webhooks/${id}`)),
  })
}

export interface WebhookInput {
  project_id: string
  name: string
  provider: string
  signing_secret?: string
  signature_header?: string
}

export function useCreateWebhook() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: WebhookInput) => post<Webhook>(orgPath(orgId, '/webhooks'), input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks', orgId] }),
  })
}

export interface WebhookPatch {
  name?: string
  status?: 'active' | 'paused'
  signing_secret?: string
  signature_header?: string
}

export function useUpdateWebhook(id: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: WebhookPatch) => patch<Webhook>(orgPath(orgId, `/webhooks/${id}`), p),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks', orgId] }),
  })
}

export function useRotateWebhookURL(id: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => post<Webhook>(orgPath(orgId, `/webhooks/${id}/rotate-url`)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks', orgId] }),
  })
}

export function useDeleteWebhook() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/webhooks/${id}`)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['webhooks', orgId] }),
  })
}

// ---- events -----------------------------------------------------------------

export interface EventFilters {
  webhook_id?: string
  project_id?: string
  type?: string
  status?: string
  signature?: string
  dedup_key?: string
}

/** Explorer list: newest first, "load more" pagination, refreshed every few seconds. */
export function useEvents(filters: EventFilters, live: boolean) {
  const orgId = useOrgId()
  return useInfiniteQuery({
    queryKey: ['events', orgId, filters],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: '50' })
      for (const [k, v] of Object.entries(filters)) if (v) qs.set(k, v)
      if (pageParam) qs.set('cursor', pageParam)
      return get<Page<EventSummary>>(orgPath(orgId, `/events?${qs}`))
    },
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: live ? 5000 : false,
  })
}

export function useEvent(id: string | null) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['event', orgId, id],
    queryFn: () => get<EventDetail>(orgPath(orgId, `/events/${id}`)),
    enabled: !!id,
    staleTime: Infinity, // events never change after ingest
  })
}

// ---- members / keys / audit ----------------------------------------------------

export function useMembers() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['members', orgId],
    queryFn: () => get<List<Member>>(orgPath(orgId, '/members')),
  })
}

export function useAddMember() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { email: string; role: Role }) => post<Member>(orgPath(orgId, '/members'), v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['members', orgId] }),
  })
}

export function useUpdateMember() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { userId: string; role: Role }) =>
      patch<void>(orgPath(orgId, `/members/${v.userId}`), { role: v.role }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['members', orgId] }),
  })
}

export function useRemoveMember() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (userId: string) => del(orgPath(orgId, `/members/${userId}`)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['members', orgId] })
      qc.invalidateQueries({ queryKey: ['me'] })
    },
  })
}

export function useApiKeys() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['api-keys', orgId],
    queryFn: () => get<List<ApiKey>>(orgPath(orgId, '/api-keys')),
  })
}

export function useCreateApiKey() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { name: string; role: Role }) =>
      post<{ api_key: ApiKey; key: string }>(orgPath(orgId, '/api-keys'), v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys', orgId] }),
  })
}

export function useRevokeApiKey() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/api-keys/${id}`)),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys', orgId] }),
  })
}

export function useAuditLogs() {
  const orgId = useOrgId()
  return useInfiniteQuery({
    queryKey: ['audit', orgId],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      get<List<AuditEntry>>(orgPath(orgId, `/audit-logs${pageParam ? `?before=${pageParam}` : ''}`)),
    getNextPageParam: (last) => (last.data.length === 100 ? String(last.data[last.data.length - 1].id) : undefined),
  })
}

/** Overview: last 24h hourly counts and per-webhook health, refreshed every 15s. */
export function useEventStats() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['event-stats', orgId],
    queryFn: () => get<EventStats>(orgPath(orgId, '/events/stats')),
    refetchInterval: 15_000,
  })
}
