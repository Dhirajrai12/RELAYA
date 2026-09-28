import { useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useEffect } from 'react'
import { create } from 'zustand'

import { useAuth } from './auth'

// Realtime: a WebSocket that tells us *what changed*; we then refetch just
// those queries. While connected, polling is switched off (see useLiveInterval).

type Status = 'connecting' | 'live' | 'offline'

export const useRealtimeStatus = create<{ status: Status; set: (s: Status) => void }>((set) => ({
  status: 'connecting',
  set: (status) => set({ status }),
}))

/** Poll every `ms` only while the realtime stream is down. */
export function useLiveInterval(ms: number): number | false {
  const status = useRealtimeStatus((s) => s.status)
  return status === 'live' ? false : ms
}

interface Message {
  type: 'ready' | 'resync' | 'event' | 'delivery' | 'change' | 'contract'
  event_id?: string
  delivery_id?: string
  action?: string
  target_id?: string
}

/** Which cached queries a message makes stale. */
function keysFor(orgId: string, m: Message): QueryKey[] {
  switch (m.type) {
    case 'event':
      return [['events', orgId], ['event-stats', orgId]]
    case 'delivery':
      return [
        ['events', orgId],
        ['event-stats', orgId],
        ['destinations', orgId],
        ['incidents', orgId], // replay progress
        ['repair-rules', orgId], // applied counts
        ...(m.event_id ? [['event', orgId, m.event_id]] : []),
        ...(m.delivery_id ? [['delivery', orgId, m.delivery_id]] : []),
      ]
    case 'contract':
      // An event was checked: its status, the contract's counts and maybe an incident changed.
      return [
        ['events', orgId],
        ['contracts', orgId],
        ['incidents', orgId],
        ['event-stats', orgId],
        ...(m.event_id ? [['event', orgId, m.event_id]] : []),
        ...(m.target_id ? [['contract', orgId, m.target_id]] : []),
      ]
    case 'change': {
      const keys: QueryKey[] = [['audit', orgId]]
      const area = (m.action ?? '').split('.')[0]
      const map: Record<string, QueryKey[]> = {
        webhook: [['webhooks', orgId], ['event-stats', orgId]],
        project: [['projects', orgId], ['webhooks', orgId]],
        destination: [['destinations', orgId]],
        member: [['members', orgId], ['me']],
        api_key: [['api-keys', orgId]],
        org: [['me']],
        delivery: [['event', orgId], ['delivery', orgId], ['events', orgId]],
        contract: [['contracts', orgId], ['contract', orgId], ['incidents', orgId], ['event-stats', orgId]],
        incident: [['incidents', orgId], ['contracts', orgId], ['contract', orgId], ['event-stats', orgId]],
        replay: [['incidents', orgId], ['event-stats', orgId], ['events', orgId]],
        alert_channel: [['alert-channels', orgId], ['alerts', orgId]],
        alert: [['alerts', orgId], ['alert-channels', orgId]],
        repair_rule: [['repair-rules', orgId], ['incidents', orgId]],
        integration: [['integrations', orgId], ['connections', orgId]],
        connection: [['connections', orgId], ['integrations', orgId], ['syncs', orgId]],
        sync: [['syncs', orgId]],
        outbound: [['outbound-apps', orgId], ['outbound-app', orgId], ['outbound-event-types', orgId]],
      }
      return keys.concat(map[area] ?? [])
    }
    default:
      return []
  }
}

/**
 * Keeps one WebSocket open for the selected org and invalidates affected
 * queries as changes arrive. Bursts (e.g. 100 webhooks at once) are coalesced
 * into one refetch per query every 100 ms. Reconnects with backoff.
 */
export function useRealtime(orgId: string | null) {
  const qc = useQueryClient()
  const token = useAuth((s) => s.token)
  const setStatus = useRealtimeStatus((s) => s.set)

  useEffect(() => {
    if (!orgId || !token) return
    let ws: WebSocket | null = null
    let retry = 0
    let stopped = false
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let flushTimer: ReturnType<typeof setTimeout> | undefined
    const pending = new Map<string, QueryKey>()

    const flush = () => {
      flushTimer = undefined
      for (const key of pending.values()) qc.invalidateQueries({ queryKey: key })
      pending.clear()
    }
    const queue = (keys: QueryKey[]) => {
      for (const k of keys) pending.set(JSON.stringify(k), k)
      flushTimer ??= setTimeout(flush, 100)
    }
    const refetchAll = () => qc.invalidateQueries({ predicate: (q) => q.queryKey[1] === orgId || q.queryKey[0] === 'me' })

    const connect = () => {
      setStatus('connecting')
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      const base = (import.meta.env.VITE_API_BASE ?? '/api/v1').replace(/\/$/, '')
      ws = new WebSocket(`${proto}://${location.host}${base}/orgs/${orgId}/stream`)
      ws.onopen = () => ws?.send(JSON.stringify({ type: 'auth', token }))
      ws.onmessage = (ev) => {
        let m: Message
        try {
          m = JSON.parse(ev.data)
        } catch {
          return
        }
        if (m.type === 'ready') {
          const wasReconnect = retry > 0
          retry = 0
          setStatus('live')
          if (wasReconnect) refetchAll() // we may have missed changes while offline
        } else if (m.type === 'resync') {
          refetchAll()
        } else {
          queue(keysFor(orgId, m))
        }
      }
      ws.onclose = () => {
        ws = null
        if (stopped) return
        setStatus('offline')
        retry++
        const delay = Math.min(30_000, 1000 * 2 ** Math.min(retry - 1, 5)) * (0.8 + Math.random() * 0.4)
        reconnectTimer = setTimeout(connect, delay)
      }
    }

    connect()
    // Reconnect right away when the tab comes back or the network returns.
    const wake = () => {
      if (!ws && !stopped && document.visibilityState === 'visible') {
        clearTimeout(reconnectTimer)
        connect()
      }
    }
    window.addEventListener('online', wake)
    document.addEventListener('visibilitychange', wake)

    return () => {
      stopped = true
      clearTimeout(reconnectTimer)
      clearTimeout(flushTimer)
      window.removeEventListener('online', wake)
      document.removeEventListener('visibilitychange', wake)
      ws?.close()
      setStatus('connecting')
    }
  }, [orgId, token, qc, setStatus])
}
