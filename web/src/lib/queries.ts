import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type QueryClient, type QueryKey } from '@tanstack/react-query'

import { del, get, patch, post } from './api'
import { useAuth } from './auth'
import { useLiveInterval } from './realtime'
import type {
  SimulateResult,
  SimulationSample,
  RepairOp,
  RepairPreview,
  RepairRule,
  RepairSuggestion,
  AlertChannel,
  AlertLogEntry,
  AlertSettings,
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
  Contract,
  ContractDetail,
  Incident,
  Replay,
  ReplayPlan,
  Delivery,
  DeliveryAttempt,
  Destination,
  TestDeliveryResult,
  User,
  Webhook,
  Connection,
  ConnectProviders,
  Finding,
  Integration,
  ProxyCall,
  Sync,
  SyncModel,
  SyncRun,
  EndpointTestResult,
  OutboundApp,
  OutboundEndpoint,
  OutboundEventType,
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

// ---- instant cache updates ------------------------------------------------------
// Mutations write the server's response straight into the cache so screens
// update without waiting for another round trip. Realtime messages (and the
// background refetch they trigger) then confirm the state.

type WithId = { id: string }

function upsertInList<T extends WithId>(qc: QueryClient, key: QueryKey, item: T) {
  qc.setQueryData<List<T>>(key, (old) =>
    old ? { ...old, data: old.data.some((x) => x.id === item.id) ? old.data.map((x) => (x.id === item.id ? item : x)) : [...old.data, item] } : old,
  )
}

function removeFromList<T extends WithId>(qc: QueryClient, key: QueryKey, id: string) {
  qc.setQueryData<List<T>>(key, (old) => (old ? { ...old, data: old.data.filter((x) => x.id !== id) } : old))
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
    onSuccess: (p) => upsertInList(qc, ['projects', orgId], p),
  })
}

export function useDeleteProject() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/projects/${id}`)),
    onSuccess: (_, id) => {
      removeFromList(qc, ['projects', orgId], id)
      qc.setQueryData<List<Webhook>>(['webhooks', orgId], (old) => (old ? { ...old, data: old.data.filter((w) => w.project_id !== id) } : old))
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
    onSuccess: (w) => {
      // Seed the detail page too, so navigating to it renders immediately.
      qc.setQueryData(['webhooks', orgId, w.id], w)
      qc.setQueryData<List<Destination>>(['destinations', orgId, w.id], { data: [] })
      upsertInList(qc, ['webhooks', orgId], w)
    },
  })
}

/** Warm the cache on hover so opening a webhook feels instant. */
export function usePrefetchWebhook() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return (id: string) => {
    qc.prefetchQuery({ queryKey: ['webhooks', orgId, id], queryFn: () => get<Webhook>(orgPath(orgId, `/webhooks/${id}`)), staleTime: 30_000 })
    qc.prefetchQuery({
      queryKey: ['destinations', orgId, id],
      queryFn: () => get<List<Destination>>(orgPath(orgId, `/webhooks/${id}/destinations`)),
      staleTime: 30_000,
    })
  }
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
    onSuccess: (w) => {
      qc.setQueryData(['webhooks', orgId, w.id], w)
      upsertInList(qc, ['webhooks', orgId], w)
    },
  })
}

/** Sample events for a webhook's provider, with fresh IDs on each fetch. */
export function useSimulationSamples(webhookId: string) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['sim-samples', orgId, webhookId],
    queryFn: () => get<{ provider: string; signed: boolean; data: SimulationSample[] }>(orgPath(orgId, `/webhooks/${webhookId}/samples`)),
    staleTime: Infinity,
  })
}

/** Signs a sample like the provider would and runs it through ingest. */
export function useSimulateEvent(webhookId: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { event_type: string; payload: string }) => post<SimulateResult>(orgPath(orgId, `/webhooks/${webhookId}/simulate`), v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['events', orgId] })
      qc.invalidateQueries({ queryKey: ['event-stats', orgId] })
    },
  })
}

export function useRotateWebhookURL(id: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => post<Webhook>(orgPath(orgId, `/webhooks/${id}/rotate-url`)),
    onSuccess: (w) => {
      qc.setQueryData(['webhooks', orgId, w.id], w)
      upsertInList(qc, ['webhooks', orgId], w)
    },
  })
}

export function useDeleteWebhook() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/webhooks/${id}`)),
    onSuccess: (_, id) => {
      removeFromList(qc, ['webhooks', orgId], id)
      qc.removeQueries({ queryKey: ['webhooks', orgId, id] })
    },
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
  contract_status?: string
}

