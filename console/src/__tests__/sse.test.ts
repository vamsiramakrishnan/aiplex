import { describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { consumeSSE } from '../sse'
import { useSSE } from '../hooks/useSSE'

vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ token: 'private-token', isAuthenticated: true }) }))

function chunks(...values: string[]) {
  return new ReadableStream<Uint8Array>({ start(controller) { for (const value of values) controller.enqueue(new TextEncoder().encode(value)); controller.close() } })
}

describe('Authenticated stream framing', () => {
  it('handles fragmented CRLF boundaries, multiple events and multiline data', async () => {
    const events: [string, string][] = []
    await consumeSSE(chunks(':heartbeat\r\n\r\nevent: sta', 'ts\r\ndata: {"value":\r\ndata: 1}\r', '\n\r\nevent: stats\ndata: {}\n\n'), (event, data) => events.push([event, data]))
    expect(events).toEqual([['stats', '{"value":\n1}'], ['stats', '{}']])
  })

  it('bounds memory for a server that never finishes its event', async () => {
    await expect(consumeSSE(chunks('data: ' + 'x'.repeat(1_048_576)), () => {})).rejects.toThrow('Event too large')
  })

  it('sends stream credentials in headers and cancels on unmount', async () => {
    const original = global.fetch
    const mock = vi.fn().mockResolvedValue({ ok: true, status: 200, body: chunks('event: stats\ndata: {"total_instances":7}\n\n') })
    global.fetch = mock
    const hook = renderHook(() => useSSE())
    try {
      await waitFor(() => expect(hook.result.current?.total_instances).toBe(7))
      const [url, options] = mock.mock.calls[0]
      expect(url).toBe('/events/stream')
      expect(options.headers.Authorization).toBe('Bearer private-token')
      hook.unmount()
      expect(options.signal.aborted).toBe(true)
    } finally { hook.unmount(); global.fetch = original }
  })
})
