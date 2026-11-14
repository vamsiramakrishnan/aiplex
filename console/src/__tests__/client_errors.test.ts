import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { authenticatedRequest } from '../api/client'

const fetchMock = vi.fn()
const original = global.fetch
beforeEach(() => { global.fetch = fetchMock as never; fetchMock.mockReset(); localStorage.clear() })
afterEach(() => { global.fetch = original })

const fail = async (response: Response, plainTextErrors = true) => {
  fetchMock.mockResolvedValueOnce(response)
  return authenticatedRequest('/x', plainTextErrors ? { plainTextErrors } : undefined).then(() => { throw new Error('expected a rejection') }, (e) => e as Error & { status?: number; code?: string })
}

describe('authenticatedRequest error bodies', () => {
  it('keeps a JSON body with message and code unchanged', async () => {
    const e = await fail(new Response(JSON.stringify({ message: 'resource revision changed', code: 'REVISION_CONFLICT', details: { a: 1 } }), { status: 409 }))
    expect(e).toMatchObject({ message: 'resource revision changed', status: 409, code: 'REVISION_CONFLICT', details: { a: 1 } })
  })
  it('uses the body text of a plain-text refusal, on one trimmed line', async () => {
    const e = await fail(new Response('  current tool grant required\n', { status: 403, statusText: 'Forbidden' }))
    expect(e.message).toBe('current tool grant required')
    expect(e.status).toBe(403)
  })
  it('falls back to the status text for an empty body, and to HTTP status for JSON with no message', async () => {
    expect((await fail(new Response('', { status: 502, statusText: 'Bad Gateway' }))).message).toBe('Bad Gateway')
    expect((await fail(new Response('{}', { status: 500 }))).message).toBe('HTTP 500')
  })
  it('reads a JSON-RPC error envelope: error.message and error.code', async () => {
    const e = await fail(new Response(JSON.stringify({ jsonrpc: '2.0', id: 1, error: { code: -32001, message: 'tool call denied by policy rule no-etc' } }), { status: 403 }))
    expect(e.message).toBe('tool call denied by policy rule no-etc')
    expect(e.code).toBe('-32001')
  })
  it('cuts an HTML error page to one line of at most 300 characters', async () => {
    const html = '<html>\n<body><h1>502 Bad Gateway</h1>' + '<p>x</p>'.repeat(200) + '</body></html>'
    const e = await fail(new Response(html, { status: 502, statusText: 'Bad Gateway' }))
    expect(e.message.length).toBeLessThanOrEqual(300)
    expect(e.message).not.toContain('\n')
    expect(e.message.endsWith('…')).toBe(true)
  })
  it('uses a JSON-RPC error that is a bare string', async () => {
    expect((await fail(new Response(JSON.stringify({ jsonrpc: '2.0', error: 'tool grant required' }), { status: 403 }))).message).toBe('tool grant required')
  })
  it('never renders an object message as [object Object]', async () => {
    const e = await fail(new Response(JSON.stringify({ message: { a: 1 }, error: { code: -32001, message: 'denied by rule' } }), { status: 403 }))
    expect(e.message).toBe('denied by rule')
    expect((await fail(new Response(JSON.stringify({ message: { a: 1 } }), { status: 500, statusText: 'Internal Server Error' }))).message).toBe('Internal Server Error')
    expect((await fail(new Response(JSON.stringify({ message: { a: 1 } }), { status: 500 }))).message).toBe('HTTP 500')
    expect((await fail(new Response(JSON.stringify({ message: 42 }), { status: 500 }))).message).toBe('42')
  })
  it('handles a null message and a JSON array body', async () => {
    expect((await fail(new Response(JSON.stringify({ message: null }), { status: 500 }))).message).toBe('HTTP 500')
    expect((await fail(new Response('[1,2]', { status: 500, statusText: 'Internal Server Error' }))).message).toBe('Internal Server Error')
    expect((await fail(new Response('null', { status: 500 }))).message).toBe('HTTP 500')
  })
  it('without the option, a non-JSON body gives the status text and nothing from the body', async () => {
    const echo = await fail(new Response('<html>you sent: {"secret":"sample"}</html>', { status: 502, statusText: 'Bad Gateway' }), false)
    expect(echo.message).toBe('Bad Gateway')
    expect((await fail(new Response('current tool grant required', { status: 403, statusText: 'Forbidden' }), false)).message).toBe('Forbidden')
    expect((await fail(new Response('plain', { status: 500 }), false)).message).toBe('HTTP 500')
  })
  it('does not send the option to fetch', async () => {
    await fail(new Response('x', { status: 500 }))
    expect(JSON.stringify(fetchMock.mock.calls[0][1])).not.toContain('plainTextErrors')
  })
})