/** Explorer list: newest first, fetched 100 at a time (5 pages of 20). Pushed live; polls only if the stream is down. */
export function useEvents(filters: EventFilters) {
  const orgId = useOrgId()
  const interval = useLiveInterval(5000)
  return useInfiniteQuery({
    queryKey: ['events', orgId, filters],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const qs = new URLSearchParams({ limit: '100' })
      for (const [k, v] of Object.entries(filters)) if (v) qs.set(k, v)
      if (pageParam) qs.set('cursor', pageParam)
      return get<Page<EventSummary>>(orgPath(orgId, `/events?${qs}`))
    },
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: interval,
  })
}

/** The newest few events, for the overview. Shares the 'events' key so live updates refresh it. */
export function useRecentEvents(limit = 6) {
  const orgId = useOrgId()
  const interval = useLiveInterval(10_000)
  return useQuery({
    queryKey: ['events', orgId, 'recent', limit],
    queryFn: () => get<Page<EventSummary>>(orgPath(orgId, `/events?limit=${limit}`)),
    refetchInterval: interval,
  })
}

export function useEvent(id: string | null) {
  const orgId = useOrgId()
  const interval = useLiveInterval(3000)
  return useQuery({
    queryKey: ['event', orgId, id],
    queryFn: () => get<EventDetail>(orgPath(orgId, `/events/${id}`)),
    enabled: !!id,
    // The event never changes, but its deliveries do: poll while any is still in progress.
    refetchInterval: (q) =>
      interval && q.state.data?.deliveries.some((d) => !['succeeded', 'failed'].includes(d.status)) ? interval : false,
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
  const interval = useLiveInterval(15_000)
  return useQuery({
    queryKey: ['event-stats', orgId],
    queryFn: () => get<EventStats>(orgPath(orgId, '/events/stats')),
    refetchInterval: interval,
  })
}

// ---- contracts & incidents -----------------------------------------------------------

export function useContracts(webhookId?: string) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['contracts', orgId, webhookId ?? 'all'],
    queryFn: () => get<List<Contract>>(orgPath(orgId, `/contracts${webhookId ? `?webhook_id=${webhookId}` : ''}`)),
  })
}

export function useContract(id: string) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['contract', orgId, id],
    queryFn: () => get<ContractDetail>(orgPath(orgId, `/contracts/${id}`)),
    enabled: !!id,
  })
}

/** All of a contract's findings, newest first, 100 per request. */
export function useContractFindings(id: string) {
  const orgId = useOrgId()
  return useInfiniteQuery({
    queryKey: ['contract', orgId, id, 'findings'],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      get<{ data: Finding[]; next_before: string | null }>(orgPath(orgId, `/contracts/${id}/findings${pageParam ? `?before=${pageParam}` : ''}`)),
    getNextPageParam: (last) => last.next_before ?? undefined,
  })
}

export function useCreateContractVersion(id: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { critical_fields: string[]; source: 'observed' | 'active' }) =>
      post<{ version: number }>(orgPath(orgId, `/contracts/${id}/versions`), v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['contract', orgId, id] })
      qc.invalidateQueries({ queryKey: ['contracts', orgId] })
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
    },
  })
}

export function useRelearnContract(id: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => post<void>(orgPath(orgId, `/contracts/${id}/relearn`)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['contract', orgId, id] })
      qc.invalidateQueries({ queryKey: ['contracts', orgId] })
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
    },
  })
}

export function useIncidents(status: 'open' | 'resolved' = 'open') {
  const orgId = useOrgId()
  const interval = useLiveInterval(15_000)
  return useQuery({
    queryKey: ['incidents', orgId, status],
    queryFn: () => get<List<Incident> & { auto_resolve_after_seconds: number }>(orgPath(orgId, `/incidents?status=${status}`)),
    refetchInterval: interval,
  })
}

/** Dry run: what a replay would send. Only fetched while the dialog is open. */
export function useReplayPreview(incidentId: string | null) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['replay-preview', orgId, incidentId],
    queryFn: () => get<ReplayPlan>(orgPath(orgId, `/incidents/${incidentId}/replay`)),
    enabled: !!incidentId,
    staleTime: 0,
  })
}

