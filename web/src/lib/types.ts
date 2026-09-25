// Mirrors the backend's JSON shapes (backend/internal/api).

export type Role = 'owner' | 'admin' | 'member'

export interface User {
  id: string
  email: string
  name: string
}

export interface Org {
  id: string
  name: string
  slug: string
  plan: string
  role?: Role
  created_at: string
}

export interface Session {
  token: string
  expires_at: string
  user: User
}

export interface Member {
  user_id: string
  email: string
  name: string
  role: Role
  created_at: string
}

export interface Project {
  id: string
  org_id: string
  name: string
  slug: string
  created_at: string
}

export interface Webhook {
  id: string
  org_id: string
  project_id: string
  name: string
  provider: string
  ingest_url: string
  has_signing_secret: boolean
  signature_header?: string
  status: 'active' | 'paused'
  created_at: string
  updated_at: string
}

export type EventStatus = 'received' | 'rejected'
export type SignatureResult = 'valid' | 'invalid' | 'missing' | 'not_configured'

export interface EventSummary {
  id: string
  project_id: string
  webhook_id: string
  dedup_key: string
  type: string
  status: EventStatus
  signature: SignatureResult
  content_type: string
  payload_size: number
  received_at: string
}

export interface EventDetail extends EventSummary {
  headers: Record<string, string>
  source_ip: string | null
  payload_json?: unknown
  payload_text?: string
  payload_base64?: string
}

export interface ApiKey {
  id: string
  name: string
  prefix: string
  role: Role
  last_used_at: string | null
  revoked_at: string | null
  created_at: string
}

export interface AuditEntry {
  id: number
  actor_type: 'user' | 'api_key' | 'system'
  actor_id: string
  action: string
  target_type: string
  target_id: string
  reason: string
  result: string
  metadata: Record<string, unknown>
  at: string
}

export interface List<T> {
  data: T[]
}

export interface Page<T> extends List<T> {
  next_cursor: string | null
}

export interface HourBucket {
  hour: string
  received: number
  rejected: number
}

export interface WebhookHealth {
  webhook_id: string
  received: number
  rejected: number
  last_received_at: string | null
}

export interface EventStats {
  hours: HourBucket[]
  totals: { received: number; rejected: number }
  webhooks: WebhookHealth[]
}
