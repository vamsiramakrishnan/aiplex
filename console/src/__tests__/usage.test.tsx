import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Usage, { buildFilter, emptyDraft } from '../pages/Usage'
import { blankDraft, priceFromDraft } from '../pages/UsagePrices'
import { QuotaOverview } from '../pages/QuotaTabs'

vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: { sub: 'principal-a' } }) }))
vi.mock('../api/quota', () => ({ quotaPolicy: vi.fn().mockResolvedValue({ policy: { tenant_id: 't', id: 'team', revision: 1 }, reference: { digest: 'd' }, active: { digest: 'd' }, as_of: new Date().toISOString() }), quotaInventory: vi.fn().mockResolvedValue({ kind: 'budgets', records: [], done: true, as_of: new Date().toISOString() }), quotaAttempt: vi.fn(), quotaReconciliation: vi.fn(), quotaMutation: vi.fn(), validateQuotaPolicy: vi.fn(), quotaOperationKey: vi.fn(), safeQuotaJSON: (s: string) => JSON.parse(s) }))

const base = { tenant_id: 't', finished_at: '2026-11-09T10:00:01Z', latency_ms: 1200, streaming: false, source: 'managed', requests: 1 }
const records = [
  { ...base, id: 'ul_known', started_at: '2026-11-09T10:00:00Z', plane: 'llmplex', model_id: 'gemini-2.5-flash', provider: 'google', actor_id: 'tutor-agent', principal_id: 'student-1', client_id: 'cursor', status: 'ok', trace_id: 'trace-123', model_version: 'v9',
    usage: { input_tokens: 0, output_tokens: null, cache_read_tokens: null, cache_write_tokens: null, reasoning_tokens: null, total_tokens: null }, cost: { basis: 'estimated', currency: 'USD', amount: '1135', scale: 6 } },
  { ...base, id: 'ul_denied', started_at: '2026-11-09T09:00:00Z', plane: 'mcplex', tool_name: 'search_curriculum', status: 'denied', http_status: 403, latency_ms: 5, usage: null, cost: { basis: 'unknown' } },
]
const summary = {
  group_by: ['model_id'], records_scanned: 14, truncated: false,
  groups: [
    { key: { model_id: 'm1' }, records: 4, requests: 4, errors: 1, denied: 0, unknown_status: 0, tokens: { input_tokens: 120, output_tokens: 0 }, tokens_unknown: { input_tokens: 1 }, unknown_usage_records: 0,
      cost: [{ currency: 'USD', basis: 'estimated', amount: '1135', scale: 6, records: 3 }, { currency: 'USD', basis: 'settled', amount: '250', scale: 2, records: 1 }], unknown_cost_records: 0 },
    { key: { model_id: 'm2' }, records: 10, requests: 10, errors: 0, denied: 2, unknown_status: 0, tokens: {}, tokens_unknown: { input_tokens: 10, output_tokens: 10 }, unknown_usage_records: 10, cost: [], unknown_cost_records: 10 },
  ],
}
const spec = { id: 'p1', model_id: 'gemini-2.5-flash', currency: 'USD', scale: 6, meters: { input_tokens: { numerator: 3, denominator: 1000 } } }

type Reply = { status?: number; body?: unknown }
let routes: Record<string, (url: URL, init?: RequestInit) => Reply>
let calls: { url: URL; init?: RequestInit }[]

beforeEach(() => {
  calls = []
  routes = {
    'GET /api/v1/usage/summary': () => ({ body: summary }),
    'GET /api/v1/usage/records': (url) => ({ body: url.searchParams.get('cursor') ? { records: [{ ...records[1], id: 'ul_older' }] } : { records, next_cursor: 'CUR2' } }),
    'GET /api/v1/usage/prices/': () => ({ body: [{ id: 'p1', revision: 4, spec }] }),
  }
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://localhost')
    calls.push({ url, init })
    const handler = routes[`${init?.method ?? 'GET'} ${url.pathname}`]
    if (!handler) return new Response(JSON.stringify({ message: 'no route' }), { status: 500 })
    const { status = 200, body } = handler(url, init)
    return new Response(status === 204 ? null : JSON.stringify(body ?? {}), { status })
  }))
})
afterEach(() => vi.unstubAllGlobals())

