import { useEffect, useState } from 'react'
import { useAuth } from './useAuth'
import { consumeSSE } from '../sse'

interface SSEStats {
  total_instances: number
  running: number
  mcplex: number
  a2aplex: number
  llmplex: number
  skillsplex?: number
  agents: number
  delegations: number
  denials: number
  timestamp: string
}

export function useSSE(enabled = true): SSEStats | null {
  const { token } = useAuth()
  const [snapshot, setSnapshot] = useState<{ token: string; stats: SSEStats } | null>(null)

  useEffect(() => {
    if (!enabled || !token) return
    const controller = new AbortController()
    let retry: ReturnType<typeof setTimeout> | undefined

    const connect = async () => {
      try {
        const response = await fetch('/api/v1/events/stream', {
          headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
          signal: controller.signal,
        })
        // An expired/revoked session should reauthenticate, not reconnect forever.
        if (response.status === 401 || response.status === 403) return
        if (!response.ok || !response.body) throw new Error('Event stream unavailable')
        await consumeSSE(response.body, (event, data) => {
          if (event !== 'stats' || controller.signal.aborted) return
          try { setSnapshot({ token, stats: JSON.parse(data) as SSEStats }) } catch { /* ignore malformed event */ }
        })
      } catch {
        // Dashboard polling remains available while the stream reconnects.
      }
      if (!controller.signal.aborted) retry = setTimeout(() => { void connect() }, 3000)
    }
    void connect()
    return () => { controller.abort(); if (retry !== undefined) clearTimeout(retry) }
  }, [enabled, token])

  return enabled && snapshot?.token === token ? snapshot.stats : null
}
