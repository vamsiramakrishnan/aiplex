import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Usage, { RecordDrawer, SourceBadge, groupName, keyText } from '../pages/Usage'
import * as api from '../api/usage'

vi.mock('../hooks/useAuth', () => { const session = { user: { sub: 'principal-a', roles: ['admin'], scopes: [] } }; return { useAuth: () => session, useOptionalAuth: () => session } })

const TRACE = '0af7651916cd43dd8448eb211c80319c'
const SPAN = 'b7ad6b7169203331'
const counters = { input_tokens: 130, output_tokens: 20, cache_read_tokens: 30, cache_write_tokens: 0, reasoning_tokens: null, total_tokens: 150 }
const reported: api.UsageRecord = {
  id: 'ul_reported', tenant_id: 't', started_at: '2026-11-10T12:00:00Z', finished_at: '2026-11-10T12:00:01Z', latency_ms: 900, plane: 'llmplex', operation: 'claude-code.api_request',
  model_id: 'claude-sonnet-4-5', principal_id: 'alice', status: 'ok', source: 'reported', streaming: false, requests: 1, harness: 'claude-code', harness_version: '2.1.322', session_id: 'sess-1',
  trace_id: TRACE, span_id: SPAN, tags: { experiment: 'arm-a' }, reported_attributes: { request_id: 'req_1', query_source: 'main' },
  usage: counters, cost: { basis: 'reported', currency: 'USD', amount: '4200', scale: 6 },
}
const managed: api.UsageRecord = { ...reported, id: 'ul_managed', source: 'managed', harness: undefined, harness_version: undefined, session_id: undefined, operation: 'openai.chat-completions', access_point_id: 'ide', cost: { basis: 'estimated', currency: 'USD', amount: '10', scale: 6 } }

describe('trace links and formatting', () => {
  it('builds a link only for an http(s) template that names the trace', () => {
    expect(api.traceURL('https://traces.example/trace/{trace_id}', TRACE)).toBe(`https://traces.example/trace/${TRACE}`)
    expect(api.traceURL('http://localhost:16686/trace/{trace_id}?x=1', TRACE)).toBe(`http://localhost:16686/trace/${TRACE}?x=1`)
    for (const bad of ['', undefined, 'https://traces.example/t', 'javascript:alert({trace_id})', 'data:text/html,{trace_id}', 'ftp://x/{trace_id}', 'https://user:pw@x/{trace_id}', '/relative/{trace_id}']) {
      expect(api.traceURL(bad, TRACE), String(bad)).toBeUndefined()
    }
    // A trace id that is not 32 hex characters never reaches a URL.
    for (const bad of [undefined, '', 'abc', TRACE.toUpperCase(), `${TRACE}/../x`, '0'.repeat(31) + '"']) expect(api.traceURL('https://t.example/{trace_id}', bad)).toBeUndefined()
  })
  it('shows a reported cost with its basis and never as unknown', () => {
    expect(api.formatCost(reported.cost)).toBe('USD 0.004200')
    expect(api.formatCost({ basis: 'unknown' })).toBe('unknown')
  })
  it('writes feedback without turning absence into a score', () => {
    expect(api.feedbackText(undefined)).toBe('—')
    expect(api.feedbackText({ records: 0, without_feedback: 3, positive: 0, negative: 0, neutral: 0, average_score: null })).toBe('none (3 without)')
    expect(api.feedbackText({ records: 2, without_feedback: 1, positive: 1, negative: 0, neutral: 1, average_score: 0.5 })).toBe('avg +0.50 (n=2; 1 without)')
  })
  it('names group fields, including tags and sources', () => {
    expect(groupName('harness')).toBe('Harness')
    expect(groupName('tag:experiment')).toBe('Tag experiment')
    expect(keyText('source', 'reported')).toMatch(/reported/)
    expect(keyText('source', 'managed')).toMatch(/managed/)
  })
})