export function useStartReplay() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (incidentId: string) => post<Replay>(orgPath(orgId, `/incidents/${incidentId}/replay`), { confirm: true }),
    onSuccess: (replay, incidentId) => {
      // Show progress immediately; realtime delivery messages keep it moving.
      qc.setQueryData<List<Incident>>(['incidents', orgId, 'open'], (old) =>
        old ? { ...old, data: old.data.map((i) => (i.id === incidentId ? { ...i, replay } : i)) } : old,
      )
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
    },
  })
}

export function useResolveIncident() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: string; resolution: string }) =>
      post<void>(orgPath(orgId, `/incidents/${v.id}/resolve`), { resolution: v.resolution }),
    onSuccess: (_, v) => {
      removeFromList(qc, ['incidents', orgId, 'open'], v.id)
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
      qc.invalidateQueries({ queryKey: ['contracts', orgId] })
    },
  })
}

// ---- destinations & deliveries ----------------------------------------------------

export function useDestinations(webhookId: string) {
  const orgId = useOrgId()
  const interval = useLiveInterval(10_000)
  return useQuery({
    queryKey: ['destinations', orgId, webhookId],
    queryFn: () => get<List<Destination>>(orgPath(orgId, `/webhooks/${webhookId}/destinations`)),
    refetchInterval: interval,
  })
}

export interface DestinationInput {
  name?: string
  url?: string
  enabled?: boolean
  max_attempts?: number
  timeout_ms?: number
  event_types?: string[]
}

export function useCreateDestination(webhookId: string) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: DestinationInput) =>
      post<{ destination: Destination; signing_secret: string }>(orgPath(orgId, `/webhooks/${webhookId}/destinations`), v),
    onSuccess: (r) => upsertInList(qc, ['destinations', orgId, webhookId], r.destination),
  })
}

export function useUpdateDestination() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: DestinationInput & { id: string }) => patch<Destination>(orgPath(orgId, `/destinations/${id}`), v),
    onSuccess: (d) => upsertInList(qc, ['destinations', orgId, d.webhook_id], d),
  })
}

export function useDeleteDestination() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/destinations/${id}`)),
    onSuccess: (_, id) => {
      for (const [key] of qc.getQueriesData<List<Destination>>({ queryKey: ['destinations', orgId] })) removeFromList(qc, key, id)
    },
  })
}

export function useRotateDestinationSecret() {
  const orgId = useOrgId()
  return useMutation({
    mutationFn: (id: string) => post<{ signing_secret: string }>(orgPath(orgId, `/destinations/${id}/rotate-secret`)),
  })
}

export function useTestDestination() {
  const orgId = useOrgId()
  return useMutation({
    mutationFn: (id: string) => post<TestDeliveryResult>(orgPath(orgId, `/destinations/${id}/test`)),
  })
}

export function useDeliveryAttempts(id: string | null) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['delivery', orgId, id],
    queryFn: () => get<{ delivery: Delivery; attempts: DeliveryAttempt[] }>(orgPath(orgId, `/deliveries/${id}`)),
    enabled: !!id,
  })
}

export function useRetryDelivery() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => post<Delivery>(orgPath(orgId, `/deliveries/${id}/retry`)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['event', orgId] })
      qc.invalidateQueries({ queryKey: ['delivery', orgId] })
      qc.invalidateQueries({ queryKey: ['events', orgId] })
    },
  })
}


// ---- alerts -------------------------------------------------------------------------

export function useAlertSettings() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['alert-settings', orgId],
    queryFn: () => get<AlertSettings>(orgPath(orgId, '/alert-settings')),
    staleTime: 5 * 60_000,
  })
}

export function useAlertChannels() {
  const orgId = useOrgId()
  const interval = useLiveInterval(30_000)
  return useQuery({
    queryKey: ['alert-channels', orgId],
    queryFn: () => get<List<AlertChannel>>(orgPath(orgId, '/alert-channels')),
    refetchInterval: interval,
  })
}

export function useAlertLog() {
  const orgId = useOrgId()
  const interval = useLiveInterval(15_000)
  return useQuery({
    queryKey: ['alerts', orgId],
    queryFn: () => get<List<AlertLogEntry>>(orgPath(orgId, '/alerts')),
    refetchInterval: interval,
  })
}

export interface AlertChannelInput {
  type: AlertChannel['type']
  name: string
  url?: string
  email?: string
  events: AlertChannel['events']
  jira?: { site: string; email: string; api_token: string; project: string; issue_type?: string }
}

export function useCreateAlertChannel() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: AlertChannelInput) =>
      post<{ channel: AlertChannel; signing_secret?: string }>(orgPath(orgId, '/alert-channels'), v),
    onSuccess: (r) => upsertInList(qc, ['alert-channels', orgId], r.channel),
  })
}

export function useUpdateAlertChannel() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; name?: string; events?: AlertChannel['events']; enabled?: boolean }) =>
      patch<AlertChannel>(orgPath(orgId, `/alert-channels/${id}`), v),
    onSuccess: (c) => upsertInList(qc, ['alert-channels', orgId], c),
  })
}

export function useDeleteAlertChannel() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/alert-channels/${id}`)),
    onSuccess: (_, id) => {
      removeFromList(qc, ['alert-channels', orgId], id)
      qc.invalidateQueries({ queryKey: ['alerts', orgId] })
    },
  })
}

