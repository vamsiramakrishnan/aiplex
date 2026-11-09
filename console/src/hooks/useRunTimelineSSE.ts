import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listRunEvents, type ExecutionEvent } from '../api/client'
import { authenticatedSSE } from '../sse'
import { useAuth } from './useAuth'

let nextSession = 0
function validEvent(event: ExecutionEvent, runID: string): boolean {
  return !!event && event.run_id === runID && Number.isSafeInteger(event.seq) && event.seq >= 0 && typeof event.kind === 'string' && ['tool', 'scope', 'payload_json'].every((field) => event[field as keyof ExecutionEvent] === undefined || typeof event[field as keyof ExecutionEvent] === 'string')
}
export function mergeRunEvents(prior: ExecutionEvent[], incoming: ExecutionEvent[], runID: string): ExecutionEvent[] {
  const bySeq = new Map(prior.filter((event) => validEvent(event, runID)).map((event) => [event.seq, event]))
  for (const event of incoming) if (validEvent(event, runID) && !bySeq.has(event.seq)) bySeq.set(event.seq, event)
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq)
}

// Session generations scope cache keys without retaining bearer credentials in
// query metadata. Polling continues until valid data proves the stream is live.
export function useRunTimelineSSE(runID: string): { events: ExecutionEvent[]; live: boolean } {
  const { token } = useAuth()
  const session = useMemo(() => ++nextSession, [token])
  const owner = `${session}:${runID}`
  const cursor = useMemo(() => ({ next: 0 }), [owner])
  const [snapshot, setSnapshot] = useState<{ owner: string; events: ExecutionEvent[]; live: boolean }>({ owner, events: [], live: false })
  const enabled = !!token && !!runID
  const current = snapshot.owner === owner && enabled ? snapshot : { owner, events: [], live: false }

  useEffect(() => {
    setSnapshot({ owner, events: [], live: false })
    if (!token || !runID) return
    const controller = new AbortController()
    let retry: ReturnType<typeof setTimeout> | undefined
    const connect = async () => {
      let denied = false
      try {
        const outcome = await authenticatedSSE(`/events/stream?run_id=${encodeURIComponent(runID)}`, token, controller.signal, (kind, data) => {
          if (controller.signal.aborted) return
          try {
            const value = JSON.parse(data)
            if (kind === 'run_event' && validEvent(value, runID)) {
              cursor.next = Math.max(cursor.next, value.seq + 1)
              setSnapshot((prior) => ({ owner, events: mergeRunEvents(prior.owner === owner ? prior.events : [], [value], runID), live: true }))
            } else if (kind === 'run_summary' && value?.run_id === runID) {
              setSnapshot((prior) => ({ owner, events: prior.owner === owner ? prior.events : [], live: true }))
            }
          } catch { /* malformed or foreign frames do not establish liveness */ }
        })
        denied = outcome === 'denied'
      } catch { /* polling supplies data while the stream is unavailable */ }
      if (controller.signal.aborted) return
      setSnapshot((prior) => prior.owner === owner ? { ...prior, live: false } : prior)
      if (!denied) retry = setTimeout(() => { void connect() }, 3000)
    }
    void connect()
    return () => { controller.abort(); if (retry !== undefined) clearTimeout(retry) }
  }, [owner, token, runID, cursor])

  const poll = useQuery({
    queryKey: ['run-events-fallback', session, runID],
    queryFn: async () => ({ owner, events: (await listRunEvents(runID, cursor.next)).events }),
    enabled: enabled && !current.live,
    refetchInterval: current.live ? false : 3000,
    staleTime: 0,
    retry: false,
  })
  useEffect(() => {
    if (!enabled || poll.data?.owner !== owner || !Array.isArray(poll.data.events)) return
    const events = poll.data.events.filter((event) => validEvent(event, runID))
    for (const event of events) cursor.next = Math.max(cursor.next, event.seq + 1)
    setSnapshot((prior) => ({ owner, events: mergeRunEvents(prior.owner === owner ? prior.events : [], events, runID), live: prior.owner === owner && prior.live }))
  }, [enabled, poll.data, owner, runID, cursor])
  return { events: current.events, live: current.live }
}
