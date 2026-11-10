import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Usage from '../pages/Usage'

vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: { sub: 'principal-a' } }) }))

const base = { tenant_id: 't', finished_at: '2026-11-09T10:00:01Z', latency_ms: 10, streaming: false, source: 'managed', requests: 1, plane: 'llmplex', status: 'ok', usage: null, cost: { basis: 'unknown' } }
const withLimits = { ...base, id: 'ul_limits', started_at: '2026-11-09T10:00:00Z', access_point_id: 'ide-team', caller_kind: 'user', provider_limits: { provider: 'openai', classification: 'rate_limited', retry_after_ms: 1500 } }
const without = { ...base, id: 'ul_plain', started_at: '2026-11-09T09:00:00Z' }

const tok = (input: number, output: number) => ({ input_tokens: input, output_tokens: output, cache_read_tokens: null, cache_write_tokens: null, reasoning_tokens: null, total_tokens: null })
const lower = { ...base, id: 'ul_lower', started_at: '2026-11-09T08:00:00Z', operation: 'openai.v1.realtime', model_id: 'gpt-realtime', usage: tok(120, 30), usage_bound: 'lower_bound', cost: { basis: 'estimated', currency: 'USD', amount: '500', scale: 6 } }
const ambiguous = { ...base, id: 'ul_amb', started_at: '2026-11-09T07:00:00Z', operation: 'live.connect', usage: tok(7, 8), usage_bound: 'ambiguous' }
const partial = { ...base, id: 'ul_part', started_at: '2026-11-09T06:00:00Z', usage: tok(1, 2), usage_bound: 'partial' }
const flagged = { ...base, id: 'ul_flag', started_at: '2026-11-09T04:00:00Z', operation: 'some.future.socket', session: true, usage: tok(3, 3) }
const exact = { ...base, id: 'ul_exact', started_at: '2026-11-09T05:00:00Z', usage: tok(9, 4) }

let calls: URL[]
beforeEach(() => {
  calls = []
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    const url = new URL(input, 'http://localhost')
    calls.push(url)
    if (url.pathname === '/api/v1/usage/summary') return new Response(JSON.stringify({ group_by: [], groups: [], records_scanned: 0, truncated: false }))
    if (url.pathname === '/api/v1/usage/records') return new Response(JSON.stringify({ records: [withLimits, without, lower, ambiguous, partial, exact, flagged] }))
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

  it('never shows a bounded count bare, and explains each bound in the table and drawer', async () => {
    show()
    const table = await screen.findByRole('table', { name: 'Usage records, newest first' })
    const row = (id: string) => within(table).getByRole('button', { name: `Open details for record ${id}` }).closest('tr')!
    expect(row('ul_lower')).toHaveTextContent('≥ 120 (lower bound)')
    expect(row('ul_lower')).toHaveTextContent('≥ 30 (lower bound)')
    expect(row('ul_lower')).toHaveTextContent('(from bounded usage)')
    expect(row('ul_amb')).toHaveTextContent('7 (ambiguous)')
    expect(row('ul_part')).toHaveTextContent('1 (partial)')
    // exact records stay bare
    expect(row('ul_exact')).toHaveTextContent('9')
    expect(row('ul_exact')).not.toHaveTextContent(/bound|ambiguous|partial/)

    fireEvent.click(within(row('ul_amb')).getByRole('button'))
    let note = within(screen.getByRole('dialog', { name: 'Usage record detail' })).getAllByRole('note')[0]
    expect(note).toHaveTextContent('Not a final count')
    expect(note).toHaveTextContent(/resumed session/)
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.click(within(row('ul_part')).getByRole('button'))
    note = within(screen.getByRole('dialog', { name: 'Usage record detail' })).getAllByRole('note')[0]
    expect(note).toHaveTextContent(/non-token usage/)
    fireEvent.keyDown(window, { key: 'Escape' })
    fireEvent.click(within(row('ul_lower')).getByRole('button'))
    const drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    expect(drawer).toHaveTextContent(/at least this much/)
    expect(drawer).toHaveTextContent('Any cost computed from these counters is not final')
  })

  it('labels WebSocket session operations as sessions and not other rows', async () => {
    show()
    const table = await screen.findByRole('table', { name: 'Usage records, newest first' })
    const row = (id: string) => within(table).getByRole('button', { name: `Open details for record ${id}` }).closest('tr')!
    expect(within(row('ul_lower')).getByText('session')).toBeInTheDocument()
    expect(within(row('ul_amb')).getByText('session')).toBeInTheDocument()
    expect(within(row('ul_exact')).queryByText('session')).toBeNull()
    // The server's flag wins; operation ids are only a fallback for records without it.
    expect(within(row('ul_flag')).getByText('session')).toBeInTheDocument()
    fireEvent.click(within(row('ul_lower')).getByRole('button'))
    expect(screen.getByText(/One request was admitted and settled for the whole session/)).toBeInTheDocument()
  })

  it('shows bounded-record counts next to the summary totals and drops the static caveat', async () => {
    const group = { key: { model_id: 'gpt-realtime' }, records: 5, requests: 5, errors: 0, denied: 0, unknown_status: 0, tokens: { input_tokens: 500 }, tokens_unknown: {}, unknown_usage_records: 0, bounded_usage_records: 3, lower_bound_records: 2, partial_usage_records: 1, cost: [], unknown_cost_records: 5 }
    const fetchMock = vi.mocked(fetch)
    const inner = fetchMock.getMockImplementation()!
    fetchMock.mockImplementation(async (input, init) => new URL(String(input), 'http://localhost').pathname === '/api/v1/usage/summary'
      ? new Response(JSON.stringify({ group_by: ['model_id'], groups: [group], records_scanned: 5, truncated: false }))
      : inner(input, init))
    show()
    const totals = await screen.findByLabelText('Usage totals')
    expect(totals).toHaveTextContent('Not final: includes 3 bounded records (2 lower bound, 1 partial)')
    const breakdown = await screen.findByRole('table', { name: 'Usage breakdown' })
    expect(breakdown).toHaveTextContent('500 (incl. 3 bounded)')
    expect(breakdown).toHaveTextContent('bounded: 3 (2 lower bound, 1 partial)')
    expect(screen.queryByText(/the summary does not flag them/)).toBeNull()
  })
})