export function useTestAlertChannel() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => post<{ ok: boolean; error: string }>(orgPath(orgId, `/alert-channels/${id}/test`)),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ['alerts', orgId] })
      qc.invalidateQueries({ queryKey: ['alert-channels', orgId] })
    },
  })
}

// ---- repair rules ------------------------------------------------------------------

export function useRepairRules(webhookId?: string) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['repair-rules', orgId, webhookId ?? 'all'],
    queryFn: () => get<List<RepairRule>>(orgPath(orgId, '/repair-rules' + (webhookId ? `?webhook_id=${webhookId}` : ''))),
  })
}

export interface RepairRuleInput {
  webhook_id?: string
  event_type?: string
  name?: string
  ops?: RepairOp[]
  enabled?: boolean
  incident_id?: string
}

export function useCreateRepairRule() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: RepairRuleInput) => post<RepairRule>(orgPath(orgId, '/repair-rules'), v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['repair-rules', orgId] })
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
    },
  })
}

export function useUpdateRepairRule() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: RepairRuleInput & { id: string }) => patch<RepairRule>(orgPath(orgId, `/repair-rules/${id}`), v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['repair-rules', orgId] })
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
    },
  })
}

export function useDeleteRepairRule() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/repair-rules/${id}`)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['repair-rules', orgId] })
      qc.invalidateQueries({ queryKey: ['incidents', orgId] })
    },
  })
}

export function usePreviewRepair() {
  const orgId = useOrgId()
  return useMutation({
    mutationFn: (v: { webhook_id: string; event_type: string; ops: RepairOp[]; event_id?: string; rule_id?: string }) =>
      post<RepairPreview>(orgPath(orgId, '/repair-rules/preview'), v),
  })
}

export function useRepairSuggestion(incidentId: string | null) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['repair-suggestion', orgId, incidentId],
    queryFn: () => get<RepairSuggestion>(orgPath(orgId, `/incidents/${incidentId}/repair-suggestion`)),
    enabled: !!incidentId,
    retry: false,
    staleTime: 60_000,
  })
}

// ---- connections ----------------------------------------------------------------

export function useConnectProviders() {
  return useQuery({
    queryKey: ['connect-providers'],
    queryFn: () => get<ConnectProviders>('/connect/providers'),
    staleTime: Infinity,
  })
}

export function useIntegrations() {
  const orgId = useOrgId()
  const interval = useLiveInterval(30_000)
  return useQuery({
    queryKey: ['integrations', orgId],
    queryFn: () => get<List<Integration>>(orgPath(orgId, '/integrations')),
    refetchInterval: interval,
  })
}

export interface IntegrationInput {
  provider: string
  key?: string
  name?: string
  client_id?: string
  client_secret?: string
  scopes?: string[]
}

export function useCreateIntegration() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: IntegrationInput) => post<Integration>(orgPath(orgId, '/integrations'), v),
    onSuccess: (r) => upsertInList(qc, ['integrations', orgId], r),
  })
}

export function useUpdateIntegration() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; name?: string; client_id?: string; client_secret?: string; scopes?: string[] }) =>
      patch<Integration>(orgPath(orgId, `/integrations/${id}`), v),
    onSuccess: (r) => upsertInList(qc, ['integrations', orgId], r),
  })
}

export function useDeleteIntegration() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/integrations/${id}`)),
    onSuccess: (_, id) => {
      removeFromList(qc, ['integrations', orgId], id)
      qc.invalidateQueries({ queryKey: ['connections', orgId] })
    },
  })
}

