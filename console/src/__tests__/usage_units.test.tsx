import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Usage from '../pages/Usage'
import { blankDraft, draftFromPrice, priceFromDraft } from '../pages/UsagePrices'
import * as api from '../api/usage'

vi.mock('../hooks/useAuth', () => { const session = { user: { sub: 'principal-a', roles: ['admin'], scopes: [] } }; return { useAuth: () => session, useOptionalAuth: () => session } })

const base = { tenant_id: 't', finished_at: '2026-11-09T10:01:30Z', latency_ms: 90000, streaming: false, source: 'managed', requests: 1, plane: 'llmplex' }
const nullCounters = { input_tokens: null, output_tokens: null, cache_read_tokens: null, cache_write_tokens: null, reasoning_tokens: null, total_tokens: null }
const records = [
  { ...base, id: 'ul_voice', started_at: '2026-11-09T10:00:00Z', model_id: 'rt-model', status: 'ok', session: true, batch: true,
    usage: { ...nullCounters, input_tokens: 10, cache_write_tokens: 0, input_audio_tokens: 0, input_text_tokens: 7, output_image_tokens: 4, voice_session_milliseconds: 90000, audio_input_milliseconds: 0 }, cost: { basis: 'unknown' } },
  { ...base, id: 'ul_image', started_at: '2026-11-09T09:00:00Z', model_id: 'img-model', status: 'ok', image_size: '1024x1024', image_quality: 'high',
    usage: { ...nullCounters, generated_images: 2 }, cost: { basis: 'unknown' } },
  { ...base, id: 'ul_shed', started_at: '2026-11-09T08:00:00Z', model_id: 'gpt-x', status: 'denied', error_class: 'capacity_shed', http_status: 429, usage: null, cost: { basis: 'unknown' } },
  { ...base, id: 'ul_cool', started_at: '2026-11-09T07:00:00Z', model_id: 'gpt-x', status: 'denied', error_class: 'capacity_cooldown', http_status: 429, usage: null, cost: { basis: 'unknown' } },
]
const summary = {
  group_by: ['model_id'], records_scanned: 6, truncated: false,
  groups: [
    { key: { model_id: 'rt-model' }, records: 3, requests: 3, errors: 0, denied: 0, unknown_status: 0, tokens: { input_tokens: 10, cache_write_tokens: 0 }, tokens_unknown: {}, unknown_usage_records: 0, cost: [], unknown_cost_records: 3,
      modality_tokens: { input_audio_tokens: 0, input_text_tokens: 7 }, units: { voice_session_milliseconds: 150000, generated_images: 0 }, unit_records: { voice_session_milliseconds: 2, generated_images: 1 }, batch_records: 1 },
    { key: { model_id: 'plain' }, records: 3, requests: 3, errors: 0, denied: 0, unknown_status: 0, tokens: { input_tokens: 5 }, tokens_unknown: {}, unknown_usage_records: 0, cost: [], unknown_cost_records: 3 },
  ],
}
const meters = { generated_images: { numerator: 40000, denominator: 1 }, input_audio_tokens: { numerator: 3, denominator: 1000 } }
const spec = { id: 'img', model_id: 'img-model', currency: 'USD', scale: 6, meters, batch: false, image_size: '1024x1024', image_quality: 'high' }

type Reply = { status?: number; body?: unknown }
let routes: Record<string, (url: URL, init?: RequestInit) => Reply>
let calls: { url: URL; init?: RequestInit }[]

beforeEach(() => {
  calls = []
  routes = {
    'GET /api/v1/usage/summary': () => ({ body: summary }),
    'GET /api/v1/usage/records': () => ({ body: { records } }),
    'GET /api/v1/usage/prices/': () => ({ body: [{ id: 'img', revision: 2, spec }] }),
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

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}><Usage /></QueryClientProvider></MemoryRouter>)
}
const region = (name: string) => screen.getByRole('region', { name })
const rowOf = (container: HTMLElement, text: string) => within(container).getAllByRole('row').find((r) => r.textContent?.includes(text))!
const cellsOf = (row: HTMLElement) => within(row).getAllByRole('cell').map((c) => c.textContent)

