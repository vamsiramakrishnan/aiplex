import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useRunTimelineSSE, mergeRunEvents } from '../hooks/useRunTimelineSSE'
import { listRunEvents, type ExecutionEvent } from '../api/client'

let token: string | null = 'private-session-one'
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ token }) }))
vi.mock('../api/client', () => ({ listRunEvents: vi.fn() }))
const originalFetch = global.fetch
const fetchMock = vi.fn()
function event(run_id: string, seq: number): ExecutionEvent { return { run_id, seq, kind: 'decision.recorded', tenant_id: 'tenant', agent_id: 'agent', plane: 'a2aplex', actor: 'actor', subject: 'subject', timestamp: new Date().toISOString() } }
function stream() {
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const cancel = vi.fn()
  const body = new ReadableStream<Uint8Array>({ start(value) { controller = value }, cancel })
  return { body, cancel, send(value: ExecutionEvent) { controller.enqueue(new TextEncoder().encode(`event: run_event\ndata: ${JSON.stringify(value)}\n\n`)) } }
}
function response(body: ReadableStream<Uint8Array>) { return { ok: true, status: 200, headers: new Headers({ 'Content-Type': 'text/event-stream' }), body } }
function show(runID: string) {
  const cache = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={cache}>{children}</QueryClientProvider>
  return { ...renderHook(({ id }) => useRunTimelineSSE(id), { initialProps: { id: runID }, wrapper }), cache }
}
beforeEach(() => { token = 'private-session-one'; global.fetch = fetchMock; fetchMock.mockReset(); vi.mocked(listRunEvents).mockReset(); vi.mocked(listRunEvents).mockResolvedValue({ events: [] }) })
afterEach(() => { global.fetch = originalFetch; vi.useRealTimers() })

describe('Authenticated run timeline', () => {
  it('uses header credentials and unions stream/poll sequence identities in order', async () => {
    const source = stream(); fetchMock.mockResolvedValue(response(source.body))
    let complete!: (value: { events: ExecutionEvent[] }) => void
    vi.mocked(listRunEvents).mockImplementation(() => new Promise((resolve) => { complete = resolve }))
    const hook = show('run/one')
    await waitFor(() => expect(listRunEvents).toHaveBeenCalled())
    await act(async () => { source.send(event('run/one', 3)); source.send(event('run/one', 1)); source.send(event('run/one', 3)); source.send(event('foreign', 99)) })
    await waitFor(() => expect(hook.result.current.live).toBe(true))
    await act(async () => complete({ events: [event('run/one', 2)] }))
    await waitFor(() => expect(hook.result.current.events.map((value) => value.seq)).toEqual([1, 2, 3]))
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/events/stream?run_id=run%2Fone'); expect(url).not.toContain('token='); expect(init.headers.Authorization).toBe('Bearer private-session-one')
    expect(JSON.stringify(hook.cache.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain('private-session-one')
    hook.unmount(); expect(init.signal.aborted).toBe(true); await waitFor(() => expect(source.cancel).toHaveBeenCalled())
  })

  it('clears run/session state immediately and rejects late poll results from the previous session', async () => {
    const first = stream(), second = stream(); fetchMock.mockResolvedValueOnce(response(first.body)).mockResolvedValueOnce(response(second.body))
    let completeOld!: (value: { events: ExecutionEvent[] }) => void
    vi.mocked(listRunEvents).mockImplementationOnce(() => new Promise((resolve) => { completeOld = resolve })).mockResolvedValue({ events: [] })
    const hook = show('same-run'); await waitFor(() => expect(listRunEvents).toHaveBeenCalled())
    await act(async () => first.send(event('same-run', 1))); await waitFor(() => expect(hook.result.current.events).toHaveLength(1))
    token = 'private-session-two'; hook.rerender({ id: 'same-run' }); expect(hook.result.current.events).toEqual([]); expect(hook.result.current.live).toBe(false)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2)); expect(fetchMock.mock.calls[0][1].signal.aborted).toBe(true)
    await act(async () => { completeOld({ events: [event('same-run', 99)] }); second.send(event('same-run', 2)) })
    await waitFor(() => expect(hook.result.current.events.map((value) => value.seq)).toEqual([2]))
    hook.rerender({ id: 'other-run' }); expect(hook.result.current.events).toEqual([]); hook.unmount()
  })

  it('ignores a fetch response that arrives after its run subscription was canceled', async () => {
    let complete!: (value: ReturnType<typeof response>) => void
    fetchMock.mockImplementationOnce(() => new Promise((resolve) => { complete = resolve }))
    const old = stream(); const hook = show('old-run'); await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    hook.rerender({ id: '' }); await act(async () => complete(response(old.body))); await waitFor(() => expect(old.cancel).toHaveBeenCalled())
    expect(hook.result.current).toEqual({ events: [], live: false }); hook.unmount()
  })

  it('keeps polling available when the stream fails and disables both paths for archived runs', async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 502 }); vi.mocked(listRunEvents).mockResolvedValue({ events: [event('run', 1)] })
    const hook = show('run'); await waitFor(() => expect(hook.result.current.events).toHaveLength(1)); expect(hook.result.current.live).toBe(false); hook.unmount()
    fetchMock.mockClear(); vi.mocked(listRunEvents).mockClear(); const archived = show(''); await act(async () => {}); expect(fetchMock).not.toHaveBeenCalled(); expect(listRunEvents).not.toHaveBeenCalled(); expect(archived.result.current.events).toEqual([]); archived.unmount()
  })

  it('preserves distinct events even when an incoming page has the same count', () => {
    expect(mergeRunEvents([event('run', 1)], [event('run', 2), event('other', 3)], 'run').map((value) => value.seq)).toEqual([1, 2])
  })
})
