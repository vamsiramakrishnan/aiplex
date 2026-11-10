import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  compareBig, deletePrice, errorStatus, filterParams, formatCost, formatCount, formatMoney, formatTotal, listUsageRecords, parseUsageJSON, presetSince, putPrice, sumCosts, tokenHeadline, usageSummary,
  type ModelPrice, type SummaryGroup,
} from '../api/usage'

afterEach(() => vi.unstubAllGlobals())

describe('exact money formatting', () => {
  it('renders minor units at scale without floats', () => {
    expect(formatMoney('1135', 6)).toBe('0.001135')
    expect(formatMoney('0', 6)).toBe('0.000000')
    expect(formatMoney('0', 0)).toBe('0')
    expect(formatMoney('250', 2)).toBe('2.50')
    expect(formatMoney('007', 3)).toBe('0.007')
    expect(formatMoney('-1135', 6)).toBe('-0.001135')
    expect(formatMoney('-0', 2)).toBe('0.00')
    // Beyond 2^53: a float would corrupt the low digits.
    expect(formatMoney('123456789012345678901234567890', 6)).toBe('123456789012345678901234.567890')
    expect(formatMoney('9007199254740993', 0)).toBe('9007199254740993')
  })
  it('rejects anything but a decimal integer', () => {
    for (const bad of ['', '-', '1.5', '1e3', 'abc', ' 1']) expect(() => formatMoney(bad, 2)).toThrow()
    expect(() => formatMoney('1', -1)).toThrow()
  })
  it('shows currency for known costs and "unknown" - never zero - otherwise', () => {
    expect(formatCost({ basis: 'estimated', currency: 'USD', amount: '1135', scale: 6 })).toBe('USD 0.001135')
    expect(formatCost({ basis: 'settled', currency: 'EUR', amount: '0', scale: 2 })).toBe('EUR 0.00')
    expect(formatCost({ basis: 'unknown' })).toBe('unknown')
    expect(formatCost(undefined)).toBe('unknown')
    expect(formatCost({ basis: 'estimated', currency: 'USD', amount: 'x', scale: 2 })).toBe('invalid amount')
    expect(formatTotal({ currency: 'USD', amount: '1135', scale: 6 })).toBe('USD 0.001135')
  })
})

describe('unknown versus zero counts', () => {
  it('renders null and undefined as unknown and a reported zero as 0', () => {
    expect(formatCount(null)).toBe('unknown')
    expect(formatCount(undefined)).toBe('unknown')
    expect(formatCount(0)).toBe('0')
    expect(formatCount(1234567)).toBe('1,234,567')
    expect(formatCount('9007199254740993')).toBe('9,007,199,254,740,993')
  })
  it('keeps int64 values above 2^53 exact as strings and leaves safe numbers alone', () => {
    const decoded = parseUsageJSON('{"a":9007199254740993,"b":12,"c":-9007199254740993,"d":1.5,"e":"9007199254740993","f":null}') as Record<string, unknown>
    expect(decoded).toEqual({ a: '9007199254740993', b: 12, c: '-9007199254740993', d: 1.5, e: '9007199254740993', f: null })
  })
})

const group = (cost: SummaryGroup['cost'], tokens: SummaryGroup['tokens'] = {}, unknown: SummaryGroup['tokens_unknown'] = {}): SummaryGroup => ({
  key: {}, records: 1, requests: 1, errors: 0, denied: 0, unknown_status: 0, tokens, tokens_unknown: unknown, unknown_usage_records: 0, cost, unknown_cost_records: 0,
})