describe('formatting', () => {
  it('formats durations exactly and never turns unknown into zero', () => {
    expect(api.formatDuration(90000)).toBe('1m 30s')
    expect(api.formatDuration('3661001')).toBe('1h 1m 1s 1ms')
    expect(api.formatDuration(0)).toBe('0s')
    expect(api.formatDuration(null)).toBe('unknown')
    expect(api.formatDuration(undefined)).toBe('unknown')
    expect(api.formatDuration('9007199254740993')).toMatch(/ 993ms$/) // beyond the safe integer range, still exact
    expect(api.formatUnit('generated_images', 1, '1024x1024', 'high')).toBe('1 image (1024x1024 high)')
    expect(api.formatUnit('generated_images', 0)).toBe('0 images')
    expect(api.formatUnit('generated_images', undefined)).toBe('unknown')
    expect(api.formatUnit('voice_session_milliseconds', null)).toBe('unknown')
  })
  it('totals units only over groups that reported them', () => {
    const t = api.unitHeadlines(summary.groups as unknown as api.SummaryGroup[])
    expect(t.voice_session_milliseconds).toEqual({ total: 150000n, records: 2n })
    expect(t.generated_images).toEqual({ total: 0n, records: 1n })
    expect(t.video_output_milliseconds).toBeUndefined()
  })
})

describe('Usage records with units', () => {
  it('shows durations, image counts with size and quality, modality splits and the cache-write counter apart from tokens', async () => {
    show()
    const table = await within(region('Requests')).findByRole('table')
    const heads = within(table).getAllByRole('columnheader').map((h) => h.textContent)
    expect(heads).toEqual(expect.arrayContaining(['Cache write', 'Modality tokens', 'Units']))
    const cell = (id: string, header: string) => cellsOf(rowOf(table, id))[heads.indexOf(header)]
    expect(cell('rt-model', 'Units')).toBe('Audio input 0s, Voice session 1m 30s')
    expect(cell('rt-model', 'Modality tokens')).toBe('Input text 7, Input audio 0, Output image 4')
    expect(cell('rt-model', 'Tokens in')).toBe('10')
    expect(cell('rt-model', 'Cache write')).toBe('0') // a reported zero
    expect(cell('img-model', 'Units')).toBe('2 images (1024x1024 high)')
    // Unreported: unknown for token counters, absent for units and modalities; never 0.
    expect(cell('img-model', 'Tokens in')).toBe('unknown')
    expect(cell('img-model', 'Cache write')).toBe('unknown')
    expect(cell('img-model', 'Modality tokens')).toBe('—')
    expect(cell('gpt-x', 'Units')).toBe('—')
    expect(cell('gpt-x', 'Cache write')).toBe('unknown')
    expect(within(rowOf(table, 'rt-model')).getByTitle('Part of a provider batch job')).toHaveTextContent('batch')
    expect(within(rowOf(table, 'img-model')).queryByTitle('Part of a provider batch job')).not.toBeInTheDocument()
  })

  it('shows capacity_cooldown and capacity_shed as distinct denial reasons', async () => {
    show()
    const table = await within(region('Requests')).findByRole('table')
    const shed = rowOf(table, 'ul_shed') ?? within(table).getAllByRole('row').find((r) => r.querySelector('[title*="shed"]'))
    const reasons = within(table).getAllByText(/^capacity (cooldown|shed)$/)
    expect(reasons.map((r) => r.textContent)).toEqual(['capacity shed', 'capacity cooldown'])
    expect(reasons[0].getAttribute('title')).toMatch(/below the headroom threshold/)
    expect(reasons[1].getAttribute('title')).toMatch(/just throttled or overloaded/)
    expect(reasons[0].getAttribute('title')).not.toBe(reasons[1].getAttribute('title'))
    expect(shed).toBeDefined()
  })

  it('opens a drawer with units, modality splits, image dimensions and explanatory notes', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_voice' }))
    let drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    const fields = () => Object.fromEntries(within(drawer).getAllByRole('term').map((t) => [t.textContent, t.nextElementSibling?.textContent]))
    expect(fields()).toMatchObject({
      Batch: 'yes', 'Cache write tokens': '0', 'Input text tokens (part of input)': '7', 'Input audio tokens (part of input)': '0', 'Input video tokens (part of input)': 'unknown', 'Output image tokens (part of output)': '4',
      'Voice session (unit)': '1m 30s', 'Audio input (unit)': '0s', 'Video output (unit)': 'unknown', 'Generated images (unit)': 'unknown',
    })
    expect(drawer).toHaveTextContent('Provider batch.')
    fireEvent.click(within(drawer).getByRole('button', { name: 'Close' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_image' }))
    drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    expect(fields()).toMatchObject({ 'Image size': '1024x1024', 'Image quality': 'high', 'Generated images (unit)': '2 images (1024x1024 high)', Batch: 'no' })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Close' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_shed' }))
    drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    expect(within(drawer).getByRole('note')).toHaveTextContent('capacity shed.')
  })

  it('filters by batch, source, error class and usage bound, and passes them to the API', async () => {
    show()
    const form = await screen.findByRole('form', { name: 'Usage filters' })
    fireEvent.click(within(form).getByRole('button', { name: 'More filters' }))
    fireEvent.change(within(form).getByLabelText('Batch'), { target: { value: 'true' } })
    fireEvent.change(within(form).getByLabelText('Source'), { target: { value: 'reported' } })
    fireEvent.change(within(form).getByLabelText('Error class'), { target: { value: 'rate_limited' } })
    fireEvent.change(within(form).getByLabelText('Usage bound'), { target: { value: 'lower_bound' } })
    fireEvent.click(within(form).getByRole('button', { name: 'Apply filters' }))
    await waitFor(() => expect(calls.some((c) => c.url.pathname === '/api/v1/usage/records' && c.url.searchParams.get('batch') === 'true')).toBe(true))
    const last = [...calls].reverse().find((c) => c.url.pathname === '/api/v1/usage/records')!
    expect(last.url.searchParams.get('source')).toBe('reported')
    expect(last.url.searchParams.get('error_class')).toBe('rate_limited')
    expect(last.url.searchParams.get('usage_bound')).toBe('lower_bound')
  })

  it('offers batch, source, error class and usage bound as group-by keys', async () => {
    show()
    const group = await screen.findByRole('group', { name: 'Group by' })
    for (const label of ['Batch', 'Source', 'Error class', 'Usage bound']) expect(within(group).getByLabelText(label)).toBeInTheDocument()
  })
})

