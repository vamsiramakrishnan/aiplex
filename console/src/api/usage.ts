import { authenticatedRequest } from './client'

// Usage ledger client. The ledger distinguishes unknown from zero: a null
// `usage`, a null counter, and a cost with basis "unknown" all mean the
// platform could not establish the value. Nothing here substitutes 0 for them.
const base = '/api/v1/usage'

export type Plane = 'llmplex' | 'mcplex' | 'a2aplex'
export type Status = 'ok' | 'error' | 'denied' | 'unknown'
export type Basis = 'settled' | 'estimated' | 'unknown'
// int64 counters beyond the browser-safe range arrive as decimal strings so
// no digit is lost; everything else is a plain number.
export type Count = number | string

export interface UsageCounters {
  input_tokens: Count | null
  output_tokens: Count | null
  cache_read_tokens: Count | null
  cache_write_tokens: Count | null
  reasoning_tokens: Count | null
  total_tokens: Count | null
}
export const TOKEN_COUNTERS = ['input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens', 'reasoning_tokens', 'total_tokens'] as const
export type TokenCounter = (typeof TOKEN_COUNTERS)[number]

export interface UsageCost {
  basis: Basis
  currency?: string
  /** Decimal-integer string of minor units at `scale` decimal places. */
  amount?: string
  scale?: number
}

export interface UsageRecord {
  id: string
  tenant_id: string
  started_at: string
  finished_at: string
  latency_ms: number
  plane: Plane
  provider?: string
  resource_id?: string
  operation?: string
  model_id?: string
  model_version?: string
  tool_name?: string
  principal_id?: string
  actor_id?: string
  client_id?: string
  request_id?: string
  trace_id?: string
  quota_attempt_id?: string
  streaming: boolean
  status: Status
  http_status?: number
  error_class?: string
  source: 'managed' | 'reported'
  requests: Count
  usage: UsageCounters | null
  cost: UsageCost
}

export interface UsagePage { records: UsageRecord[]; next_cursor?: string }

export interface UsageFilter {
  plane?: string
  provider?: string
  model_id?: string
  tool_name?: string
  actor_id?: string
  principal_id?: string
  client_id?: string
  status?: string
  source?: string
  since?: string
  until?: string
}

export const GROUP_FIELDS = ['plane', 'provider', 'model_id', 'tool_name', 'actor_id', 'principal_id', 'client_id', 'status'] as const
export type GroupField = (typeof GROUP_FIELDS)[number]
export type Bucket = '' | 'hour' | 'day'

export interface CostTotal { currency: string; basis: Basis; amount: string; scale: number; records: number }
export interface SummaryGroup {
  key: Partial<Record<GroupField, string>>
  bucket_start?: string
  records: Count
  requests: Count
  errors: Count
  denied: Count
  unknown_status: Count
  /** A counter missing here was reported by no record in the group. */
  tokens: Partial<Record<TokenCounter, Count>>
  tokens_unknown: Partial<Record<TokenCounter, Count>>
  unknown_usage_records: Count
  cost: CostTotal[]
  unknown_cost_records: Count
}
export interface UsageSummary { group_by: GroupField[]; bucket?: string; groups: SummaryGroup[]; records_scanned: Count; truncated: boolean }

export const PRICE_METERS = ['input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens', 'reasoning_tokens', 'requests'] as const
export type PriceMeter = (typeof PRICE_METERS)[number]
export interface PriceRate { numerator: number; denominator: number }
export interface ModelPrice {
  id: string
  model_id: string
  provider?: string
  currency: string
  scale: number
  meters: Partial<Record<PriceMeter, PriceRate>>
  effective_from?: string
  effective_until?: string
}
export interface PriceResource { id: string; revision: number; spec: ModelPrice }

// ---- exact decoding ------------------------------------------------------

const JSON_TOKEN = /"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g
const MAX_SAFE = BigInt(Number.MAX_SAFE_INTEGER)

/** Parses JSON, turning integers outside the browser-safe range into strings. */
export function parseUsageJSON(text: string): unknown {
  return JSON.parse(text.replace(JSON_TOKEN, (token) => {
    if (token[0] === '"' || !/^-?\d+$/.test(token)) return token
    const n = BigInt(token)
    return n > MAX_SAFE || n < -MAX_SAFE ? `"${token}"` : token
  }))
}

function usageRequest<T>(path: string, init?: RequestInit): Promise<T> {
  return authenticatedRequest<T>(`${base}${path}`, init, async (response) => parseUsageJSON(await response.text()) as T)
}

/** HTTP status of a failed request, when the transport recorded one. */
export function errorStatus(e: unknown): number | undefined {
  const status = (e as { status?: unknown } | null)?.status
  return typeof status === 'number' ? status : undefined
}

export function filterParams(filter: UsageFilter): URLSearchParams {
  const params = new URLSearchParams()
  for (const [name, value] of Object.entries(filter)) if (value) params.set(name, value)
  return params
}

export const listUsageRecords = (filter: UsageFilter, limit = 50, cursor = '') => {
  const params = filterParams(filter)
  params.set('limit', String(limit))
  if (cursor) params.set('cursor', cursor)
  return usageRequest<UsagePage>(`/records?${params}`)
}
export const getUsageRecord = (id: string) => usageRequest<UsageRecord>(`/records/${encodeURIComponent(id)}`)
export const usageSummary = (filter: UsageFilter, groupBy: readonly GroupField[], bucket: Bucket = '') => {
  const params = filterParams(filter)
  if (groupBy.length) params.set('group_by', groupBy.join(','))
  if (bucket) params.set('bucket', bucket)
  return usageRequest<UsageSummary>(`/summary?${params}`)
}
export const listPrices = () => usageRequest<PriceResource[]>('/prices/')
export const getPrice = (id: string) => usageRequest<PriceResource>(`/prices/${encodeURIComponent(id)}`)
/** Revision 0 creates; any other value must be the current revision. */
export const putPrice = (price: ModelPrice, revision: number) =>
  usageRequest<PriceResource>(`/prices/${encodeURIComponent(price.id)}`, { method: 'PUT', headers: { 'If-Match': `"${revision}"` }, body: JSON.stringify(price) })