function show(ui = <Usage />) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}>{ui}</QueryClientProvider></MemoryRouter>)
}
const region = (name: string) => screen.getByRole('region', { name })
const lastCall = (path: string) => calls.filter((c) => c.url.pathname === path).at(-1)!
const rowOf = (container: HTMLElement, text: string) => within(container).getAllByRole('row').find((r) => r.textContent?.includes(text))!

describe('Usage summary', () => {
  it('shows requests, tokens and cost per currency and basis with unknown counts', async () => {
    show()
    const totals = await screen.findByRole('region', { name: 'Usage totals' })
    expect(within(within(totals).getByRole('group', { name: 'Requests' })).getByText('14')).toBeInTheDocument()
    const tokens = within(totals).getByRole('group', { name: 'Tokens' })
    expect(tokens).toHaveTextContent('Input 120')
    expect(tokens).toHaveTextContent('+11 records unreported')
    expect(tokens).toHaveTextContent('Output 0')
    expect(tokens).toHaveTextContent('10 records reported no usage at all')
    const cost = within(totals).getByRole('group', { name: 'Cost' })
    expect(cost).toHaveTextContent('USD 0.001135 estimated')
    expect(cost).toHaveTextContent('USD 2.50 settled')
    expect(cost).toHaveTextContent('10 records have unknown cost')
  })

  it('renders unknown usage and cost as unknown, not zero, in the breakdown', async () => {
    show()
    const table = await screen.findByRole('table', { name: 'Usage breakdown' })
    const m1 = rowOf(table, 'm1'), m2 = rowOf(table, 'm2')
    expect(m1).toHaveTextContent('120 (+1 unknown)')
    expect(within(m1).getByText('0')).toBeInTheDocument()
    expect(m1).toHaveTextContent('USD 0.001135 estimated')
    expect(m1).toHaveTextContent('USD 2.50 settled')
    const cells = within(m2).getAllByRole('cell').map((c) => c.textContent)
    expect(cells[1]).toBe('unknown')
    expect(cells[2]).toBe('unknown')
    expect(cells[3]).toBe('unknown')
    expect(m2).toHaveTextContent('usage: 10cost: 10')
  })

  it('sorts by each measure and keeps unknown last in both directions', async () => {
    show()
    const table = await screen.findByRole('table', { name: 'Usage breakdown' })
    const order = () => within(table).getAllByRole('rowheader').map((h) => h.textContent)
    expect(order()).toEqual(['m2', 'm1']) // requests, descending by default
    fireEvent.click(screen.getByRole('button', { name: 'Sort by Requests' }))
    expect(order()).toEqual(['m1', 'm2'])
    fireEvent.click(screen.getByRole('button', { name: 'Sort by Input tokens' }))
    expect(order()).toEqual(['m1', 'm2']) // m2 has unknown input: last
    fireEvent.click(screen.getByRole('button', { name: 'Sort by Input tokens' }))
    expect(order()).toEqual(['m1', 'm2']) // still last when ascending
    fireEvent.click(screen.getByRole('button', { name: 'Sort by Cost' }))
    expect(order()).toEqual(['m1', 'm2'])
    expect(within(table).getByRole('columnheader', { name: /Cost/ })).toHaveAttribute('aria-sort', 'descending')
  })

  it('warns when the summary is truncated', async () => {
    routes['GET /api/v1/usage/summary'] = () => ({ body: { ...summary, truncated: true, records_scanned: 200000 } })
    show()
    expect(await screen.findByText(/Truncated: totals cover only the newest 200,000 records/)).toBeInTheDocument()
  })

  it('explains an empty range and forbidden access', async () => {
    routes['GET /api/v1/usage/summary'] = () => ({ body: { group_by: [], groups: [], records_scanned: 0, truncated: false } })
    routes['GET /api/v1/usage/records'] = () => ({ status: 403, body: { code: 'TENANT_REQUIRED', message: 'verified tenant binding required' } })
    show()
    expect(await screen.findByText('No usage recorded for this selection.')).toBeInTheDocument()
    expect(await within(region('Requests')).findByRole('alert')).toHaveTextContent('You do not have access to usage records.')
  })

  it('reports an unconfigured ledger and offers retry', async () => {
    routes['GET /api/v1/usage/summary'] = () => ({ status: 503, body: { code: 'USAGE_LEDGER_UNAVAILABLE', message: 'usage ledger is not configured' } })
    show()
    const panel = await within(region('Usage summary')).findByRole('alert')
    expect(panel).toHaveTextContent('The usage ledger is not configured on this server.')
    expect(within(panel).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('sends filters and the group-by selection to the API', async () => {
    show()
    await screen.findByRole('table', { name: 'Usage breakdown' })
    expect(lastCall('/api/v1/usage/summary').url.searchParams.get('group_by')).toBe('model_id')
    fireEvent.click(within(screen.getByRole('group', { name: 'Group by' })).getByLabelText('Agent'))
    await waitFor(() => expect(lastCall('/api/v1/usage/summary').url.searchParams.get('group_by')).toBe('model_id,actor_id'))
    fireEvent.change(screen.getByLabelText('Time bucket'), { target: { value: 'day' } })
    await waitFor(() => expect(lastCall('/api/v1/usage/summary').url.searchParams.get('bucket')).toBe('day'))
    const form = screen.getByRole('form', { name: 'Usage filters' })
    fireEvent.change(within(form).getByLabelText('Time range'), { target: { value: '7d' } })
    fireEvent.change(within(form).getByLabelText('Plane'), { target: { value: 'mcplex' } })
    fireEvent.change(within(form).getByLabelText('Model'), { target: { value: ' gemini-2.5-flash ' } })
    fireEvent.change(within(form).getByLabelText('Tool'), { target: { value: 'search_curriculum' } })
    fireEvent.change(within(form).getByLabelText('Agent'), { target: { value: 'tutor-agent' } })
    fireEvent.change(within(form).getByLabelText('Principal'), { target: { value: 'student-1' } })
    fireEvent.change(within(form).getByLabelText('Client'), { target: { value: 'cursor' } })
    fireEvent.change(within(form).getByLabelText('Status'), { target: { value: 'error' } })
    fireEvent.click(within(form).getByRole('button', { name: 'Apply filters' }))
    await waitFor(() => expect(lastCall('/api/v1/usage/records').url.searchParams.get('client_id')).toBe('cursor'))
    const q = lastCall('/api/v1/usage/records').url.searchParams
    expect([q.get('plane'), q.get('model_id'), q.get('tool_name'), q.get('actor_id'), q.get('principal_id'), q.get('status')]).toEqual(['mcplex', 'gemini-2.5-flash', 'search_curriculum', 'tutor-agent', 'student-1', 'error'])
    expect(Date.parse(q.get('since')!)).toBeLessThan(Date.now() - 6 * 86_400_000)
  })

  it('builds filters with a resolved time window and no empty fields', () => {
    const filter = buildFilter({ ...emptyDraft, preset: '1h', model_id: ' m ' }, new Date('2026-11-09T12:00:00Z'))
    expect(filter).toEqual({ since: '2026-11-09T11:00:00.000Z', model_id: 'm' })
  })
})

describe('Usage requests', () => {
  it('distinguishes reported zero, unknown tokens and unknown cost per row', async () => {
    show()
    const table = await within(region('Requests')).findByRole('table')
    const cells = (id: string) => within(rowOf(table, id)).getAllByRole('cell').map((c) => c.textContent)
    // time, plane, model/tool, agent, principal, client, status, in, out, cache write, modality, units, cost, latency
    const known = cells('gemini-2.5-flash')
    expect(known.slice(1, 14)).toEqual(['llmplex', 'gemini-2.5-flash', 'tutor-agent', 'student-1', 'cursor', 'ok', '0', 'unknown', 'unknown', '—', '—', 'USD 0.001135estimated', '1200 ms'])
    const denied = cells('search_curriculum')
    expect(denied.slice(1, 14)).toEqual(['mcplex', 'search_curriculum', '—', '—', '—', 'denied', 'unknown', 'unknown', 'unknown', '—', '—', 'unknown', '5 ms'])
  })

  it('opens a drawer with every metadata field and closes on Escape', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_known' }))
    const drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    const fields = Object.fromEntries(within(drawer).getAllByRole('term').map((t) => [t.textContent, t.nextElementSibling?.textContent]))
    expect(fields).toMatchObject({
      ID: 'ul_known', Tenant: 't', Plane: 'llmplex', Provider: 'google', Model: 'gemini-2.5-flash', 'Model version': 'v9', Agent: 'tutor-agent', Principal: 'student-1', Client: 'cursor',
      'Trace ID': 'trace-123', 'Request ID': '—', Status: 'ok', Source: 'managed', Streaming: 'no', Latency: '1200 ms', Requests: '1',
      'Input tokens': '0', 'Output tokens': 'unknown', 'Total tokens': 'unknown', Cost: 'USD 0.001135', 'Cost basis': 'estimated', 'Cost amount (minor units)': '1135', 'Cost scale': '6',
    })
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Open details for record ul_denied' }))
    expect(screen.getByText(/Token counts are unknown, not zero/)).toBeInTheDocument()
    expect(within(screen.getByRole('dialog')).getByText('HTTP status').nextElementSibling).toHaveTextContent('403')
  })

  it('pages through records with the server cursor', async () => {
    show()
    const section = region('Requests')
    await within(section).findByRole('table')
    expect(within(section).getByRole('button', { name: 'Newer' })).toBeDisabled()
    fireEvent.click(within(section).getByRole('button', { name: 'Older' }))
    await within(section).findByText('Page 2')
    expect(lastCall('/api/v1/usage/records').url.searchParams.get('cursor')).toBe('CUR2')
    expect(within(section).getByRole('button', { name: 'Older' })).toBeDisabled()
    fireEvent.click(within(section).getByRole('button', { name: 'Newer' }))
    await within(section).findByText('Page 1')
  })

  it('shows an empty state when nothing matches', async () => {
    routes['GET /api/v1/usage/records'] = () => ({ body: { records: [] } })
    show()
    expect(await screen.findByText('No requests match these filters.')).toBeInTheDocument()
  })
})

describe('Model prices', () => {
  it('explains that cost is unknown until a price exists', async () => {
    routes['GET /api/v1/usage/prices/'] = () => ({ body: [] })
    show()
    expect(await within(region('Model prices')).findByText('No prices configured. Cost is unknown until a price is configured or quota settles it.')).toBeInTheDocument()
  })

  it('lists prices and validates before sending', async () => {
    show()
    const section = region('Model prices')
    const table = await within(section).findByRole('table')
    expect(table).toHaveTextContent('input_tokens 3/1000')
    fireEvent.click(within(section).getByRole('button', { name: 'Add price' }))
    fireEvent.click(within(section).getByRole('button', { name: 'Save price' }))
    expect(await within(section).findByText('Price ID is required (up to 512 characters).')).toBeInTheDocument()
    expect(calls.some((c) => c.init?.method === 'PUT')).toBe(false)
  })

  it('creates a price at revision 0 with exact meter numbers', async () => {
    routes['PUT /api/v1/usage/prices/new-price'] = () => ({ body: { id: 'new-price', revision: 1, spec } })
    show()
    const section = region('Model prices')
    await within(section).findByRole('table')
    fireEvent.click(within(section).getByRole('button', { name: 'Add price' }))
    fireEvent.change(within(section).getByLabelText('Price ID'), { target: { value: 'new-price' } })
    fireEvent.change(within(section).getByLabelText('Model ID'), { target: { value: 'gemini-2.5-flash' } })
    fireEvent.change(within(section).getByLabelText('output_tokens numerator'), { target: { value: '9' } })
    fireEvent.change(within(section).getByLabelText('output_tokens denominator'), { target: { value: '1000' } })
    fireEvent.click(within(section).getByRole('button', { name: 'Save price' }))
    await waitFor(() => expect(calls.some((c) => c.init?.method === 'PUT')).toBe(true))
    const put = calls.find((c) => c.init?.method === 'PUT')!
    expect((put.init!.headers as Record<string, string>)['If-Match']).toBe('"0"')
    expect(JSON.parse(put.init!.body as string)).toEqual({ id: 'new-price', model_id: 'gemini-2.5-flash', currency: 'USD', scale: 6, meters: { output_tokens: { numerator: 9, denominator: 1000 } } })
    await waitFor(() => expect(screen.queryByRole('form', { name: 'Add price' })).toBeNull())
  })

  it('handles a revision conflict by offering the latest revision, then saves against it', async () => {
    let writes = 0
    routes['PUT /api/v1/usage/prices/p1'] = (_url, init) => {
      writes++
      return (init!.headers as Record<string, string>)['If-Match'] === '"4"' ? { status: 409, body: { code: 'REVISION_CONFLICT', message: 'resource revision changed' } } : { body: { id: 'p1', revision: 8, spec } }
    }
    routes['GET /api/v1/usage/prices/p1'] = () => ({ body: { id: 'p1', revision: 7, spec: { ...spec, currency: 'EUR' } } })
    show()
    const section = region('Model prices')
    await within(section).findByRole('table')
    fireEvent.click(within(section).getByRole('button', { name: 'Edit price p1' }))
    expect(within(section).getByLabelText('Price ID')).toHaveAttribute('readonly')
    fireEvent.change(within(section).getByLabelText('input_tokens numerator'), { target: { value: '5' } })
    fireEvent.click(within(section).getByRole('button', { name: 'Save price' }))
    expect(await within(section).findByText(/was changed by someone else/)).toBeInTheDocument()
    expect(writes).toBe(1)
    fireEvent.click(within(section).getByRole('button', { name: 'Load latest revision' }))
    await within(section).findByText('Edit price (revision 7)')
    expect(within(section).getByLabelText('Currency')).toHaveValue('EUR')
    fireEvent.click(within(section).getByRole('button', { name: 'Save price' }))
    await waitFor(() => expect(writes).toBe(2))
    expect(((calls.filter((c) => c.init?.method === 'PUT').at(-1)!.init!.headers) as Record<string, string>)['If-Match']).toBe('"7"')
  })

  it('tells non-administrators that writes need the admin role', async () => {
    routes['PUT /api/v1/usage/prices/p1'] = () => ({ status: 403, body: { code: 'FORBIDDEN', message: 'insufficient role' } })
    show()
    const section = region('Model prices')
    await within(section).findByRole('table')
    fireEvent.click(within(section).getByRole('button', { name: 'Edit price p1' }))
    fireEvent.click(within(section).getByRole('button', { name: 'Save price' }))
    expect(await within(section).findByText('Only administrators can change prices.')).toBeInTheDocument()
  })

  it('deletes with an explicit confirmation at the listed revision', async () => {
    routes['DELETE /api/v1/usage/prices/p1'] = () => ({ status: 204 })
    show()
    const section = region('Model prices')
    await within(section).findByRole('table')
    fireEvent.click(within(section).getByRole('button', { name: 'Delete price p1' }))
    expect(calls.some((c) => c.init?.method === 'DELETE')).toBe(false)
    fireEvent.click(within(section).getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(calls.some((c) => c.init?.method === 'DELETE')).toBe(true))
    expect(((calls.find((c) => c.init?.method === 'DELETE')!.init!.headers) as Record<string, string>)['If-Match']).toBe('"4"')
  })

  it('validates drafts with the backend rules', () => {
    const draft = blankDraft()
    expect(priceFromDraft(draft).errors).toEqual(expect.arrayContaining(['Price ID is required (up to 512 characters).', 'Model ID is required.', 'Price at least one meter.']))
    const ok = { ...draft, id: 'p', model_id: 'm', currency: 'usd' }
    expect(priceFromDraft(ok).errors).toContain('Currency must be three uppercase letters, e.g. USD.')
    const meter = (numerator: string, denominator: string) => ({ ...draft, id: 'p', model_id: 'm', meters: { ...draft.meters, requests: { numerator, denominator } } })
    expect(priceFromDraft(meter('0', '1')).price?.meters).toEqual({ requests: { numerator: 0, denominator: 1 } }) // free meter is valid
    expect(priceFromDraft(meter('1', '0')).errors[0]).toMatch(/denominator/)
    expect(priceFromDraft(meter('1.5', '1')).errors[0]).toMatch(/numerator/)
    expect(priceFromDraft(meter('9007199254740993', '1')).errors[0]).toMatch(/numerator/)
    expect(priceFromDraft({ ...meter('1', '1'), scale: '19' }).errors).toContain('Scale must be a whole number from 0 to 18.')
    expect(priceFromDraft({ ...meter('1', '1'), effective_from: '2026-11-02T00:00:00Z', effective_until: '2026-11-01T00:00:00Z' }).errors).toContain('Effective until must be after effective from.')
  })
})

describe('QuotaPlex navigation', () => {
  it('links the Overview tab to the Usage sub-route', async () => {
    show(<QuotaOverview />)
    const nav = screen.getByRole('navigation', { name: 'QuotaPlex sections' })
    expect(within(nav).getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Usage' })).toHaveAttribute('href', '/quotaplex/usage')
    await screen.findByText('team · revision 1')
  })
})