describe('Usage summary with units', () => {
  it('keeps units, modality tokens and batch counts out of the token totals', async () => {
    show()
    const totals = await screen.findByRole('region', { name: 'Usage totals' })
    const units = within(totals).getByRole('group', { name: 'Units' })
    expect(units).toHaveTextContent('Voice session 2m 30s')
    expect(units).toHaveTextContent('(2 records)')
    expect(units).toHaveTextContent('Generated images 0 images')
    expect(units).not.toHaveTextContent('Video output') // no record reported it
    expect(units).toHaveTextContent('never added to them')
    expect(units).toHaveTextContent('1 batch record')
    expect(within(totals).getByRole('group', { name: 'Tokens' })).toHaveTextContent('Input 15')
    const table = await within(region('Usage summary')).findByRole('table')
    const heads = within(table).getAllByRole('columnheader').map((h) => h.textContent)
    expect(heads).toEqual(expect.arrayContaining(['Cache write tokens', 'Modality tokens (part of input / output)', 'Units (not tokens)', 'Batch']))
    const rt = cellsOf(rowOf(table, 'rt-model'))
    const plain = cellsOf(rowOf(table, 'plain'))
    const at = (cells: (string | null)[], header: string) => cells[heads.indexOf(header) - 1] // the group name is a row header, not a cell
    expect(at(rt, 'Cache write tokens')).toBe('0')
    expect(at(rt, 'Modality tokens (part of input / output)')).toBe('Input text: 7Input audio: 0')
    expect(at(rt, 'Units (not tokens)')).toBe('Voice session: 2m 30s (2 records)Generated images: 0 images (1 record)')
    expect(at(rt, 'Batch')).toBe('1')
    expect(at(plain, 'Cache write tokens')).toBe('unknown')
    expect(at(plain, 'Modality tokens (part of input / output)')).toBe('—')
    expect(at(plain, 'Units (not tokens)')).toBe('—')
    expect(at(plain, 'Batch')).toBe('—')
  })
})

