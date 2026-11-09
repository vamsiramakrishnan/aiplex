import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Usage from '../pages/Usage'

vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: { sub: 'principal-a' } }) }))

const base = { tenant_id: 't', finished_at: '2026-11-09T10:00:01Z', latency_ms: 10, streaming: false, source: 'managed', requests: 1, plane: 'llmplex', status: 'ok', usage: null, cost: { basis: 'unknown' } }
const withLimits = { ...base, id: 'ul_limits', started_at: '2026-11-09T10:00:00Z', access_point_id: 'ide-team', caller_kind: 'user', provider_limits: { provider: 'openai', classification: 'rate_limited', retry_after_ms: 1500 } }
const without = { ...base, id: 'ul_plain', started_at: '2026-11-09T09:00:00Z' }

let calls: URL[]
beforeEach(() => {
  calls = []
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    const url = new URL(input, 'http://localhost')
    calls.push(url)
    if (url.pathname === '/api/v1/usage/summary') return new Response(JSON.stringify({ group_by: [], groups: [], records_scanned: 0, truncated: false }))
    if (url.pathname === '/api/v1/usage/records') return new Response(JSON.stringify({ records: [withLimits, without] }))
    if (url.pathname === '/api/v1/usage/prices/') return new Response('[]')
    return new Response('{}', { status: 500 })
  }))
})
afterEach(() => vi.unstubAllGlobals())

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}><Usage /></QueryClientProvider></MemoryRouter>)
}
const last = (path: string) => calls.filter(u => u.pathname === path).at(-1)!

describe('Usage page: access point and caller kind', () => {
  it('filters by access point and caller kind and offers both as group-by keys', async () => {
    show()
    const form = await screen.findByRole('form', { name: 'Usage filters' })
    fireEvent.change(within(form).getByLabelText('Access point'), { target: { value: 'ide-team' } })
    fireEvent.change(within(form).getByLabelText('Caller kind'), { target: { value: 'user' } })
    fireEvent.click(within(form).getByRole('button', { name: 'Apply filters' }))
    await waitFor(() => expect(last('/api/v1/usage/summary').searchParams.get('access_point_id')).toBe('ide-team'))
    expect(last('/api/v1/usage/summary').searchParams.get('caller_kind')).toBe('user')
    expect(last('/api/v1/usage/records').searchParams.get('access_point_id')).toBe('ide-team')

    const groupBy = screen.getByRole('group', { name: 'Group by' })
    fireEvent.click(within(groupBy).getByLabelText('Caller kind'))
    fireEvent.click(within(groupBy).getByLabelText('Access point'))
    await waitFor(() => expect(last('/api/v1/usage/summary').searchParams.get('group_by')).toBe('model_id,access_point_id,caller_kind'))
  })

  it('shows provider limits in the record drawer only when the record has them', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_limits' }))
    let drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    const limits = within(drawer).getByRole('region', { name: 'Provider limits' })
    expect(limits).toHaveTextContent('rate_limited')
    expect(limits).toHaveTextContent('1500')
    expect(drawer).toHaveTextContent('ide-team')
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_plain' }))
    drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    expect(within(drawer).queryByRole('region', { name: 'Provider limits' })).toBeNull()
  })
})