export const deletePrice = (id: string, revision: number) =>
  usageRequest<void>(`/prices/${encodeURIComponent(id)}`, { method: 'DELETE', headers: { 'If-Match': `"${revision}"` } })

// ---- time ranges ---------------------------------------------------------

export const TIME_PRESETS = [
  { id: '1h', label: 'Last hour', ms: 3_600_000 },
  { id: '24h', label: 'Last 24 hours', ms: 86_400_000 },
  { id: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
  { id: '30d', label: 'Last 30 days', ms: 30 * 86_400_000 },
  { id: 'all', label: 'All retained', ms: 0 },
] as const
export type TimePreset = (typeof TIME_PRESETS)[number]['id']

/** RFC3339 start of the preset window, or undefined for "all". */
export function presetSince(id: TimePreset, now: Date = new Date()): string | undefined {
  const preset = TIME_PRESETS.find((p) => p.id === id)
  return preset && preset.ms > 0 ? new Date(now.getTime() - preset.ms).toISOString() : undefined
}

// ---- exact formatting ----------------------------------------------------

export const UNKNOWN = 'unknown'

/**
 * Renders minor units at `scale` decimal places using string arithmetic only:
 * ("1135", 6) -> "0.001135". Throws on anything but a decimal integer.
 */
export function formatMoney(amount: string, scale: number): string {
  if (!/^-?\d+$/.test(amount) || !Number.isInteger(scale) || scale < 0 || scale > 36) throw new Error(`invalid money amount ${JSON.stringify(amount)} at scale ${scale}`)
  const negative = amount.startsWith('-')
  let digits = BigInt(amount.replace('-', '')).toString()
  if (scale > 0 && digits.length <= scale) digits = digits.padStart(scale + 1, '0')
  const whole = scale > 0 ? digits.slice(0, digits.length - scale) : digits
  const fraction = scale > 0 ? digits.slice(digits.length - scale) : ''
  const zero = /^0*$/.test(whole + fraction)
  return `${negative && !zero ? '-' : ''}${whole}${fraction ? `.${fraction}` : ''}`
}

/** "USD 0.001135" with its basis, or "unknown". Never zero for unknown. */
export function formatCost(cost: UsageCost | null | undefined): string {
  if (!cost || (cost.basis !== 'settled' && cost.basis !== 'estimated')) return UNKNOWN
  try { return `${cost.currency ?? '?'} ${formatMoney(cost.amount ?? '', cost.scale ?? 0)}` } catch { return 'invalid amount' }
}

export function formatTotal(total: Pick<CostTotal, 'currency' | 'amount' | 'scale'>): string {
  try { return `${total.currency} ${formatMoney(total.amount, total.scale)}` } catch { return 'invalid amount' }
}

const group3 = (digits: string) => digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',')

/** Thousands-separated integer; null/undefined render as "unknown", never 0. */
export function formatCount(value: Count | null | undefined): string {
  if (value === null || value === undefined) return UNKNOWN
  const text = typeof value === 'string' ? value : String(value)
  if (!/^-?\d+$/.test(text)) return UNKNOWN
  return text.startsWith('-') ? `-${group3(text.slice(1))}` : group3(text)
}

export const toBig = (value: Count): bigint => BigInt(value)
export const sumCounts = (values: readonly (Count | null | undefined)[]): bigint => values.reduce<bigint>((sum, v) => (v === null || v === undefined ? sum : sum + toBig(v)), 0n)
export const compareBig = (a: bigint, b: bigint) => (a < b ? -1 : a > b ? 1 : 0)

const pow10 = (n: number) => 10n ** BigInt(n)

/** Exact per-currency, per-basis totals across groups; never merges currencies or bases. */
export function sumCosts(groups: readonly Pick<SummaryGroup, 'cost'>[]): CostTotal[] {
  const totals = new Map<string, { currency: string; basis: Basis; amount: bigint; scale: number; records: number }>()
  for (const group of groups) {
    for (const cost of group.cost) {
      const key = `${cost.currency}\u0000${cost.basis}`
      const amount = BigInt(cost.amount)
      const current = totals.get(key)
      if (!current) { totals.set(key, { currency: cost.currency, basis: cost.basis, amount, scale: cost.scale, records: Number(cost.records) }); continue }
      const scale = Math.max(current.scale, cost.scale)
      current.amount = current.amount * pow10(scale - current.scale) + amount * pow10(scale - cost.scale)
      current.scale = scale
      current.records += Number(cost.records)
    }
  }
  return [...totals.values()]
    .map((t) => ({ currency: t.currency, basis: t.basis, amount: t.amount.toString(), scale: t.scale, records: t.records }))
    .sort((a, b) => a.currency.localeCompare(b.currency) || a.basis.localeCompare(b.basis))
}

export interface TokenHeadline { reported: boolean; total: bigint; unknownRecords: bigint }
/** Sum of one counter over groups, plus how many records did not report it. */
export function tokenHeadline(groups: readonly SummaryGroup[], name: TokenCounter): TokenHeadline {
  const reported = groups.some((g) => g.tokens[name] !== undefined)
  return { reported, total: sumCounts(groups.map((g) => g.tokens[name])), unknownRecords: sumCounts(groups.map((g) => g.tokens_unknown[name])) }
}