describe('Prices with unit and modality meters and selectors', () => {
  it('converts a draft with a unit meter, a modality meter and selectors into a price, and back', () => {
    const d = blankDraft()
    d.id = 'img'; d.model_id = 'img-model'; d.batch = 'false'; d.image_size = ' 1024x1024 '; d.image_quality = 'high'
    d.meters.generated_images = { numerator: '40000', denominator: '1' }
    d.meters.input_audio_tokens = { numerator: '3', denominator: '1000' }
    d.meters.voice_session_milliseconds = { numerator: '0', denominator: '1000' } // declared free
    const { price, errors } = priceFromDraft(d)
    expect(errors).toEqual([])
    expect(price).toEqual({ id: 'img', model_id: 'img-model', currency: 'USD', scale: 6, batch: false, image_size: '1024x1024', image_quality: 'high',
      meters: { generated_images: { numerator: 40000, denominator: 1 }, input_audio_tokens: { numerator: 3, denominator: 1000 }, voice_session_milliseconds: { numerator: 0, denominator: 1000 } } })
    expect(priceFromDraft(draftFromPrice(price!)).price).toEqual(price)
    // An unset selector is omitted, distinct from batch=false.
    const open = blankDraft(); open.id = 'p'; open.model_id = 'm'; open.meters.requests = { numerator: '1', denominator: '1' }
    expect(priceFromDraft(open).price).not.toHaveProperty('batch')
    expect(priceFromDraft(open).price).not.toHaveProperty('image_size')
    d.meters.video_output_milliseconds = { numerator: '1', denominator: '0' }
    expect(priceFromDraft(d).errors.join()).toMatch(/video_output_milliseconds: numerator/)
  })

  it('adds unit and modality meters and selectors in the form and saves them', async () => {
    routes['PUT /api/v1/usage/prices/new'] = () => ({ body: { id: 'new', revision: 1, spec } })
    show()
    await screen.findByRole('table', { name: 'Configured model prices' })
    expect(within(screen.getByRole('table', { name: 'Configured model prices' })).getByRole('row', { name: /img/ })).toHaveTextContent('batch excluded, size 1024x1024, quality high')
    fireEvent.click(screen.getByRole('button', { name: 'Add price' }))
    const form = screen.getByRole('form', { name: 'Add price' })
    for (const legend of ['Token and request meters', 'Modality token meters', 'Unit meters', 'Selectors (optional)']) expect(within(form).getByText(legend)).toBeInTheDocument()
    expect(form).toHaveTextContent('taken out of the parent total')
    expect(form).toHaveTextContent('never tokens')
    const set = (label: string, value: string) => fireEvent.change(within(form).getByLabelText(label), { target: { value } })
    set('Price ID', 'new'); set('Model ID', 'img-model')
    set('generated_images numerator', '40000'); set('generated_images denominator', '1')
    set('output_audio_tokens numerator', '6'); set('output_audio_tokens denominator', '1000')
    set('audio_input_milliseconds numerator', '1'); set('audio_input_milliseconds denominator', '1000')
    set('Batch', 'true'); set('Image size', '1024x1024'); set('Image quality', 'high')
    fireEvent.click(within(form).getByRole('button', { name: 'Save price' }))
    await waitFor(() => expect(calls.some((c) => c.init?.method === 'PUT')).toBe(true))
    const put = calls.find((c) => c.init?.method === 'PUT')!
    expect(put.init!.headers).toMatchObject({ 'If-Match': '"0"' })
    expect(JSON.parse(String(put.init!.body))).toEqual({
      id: 'new', model_id: 'img-model', currency: 'USD', scale: 6, batch: true, image_size: '1024x1024', image_quality: 'high',
      meters: { generated_images: { numerator: 40000, denominator: 1 }, output_audio_tokens: { numerator: 6, denominator: 1000 }, audio_input_milliseconds: { numerator: 1, denominator: 1000 } },
    })
  })
})