describe('summary aggregation', () => {
  it('sums costs exactly across scales but never across currencies or bases', () => {
    const totals = sumCosts([
      group([{ currency: 'USD', basis: 'estimated', amount: '1135', scale: 6, records: 2 }, { currency: 'USD', basis: 'settled', amount: '250', scale: 2, records: 1 }]),
      group([{ currency: 'USD', basis: 'estimated', amount: '5', scale: 2, records: 1 }, { currency: 'EUR', basis: 'settled', amount: '1', scale: 0, records: 1 }]),
    ])
    expect(totals.map((t) => `${t.currency}/${t.basis}/${formatTotal(t)}/${t.records}`)).toEqual([
      'EUR/settled/EUR 1/1',
      'USD/estimated/USD 0.051135/3',
      'USD/settled/USD 2.50/1',
    ])
  })
  it('distinguishes tokens no record reported from a reported zero', () => {
    const none = tokenHeadline([group([], {}, { output_tokens: 3 })], 'output_tokens')
    expect(none).toEqual({ reported: false, total: 0n, unknownRecords: 3n })
    const zero = tokenHeadline([group([], { output_tokens: 0 }), group([], { output_tokens: 5 }, { output_tokens: 2 })], 'output_tokens')
    expect(zero).toEqual({ reported: true, total: 5n, unknownRecords: 2n })
    expect(compareBig(1n, 2n)).toBe(-1)
  })
})

describe('time presets and query strings', () => {
  it('resolves a preset relative to a fixed clock and leaves "all" open', () => {
    const now = new Date('2026-11-09T12:00:00Z')
    expect(presetSince('24h', now)).toBe('2026-11-08T12:00:00.000Z')
    expect(presetSince('7d', now)).toBe('2026-11-02T12:00:00.000Z')
    expect(presetSince('all', now)).toBeUndefined()
  })
  it('omits empty filters', () => {
    expect(filterParams({ model_id: 'm', plane: '', since: undefined }).toString()).toBe('model_id=m')
  })
})

describe('transport', () => {
  function stub(status: number, body: unknown) {
    const fetchMock = vi.fn().mockResolvedValue(new Response(status === 204 ? null : JSON.stringify(body), { status }))
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }
  const price: ModelPrice = { id: 'p1', model_id: 'm', currency: 'USD', scale: 6, meters: { input_tokens: { numerator: 3, denominator: 1000 } } }

  it('sends filters, group_by, bucket, limit and cursor', async () => {
    const fetchMock = stub(200, { records: [] })
    await listUsageRecords({ model_id: 'm', since: '2026-11-08T00:00:00Z' }, 25, 'abc')
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/usage/records?model_id=m&since=2026-11-08T00%3A00%3A00Z&limit=25&cursor=abc')
    fetchMock.mockResolvedValue(new Response('{"group_by":[],"groups":[],"records_scanned":0,"truncated":false}'))
    await usageSummary({}, ['model_id', 'actor_id'], 'day')
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/usage/summary?group_by=model_id%2Cactor_id&bucket=day')
  })
  it('decodes huge counters exactly', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"records":[{"id":"r","requests":1,"usage":{"input_tokens":9007199254740993,"output_tokens":null},"cost":{"basis":"unknown"}}]}')))
    const page = await listUsageRecords({})
    expect(page.records[0].usage?.input_tokens).toBe('9007199254740993')
    expect(page.records[0].usage?.output_tokens).toBeNull()
  })
  it('writes prices with the expected revision and surfaces status codes', async () => {
    const fetchMock = stub(409, { code: 'REVISION_CONFLICT', message: 'resource revision changed' })
    const error = await putPrice(price, 3).catch((e: unknown) => e)
    expect(errorStatus(error)).toBe(409)
    expect((error as Error).message).toBe('resource revision changed')
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/usage/prices/p1')
    expect(init.method).toBe('PUT')
    expect((init.headers as Record<string, string>)['If-Match']).toBe('"3"')
    expect(JSON.parse(init.body as string)).toEqual(price)

    const del = stub(204, null)
    await deletePrice('p1', 7)
    expect((del.mock.calls[0][1] as RequestInit).method).toBe('DELETE')
    expect(((del.mock.calls[0][1] as RequestInit).headers as Record<string, string>)['If-Match']).toBe('"7"')
    expect(errorStatus(new Error('plain'))).toBeUndefined()
  })
})