export function useConnections() {
  const orgId = useOrgId()
  const interval = useLiveInterval(30_000)
  return useQuery({
    queryKey: ['connections', orgId],
    queryFn: () => get<List<Connection>>(orgPath(orgId, '/connections')),
    refetchInterval: interval,
  })
}

export function useDeleteConnection() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/connections/${id}`)),
    onSuccess: (_, id) => {
      removeFromList(qc, ['connections', orgId], id)
      qc.invalidateQueries({ queryKey: ['integrations', orgId] })
    },
  })
}

export function useRefreshConnection() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      post<{ connection: Connection; refreshed: boolean; error?: string }>(orgPath(orgId, `/connections/${id}/refresh`)),
    onSuccess: (r) => {
      upsertInList(qc, ['connections', orgId], r.connection)
      qc.invalidateQueries({ queryKey: ['integrations', orgId] })
    },
  })
}

export function useCreateConnectSession() {
  const orgId = useOrgId()
  return useMutation({
    mutationFn: (v: { integration: string; end_user_id: string; return_url?: string }) =>
      post<{ id: string; url: string; expires_at: string }>(orgPath(orgId, '/connect-sessions'), v),
  })
}

export function useProxyCalls() {
  const orgId = useOrgId()
  const interval = useLiveInterval(15_000)
  return useQuery({
    queryKey: ['proxy-calls', orgId],
    queryFn: () => get<List<ProxyCall>>(orgPath(orgId, '/proxy-calls')),
    refetchInterval: interval,
  })
}

// ---- syncs ----------------------------------------------------------------------

export function useSyncModels() {
  return useQuery({
    queryKey: ['sync-models'],
    queryFn: () => get<List<SyncModel>>('/connect/sync-models'),
    staleTime: Infinity,
  })
}

export function useSyncs() {
  const orgId = useOrgId()
  // Runs happen in the worker without an audited change, so poll while the page is open.
  return useQuery({
    queryKey: ['syncs', orgId],
    queryFn: () => get<List<Sync>>(orgPath(orgId, '/syncs')),
    refetchInterval: 10_000,
  })
}

export function useSyncRuns(id: string) {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['sync-runs', orgId, id],
    queryFn: () => get<List<SyncRun>>(orgPath(orgId, `/syncs/${id}/runs`)),
    refetchInterval: 10_000,
  })
}

export interface SyncInput {
  connection_id: string
  model: string
  config: Record<string, string>
  interval_minutes: number
  webhook_id?: string
  emit_existing: boolean
}

export function useCreateSync() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: SyncInput) => post<Sync>(orgPath(orgId, '/syncs'), v),
    onSuccess: (r) => {
      upsertInList(qc, ['syncs', orgId], r)
      qc.invalidateQueries({ queryKey: ['webhooks', orgId] })
    },
  })
}

export function useUpdateSync() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...v }: { id: string; enabled?: boolean; interval_minutes?: number; config?: Record<string, string> }) =>
      patch<Sync>(orgPath(orgId, `/syncs/${id}`), v),
    onSuccess: (r) => upsertInList(qc, ['syncs', orgId], r),
  })
}

export function useRunSync() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => post<Sync>(orgPath(orgId, `/syncs/${id}/run`)),
    onSuccess: (r) => {
      upsertInList(qc, ['syncs', orgId], r)
      // The worker picks it up within seconds.
      setTimeout(() => qc.invalidateQueries({ queryKey: ['syncs', orgId] }), 3000)
      setTimeout(() => qc.invalidateQueries({ queryKey: ['sync-runs', orgId, r.id] }), 3000)
    },
  })
}

export function useDeleteSync() {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => del(orgPath(orgId, `/syncs/${id}`)),
    onSuccess: (_, id) => removeFromList(qc, ['syncs', orgId], id),
  })
}

// ---- outbound webhooks ------------------------------------------------------------

export function useOutboundApps() {
  const orgId = useOrgId()
  const interval = useLiveInterval(30_000)
  return useQuery({
    queryKey: ['outbound-apps', orgId],
    queryFn: () => get<List<OutboundApp>>(orgPath(orgId, '/outbound/apps')),
    refetchInterval: interval,
  })
}

export function useOutboundApp(ref: string) {
  const orgId = useOrgId()
  const interval = useLiveInterval(15_000)
  return useQuery({
    queryKey: ['outbound-app', orgId, ref],
    queryFn: () => get<{ app: OutboundApp; endpoints: OutboundEndpoint[] }>(orgPath(orgId, `/outbound/apps/${encodeURIComponent(ref)}`)),
    refetchInterval: interval,
  })
}

/** Mutations on outbound apps; each refreshes the app list and app detail. */
function useOutboundMutation<V, R>(fn: (orgId: string, v: V) => Promise<R>) {
  const orgId = useOrgId()
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: V) => fn(orgId, v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['outbound-apps', orgId] })
      qc.invalidateQueries({ queryKey: ['outbound-app', orgId] })
      qc.invalidateQueries({ queryKey: ['outbound-event-types', orgId] })
    },
  })
}

export const useCreateOutboundApp = () =>
  useOutboundMutation((orgId, v: { uid: string; name?: string }) => post<OutboundApp>(orgPath(orgId, '/outbound/apps'), v))

export const useDeleteOutboundApp = () =>
  useOutboundMutation((orgId, ref: string) => del(orgPath(orgId, `/outbound/apps/${encodeURIComponent(ref)}`)))

export interface EndpointInput {
  url?: string
  description?: string
  event_types?: string[]
  enabled?: boolean
}

export const useCreateOutboundEndpoint = (app: string) =>
  useOutboundMutation((orgId, v: EndpointInput) =>
    post<{ endpoint: OutboundEndpoint; signing_secret: string }>(orgPath(orgId, `/outbound/apps/${encodeURIComponent(app)}/endpoints`), v),
  )

export const useUpdateOutboundEndpoint = (app: string) =>
  useOutboundMutation((orgId, { id, ...v }: EndpointInput & { id: string }) =>
    patch<OutboundEndpoint>(orgPath(orgId, `/outbound/apps/${encodeURIComponent(app)}/endpoints/${id}`), v),
  )

export const useDeleteOutboundEndpoint = (app: string) =>
  useOutboundMutation((orgId, id: string) => del(orgPath(orgId, `/outbound/apps/${encodeURIComponent(app)}/endpoints/${id}`)))

export const useTestOutboundEndpoint = (app: string) =>
  useOutboundMutation((orgId, id: string) =>
    post<EndpointTestResult>(orgPath(orgId, `/outbound/apps/${encodeURIComponent(app)}/endpoints/${id}/test`)),
  )

export function fetchOutboundEndpointSecret(orgId: string, app: string, id: string) {
  return get<{ signing_secret: string }>(orgPath(orgId, `/outbound/apps/${encodeURIComponent(app)}/endpoints/${id}/secret`))
}

export const useCreatePortalLink = (app: string) =>
  useOutboundMutation((orgId, _: void) =>
    post<{ url: string; expires_at: string }>(orgPath(orgId, `/outbound/apps/${encodeURIComponent(app)}/portal-link`)),
  )

export const useSendOutboundMessage = () =>
  useOutboundMutation((orgId, v: { app: string; event_type: string; payload: unknown; idempotency_key?: string }) =>
    post<{ id: string; app: string; event_type: string; endpoints: number; duplicate: boolean }>(orgPath(orgId, '/outbound/messages'), v),
  )

export function useOutboundEventTypes() {
  const orgId = useOrgId()
  return useQuery({
    queryKey: ['outbound-event-types', orgId],
    queryFn: () => get<List<OutboundEventType>>(orgPath(orgId, '/outbound/event-types')),
  })
}

export const useSaveOutboundEventType = () =>
  useOutboundMutation((orgId, v: { name: string; description?: string }) => post<OutboundEventType>(orgPath(orgId, '/outbound/event-types'), v))

export const useDeleteOutboundEventType = () =>
  useOutboundMutation((orgId, name: string) => del(orgPath(orgId, `/outbound/event-types/${encodeURIComponent(name)}`)))