describe('Tool call response size and duration', () => {
  it('formats bytes and milliseconds exactly, with no unit conversion; unknown is never 0', () => {
    expect(api.formatUnit('tool_response_bytes', 2048)).toBe('2,048 bytes')
    expect(api.formatUnit('tool_response_bytes', 0)).toBe('0 bytes')
    expect(api.formatUnit('tool_response_bytes', null)).toBe('unknown')
    expect(api.formatUnit('tool_response_bytes', undefined)).toBe('unknown')
    expect(api.formatUnit('tool_duration_milliseconds', 90000)).toBe('90,000 ms')
    expect(api.formatUnit('tool_duration_milliseconds', 0)).toBe('0 ms')
    expect(api.formatUnit('tool_duration_milliseconds', null)).toBe('unknown')
    expect(api.formatUnit('tool_duration_milliseconds', undefined)).toBe('unknown')
    expect(api.UNIT_LABEL.tool_response_bytes).toBe('Tool response')
    expect(api.UNIT_LABEL.tool_duration_milliseconds).toBe('Tool duration')
  })
  it('never totals them: the Console does no arithmetic on these units', () => {
    const groups = [
      { key: { model_id: 'a' }, units: { tool_response_bytes: 10, tool_duration_milliseconds: 5 }, unit_records: { tool_response_bytes: 1, tool_duration_milliseconds: 1 } },
      { key: { model_id: 'b' }, units: { tool_response_bytes: 20 }, unit_records: { tool_response_bytes: 1 } },
    ]
    const t = api.unitHeadlines(groups as unknown as api.SummaryGroup[])
    expect(t.tool_response_bytes).toBeUndefined()
    expect(t.tool_duration_milliseconds).toBeUndefined()
  })
  it('shows present, zero and absent values on a usage record, and unknown in the detail', async () => {
    const tool = { ...base, id: 'ul_tool', started_at: '2026-11-09T11:00:00Z', model_id: 'tool-model', status: 'ok', source: 'managed', tool_name: 'search', usage: { ...nullCounters, tool_response_bytes: 0, tool_duration_milliseconds: 90000 }, cost: { basis: 'unknown' } }
    const absent = { ...base, id: 'ul_plain', started_at: '2026-11-09T12:00:00Z', model_id: 'plain-model', status: 'ok', source: 'managed', usage: { ...nullCounters, input_tokens: 1 }, cost: { basis: 'unknown' } }
    routes['GET /api/v1/usage/records'] = () => ({ body: { records: [tool, absent] } })
    show()
    const table = await within(region('Requests')).findByRole('table')
    const heads = within(table).getAllByRole('columnheader').map((h) => h.textContent)
    expect(cellsOf(rowOf(table, 'tool-model'))[heads.indexOf('Units')]).toBe('Tool response 0 bytes, Tool duration 90,000 ms')
    expect(cellsOf(rowOf(table, 'plain-model'))[heads.indexOf('Units')]).toBe('—')
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_tool' }))
    let drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    const fields = () => Object.fromEntries(within(drawer).getAllByRole('term').map((t) => [t.textContent, t.nextElementSibling?.textContent]))
    expect(fields()).toMatchObject({ 'Tool response (unit)': '0 bytes', 'Tool duration (unit)': '90,000 ms' })
    fireEvent.click(within(drawer).getByRole('button', { name: 'Close' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Open details for record ul_plain' }))
    drawer = screen.getByRole('dialog', { name: 'Usage record detail' })
    expect(fields()).toMatchObject({ 'Tool response (unit)': 'unknown', 'Tool duration (unit)': 'unknown' })
  })
})