describe('record detail', () => {
  const writeText = vi.fn((_text: string) => Promise.resolve())
  beforeEach(() => { writeText.mockClear(); Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true }) })

  it('shows trace and span ids with copy buttons, the source badge and the harness', async () => {
    render(<RecordDrawer record={reported} onClose={() => {}} traceUrlTemplate="https://traces.example/trace/{trace_id}" />)
    const ids = screen.getByLabelText('Correlation identifiers')
    expect(within(ids).getByText(TRACE)).toBeInTheDocument()
    expect(within(ids).getByText(SPAN)).toBeInTheDocument()
    fireEvent.click(within(ids).getByRole('button', { name: 'Copy Trace ID' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(TRACE))
    expect(await within(ids).findByText('Copied')).toBeInTheDocument()
    fireEvent.click(within(ids).getByRole('button', { name: 'Copy Span ID' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(SPAN))
    const link = within(ids).getByRole('link', { name: 'Open in trace viewer' })
    expect(link).toHaveAttribute('href', `https://traces.example/trace/${TRACE}`)
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    const drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    expect(within(drawer).getAllByText(/reported · claude-code/).length).toBeGreaterThan(0)
    expect(within(drawer).getByText(/did not measure this request/)).toBeInTheDocument()
    expect(within(drawer).getByText('claude-code 2.1.322')).toBeInTheDocument()
    expect(within(drawer).getByText('experiment=arm-a')).toBeInTheDocument()
    expect(within(drawer).getByText('query_source=main request_id=req_1')).toBeInTheDocument()
    expect(within(drawer).getByText('USD 0.004200')).toBeInTheDocument()
  })

  it('offers no link without a valid template, and says when copying fails', async () => {
    writeText.mockImplementationOnce(() => Promise.reject(new Error('denied')))
    render(<RecordDrawer record={reported} onClose={() => {}} traceUrlTemplate="javascript:alert({trace_id})" />)
    const ids = screen.getByLabelText('Correlation identifiers')
    expect(within(ids).queryByRole('link')).toBeNull()
    fireEvent.click(within(ids).getByRole('button', { name: 'Copy Trace ID' }))
    expect(await within(ids).findByText('Could not copy')).toBeInTheDocument()
  })

  it('shows a managed record as managed and a missing span as a dash', () => {
    render(<RecordDrawer record={{ ...managed, span_id: undefined }} onClose={() => {}} />)
    const ids = screen.getByLabelText('Correlation identifiers')
    expect(within(ids).queryByRole('button', { name: 'Copy Span ID' })).toBeNull()
    expect(screen.getAllByText('managed').length).toBeGreaterThan(0)
    expect(screen.queryByText(/did not measure/)).toBeNull()
  })

  it('shows feedback and lets the viewer rate when the deployment has it', () => {
    const rate = vi.fn()
    const { rerender } = render(<RecordDrawer record={managed} onClose={() => {}} feedback={null} onRate={rate} />)
    expect(screen.getByText(/No feedback yet/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Not helpful' }))
    expect(rate).toHaveBeenCalledWith(-1)
    rerender(<RecordDrawer record={managed} onClose={() => {}} feedback={{ id: 'fb', tenant_id: 't', record_id: 'ul_managed', by: 'alice', created_at: '2026-11-10T12:00:00Z', score: 1, label: 'helpful' }} />)
    expect(screen.getByText(/Latest: \+1 \(helpful\)/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Helpful' })).toBeNull()
  })

  it('renders the source badge', () => {
    render(<><SourceBadge r={{ source: 'reported', harness: 'codex' }} /><SourceBadge r={{ source: 'managed' }} /></>)
    expect(screen.getByText('reported · codex')).toBeInTheDocument()
    expect(screen.getByText('managed')).toBeInTheDocument()
  })
})

describe('usage page with reported records', () => {
  type Reply = { status?: number; body?: unknown }
  let routes: Record<string, (url: URL, init?: RequestInit) => Reply>
  let calls: { url: URL; init?: RequestInit }[]
  const summary = {
    group_by: ['model_id', 'source'], source_split: true, excluded_correlated_reported: 2, notes: ['Managed and reported records are separate groups and are not added together.'],
    records_scanned: 4, truncated: false,
    groups: [
      { key: { model_id: 'm', source: 'managed' }, records: 3, requests: 3, errors: 0, denied: 0, unknown_status: 0, tokens: { input_tokens: 30 }, tokens_unknown: {}, unknown_usage_records: 0, cost: [{ currency: 'USD', basis: 'estimated', amount: '10', scale: 6, records: 3 }], unknown_cost_records: 0, feedback: { records: 2, without_feedback: 1, positive: 1, negative: 0, neutral: 1, average_score: 0.5 } },
      { key: { model_id: 'm', source: 'reported' }, records: 1, requests: 1, errors: 0, denied: 0, unknown_status: 0, tokens: { input_tokens: 10 }, tokens_unknown: {}, unknown_usage_records: 0, cost: [{ currency: 'USD', basis: 'reported', amount: '4200', scale: 6, records: 1 }], unknown_cost_records: 0, feedback: { records: 0, without_feedback: 1, positive: 0, negative: 0, neutral: 0, average_score: null } },
    ],
  }
  beforeEach(() => {
    calls = []
    routes = {
      'GET /api/v1/usage/summary': () => ({ body: summary }),
      'GET /api/v1/usage/records': () => ({ body: { records: [reported, managed] } }),
      'GET /api/v1/usage/config': () => ({ body: { trace_url_template: 'https://traces.example/t/{trace_id}', feedback: true, feedback_comments: false, tag_keys_allowed: [] } }),
      'GET /api/v1/usage/records/ul_reported': () => ({ body: { ...reported, feedback: { id: 'fb', tenant_id: 't', record_id: 'ul_reported', by: 'alice', created_at: '2026-11-10T12:05:00Z', score: -1 } } }),
      'POST /api/v1/usage/records/ul_reported/feedback': () => ({ status: 201, body: { id: 'fb2', tenant_id: 't', record_id: 'ul_reported', by: 'alice', created_at: '2026-11-10T12:06:00Z', score: 1 } }),
      'GET /api/v1/usage/prices/': () => ({ body: [] }),
    }
    vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, 'http://localhost')
      calls.push({ url, init })
      const handler = routes[`${init?.method ?? 'GET'} ${url.pathname}`]
      if (!handler) return new Response(JSON.stringify({ message: 'no route' }), { status: 500 })
      const { status = 200, body } = handler(url, init)
      return new Response(JSON.stringify(body ?? {}), { status })
    }))
  })
  afterEach(() => vi.unstubAllGlobals())

  function show() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    return render(<MemoryRouter><QueryClientProvider client={client}><Usage /></QueryClientProvider></MemoryRouter>)
  }

  it('keeps managed and reported groups apart, with the notes and the exclusion count', async () => {
    show()
    const summarySection = await screen.findByRole('region', { name: 'Usage summary' })
    await within(summarySection).findByText(/not added together/)
    expect(within(summarySection).getByText(/2 reported records have been left out/)).toBeInTheDocument()
    expect(within(summarySection).getByText('Model · Source')).toBeInTheDocument()
    expect(within(summarySection).getByText('m · managed (through AIPlex)')).toBeInTheDocument()
    expect(within(summarySection).getByText('m · reported (client telemetry)')).toBeInTheDocument()
    expect(within(summarySection).getByText(/USD 0\.004200/)).toBeInTheDocument()
  })

  it('asks for feedback totals only when asked, and shows them without turning none into a score', async () => {
    show()
    const summarySection = await screen.findByRole('region', { name: 'Usage summary' })
    await within(summarySection).findByText(/not added together/)
    expect(calls.filter((c) => c.url.pathname === '/api/v1/usage/summary').every((c) => !c.url.searchParams.has('feedback'))).toBe(true)
    fireEvent.click(within(summarySection).getByLabelText('Show feedback'))
    await waitFor(() => expect(calls.some((c) => c.url.pathname === '/api/v1/usage/summary' && c.url.searchParams.get('feedback') === 'true')).toBe(true))
    expect(await within(summarySection).findByText('avg +0.50 (n=2; 1 without)')).toBeInTheDocument()
    expect(within(summarySection).getByText('none (1 without)')).toBeInTheDocument()
  })

  it('groups by a tag when a key is typed, and ignores an invalid key', async () => {
    show()
    const summarySection = await screen.findByRole('region', { name: 'Usage summary' })
    await within(summarySection).findByText(/not added together/)
    fireEvent.change(within(summarySection).getByLabelText('Group by tag'), { target: { value: 'Bad Key' } })
    expect(calls.some((c) => (c.url.searchParams.get('group_by') ?? '').includes('tag:'))).toBe(false)
    fireEvent.change(within(summarySection).getByLabelText('Group by tag'), { target: { value: 'experiment' } })
    await waitFor(() => expect(calls.some((c) => (c.url.searchParams.get('group_by') ?? '').endsWith('tag:experiment'))).toBe(true))
  })

  it('lists the source and harness, and opens the drawer with the trace link and the latest feedback', async () => {
    show()
    const requests = await screen.findByRole('region', { name: 'Requests' })
    await within(requests).findAllByText('claude-sonnet-4-5')
    expect(within(requests).getByText('reported · claude-code')).toBeInTheDocument()
    expect(within(requests).getByText('managed')).toBeInTheDocument()
    fireEvent.click(within(requests).getByRole('button', { name: 'Open details for record ul_reported' }))
    const drawer = await screen.findByRole('dialog', { name: 'Usage record detail' })
    const link = await within(drawer).findByRole('link', { name: 'Open in trace viewer' })
    expect(link).toHaveAttribute('href', `https://traces.example/t/${TRACE}`)
    expect(await within(drawer).findByText(/Latest: -1/)).toBeInTheDocument()
    fireEvent.click(await within(drawer).findByRole('button', { name: 'Helpful' }))
    await waitFor(() => expect(calls.some((c) => c.init?.method === 'POST' && c.url.pathname.endsWith('/feedback'))).toBe(true))
    const post = calls.find((c) => c.init?.method === 'POST')!
    expect(JSON.parse(String(post.init?.body))).toEqual({ score: 1 })
  })

  it('still lists records when the config and detail lookups fail', async () => {
    delete routes['GET /api/v1/usage/config']
    delete routes['GET /api/v1/usage/records/ul_reported']
    show()
    const requests = await screen.findByRole('region', { name: 'Requests' })
    fireEvent.click(await within(requests).findByRole('button', { name: 'Open details for record ul_reported' }))
    const drawer = await screen.findByRole('dialog', { name: 'Usage record detail' })
    expect(within(drawer).queryByRole('link', { name: 'Open in trace viewer' })).toBeNull()
    expect(within(drawer).getByText(TRACE)).toBeInTheDocument()
    expect(within(drawer).queryByRole('button', { name: 'Helpful' })).toBeNull()
  })
})
