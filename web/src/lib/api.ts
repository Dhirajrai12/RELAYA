import { useAuth } from './auth'

// Same origin in dev (Vite proxy) and in production (IIS serves both).
const BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'

// The address customers call from their own code (SDKs, curl), without /v1. On the hosted
// dashboard that's the API's own domain; elsewhere the dashboard's origin serves it under /api.
export const PUBLIC_API = location.origin === 'https://relaya.sbs' ? 'https://api.relaya.sbs' : `${location.origin}/api`

export class ApiError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const { signOut } = useAuth.getState()
  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  // Session token is in an httpOnly cookie, automatically sent by the browser.
  const res = await fetch(BASE + path, {
    method,
    headers,
    credentials: 'include', // ensure cookies are sent
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    // An expired or revoked session sends the user back to login.
    if (res.status === 401) signOut()
    const err = data?.error
    throw new ApiError(res.status, err?.code ?? 'error', err?.message ?? `Request failed (${res.status})`)
  }
  return data as T
}

export const get = <T>(path: string) => api<T>('GET', path)
export const post = <T>(path: string, body?: unknown) => api<T>('POST', path, body ?? {})
export const patch = <T>(path: string, body: unknown) => api<T>('PATCH', path, body)
export const del = (path: string) => api<void>('DELETE', path)

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : 'Something went wrong'
}
