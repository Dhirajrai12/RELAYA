import { useAuth } from './auth'

// Same origin in dev (Vite proxy) and in production (IIS serves both).
const BASE = import.meta.env.VITE_API_BASE ?? '/api/v1'

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
  const { token, signOut } = useAuth.getState()
  const headers: Record<string, string> = {}
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const res = await fetch(BASE + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })

  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    // An expired or revoked session anywhere sends the user back to login.
    if (res.status === 401 && token) signOut()
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
