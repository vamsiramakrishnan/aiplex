import { authenticatedRequest } from './client'

// Usage ledger client. The ledger distinguishes unknown from zero: a null
// `usage`, a null counter, and a cost with basis "unknown" all mean the
// platform could not establish the value. Nothing here substitutes 0 for them.
const base = '/api/v1/usage'

export type Plane = 'llmplex' | 'mcplex' | 'a2aplex'
export type Status = 'ok' | 'error' | 'denied' | 'unknown'
/** reported is a cost the client's own telemetry reported (the sender's estimate); it is never a settled or an AIPlex-priced figure. */
export type Basis = 'settled' | 'estimated' | 'reported' | 'unknown'
// int64 counters beyond the browser-safe range arrive as decimal strings so
// no digit is lost; everything else is a plain number.
export type Count = number | string

export interface UsageCounters extends UnitUsage {
  input_tokens: Count | null
  output_tokens: Count | null
  cache_read_tokens: Count | null
  cache_write_tokens: Count | null
  reasoning_tokens: Count | null
  total_tokens: Count | null
}
/**
 * Per-modality token splits. They are SUBSETS of input_tokens / output_tokens,
 * never added to them. Omitted by the server when the provider did not report
 * them (unknown); a present 0 is a reported zero.
 */
export const MODALITY_COUNTERS = ['input_text_tokens', 'input_audio_tokens', 'input_image_tokens', 'input_video_tokens', 'output_text_tokens', 'output_audio_tokens', 'output_image_tokens'] as const
export type ModalityCounter = (typeof MODALITY_COUNTERS)[number]
/** Non-token units: milliseconds of audio or video, and a count of generated images. Never tokens. */
export const UNIT_COUNTERS = ['audio_input_milliseconds', 'voice_session_milliseconds', 'video_output_milliseconds', 'generated_images'] as const
export type UnitCounter = (typeof UNIT_COUNTERS)[number]
export const UNIT_LABEL: Record<UnitCounter, string> = {
  audio_input_milliseconds: 'Audio input', voice_session_milliseconds: 'Voice session', video_output_milliseconds: 'Video output', generated_images: 'Generated images',
}
export const MODALITY_LABEL: Record<ModalityCounter, string> = {
  input_text_tokens: 'Input text', input_audio_tokens: 'Input audio', input_image_tokens: 'Input image', input_video_tokens: 'Input video',
  output_text_tokens: 'Output text', output_audio_tokens: 'Output audio', output_image_tokens: 'Output image',
}
export type UnitUsage = Partial<Record<ModalityCounter | UnitCounter, Count | null>>

export const TOKEN_COUNTERS = ['input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens', 'reasoning_tokens', 'total_tokens'] as const
export type TokenCounter = (typeof TOKEN_COUNTERS)[number]

export interface UsageCost {
  basis: Basis
  currency?: string
  /** Decimal-integer string of minor units at `scale` decimal places. */
  amount?: string
  scale?: number
  /** The same amount as an exact decimal string. */
  amount_decimal?: string
  /** The price an estimate used. */
  price_id?: string
  /** The unrounded amount in minor units as a fraction ("37/4"), present when `amount` was rounded up. */
  exact?: string
  /** Set when the cost was unknown at write time and a price was applied afterwards (back-estimation). */
  backfilled_at?: string
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
  access_point_id?: string
  caller_kind?: string
  request_id?: string
  /** W3C trace id. For a managed record, the trace the request was part of; for a reported record, the id on the client's own log event. */
  trace_id?: string
  /** W3C span id: the CLIENT span of a managed exchange, or the id on a reported event. */
  span_id?: string
  quota_attempt_id?: string
  /** Reported records only: the client that sent its telemetry (claude-code, codex, gemini-cli) and its version. Labels the client chose, not identity. */
  harness?: string
  harness_version?: string
  session_id?: string
  client_request_id?: string
  /** Managed records: the span id of the traceparent the caller sent. */
  caller_span_id?: string
  /** A bounded, documented subset of the client's labels. Never identity. */
  reported_attributes?: Record<string, string>
  /** Experiment labels from access point configuration and allowlisted request headers. */
  tags?: Record<string, string>
  /** Request-hook summary; present only when a hook ran on the exchange. Never holds content or matched text. */
  hooks?: UsageHooks
  streaming: boolean
  /** True for a WebSocket session record (one record per session; usage is advisory). Absent on older servers and on non-session records. */
  session?: boolean
  status: Status
  http_status?: number
  error_class?: string
  source: 'managed' | 'reported'
  /** Provider rate-limit observation; shape is provider-defined, so it is shown as received. */
  provider_limits?: Record<string, unknown>
  /** True for an exchange that belongs to a provider batch job (its creation, or the results fetch that settled it). Omitted when false. */
  batch?: boolean
  /** Billing dimensions the provider reported for generated images, for example "1024x1024" and "high". */
  image_size?: string
  image_quality?: string
  /** The processing tier the provider reported (default, standard, flex, priority), or "mixed". */
  service_tier?: string
  /** Provider-side tool requests the provider reported, by kind (web_search, file_search, google_search, ...). */
  server_tools?: Record<string, Count>
  /** Set when the exchange ended without final usage evidence; the counters are then not a final exact count. */
  usage_bound?: UsageBound
  requests: Count
  usage: UsageCounters | null
  cost: UsageCost
}

export interface UsageHooks {
  decision: 'allow' | 'deny' | 'transform' | 'error' | 'skipped'
  fail_open_used?: boolean
  uninspected_parts?: Count
  results?: { id: string; type: string; phase: string; decision: string; rules?: { id: string; count: Count }[]; duration_ms?: Count; fail_open_used?: boolean }[]
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
  access_point_id?: string
  caller_kind?: string
  status?: string
  source?: string
  /** Failure class of an error record, for example rate_limited or invalid_request. */
  error_class?: string
  /** lower_bound, ambiguous or partial for records whose counters are not final; none for exact ones. */
  usage_bound?: string
  /** "true" for provider batch exchanges, "false" for everything else. */
  batch?: string
  /** Reported records from one client. */
  harness?: string
  /** allow, deny, transform, error, skipped or none. */
  hook_decision?: string
  since?: string
  until?: string
}

export const GROUP_FIELDS = ['plane', 'provider', 'model_id', 'tool_name', 'actor_id', 'principal_id', 'client_id', 'access_point_id', 'caller_kind', 'status', 'source', 'error_class', 'usage_bound', 'batch', 'hook_decision', 'harness'] as const
/** A record field, or a record tag written tag:<key>. */
export type GroupField = (typeof GROUP_FIELDS)[number] | `tag:${string}`
export type Bucket = '' | 'hour' | 'day'

export interface CostTotal { currency: string; basis: Basis; amount: string; scale: number; amount_decimal?: string; records: number }
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
  /** Records whose usage_bound is set. Their counters are included in `tokens` but are not final. Absent when zero. */
  bounded_usage_records?: Count
  lower_bound_records?: Count
  ambiguous_usage_records?: Count
  partial_usage_records?: Count
  cost: CostTotal[]
  unknown_cost_records: Count
  /** Sums of modality token splits (subsets of the token totals). A split missing here was reported by no record. */
  modality_tokens?: Partial<Record<ModalityCounter, Count>>
  /** Sums of non-token units, and how many records reported each. A unit missing here was reported by no record: unknown, not zero. */
  units?: Partial<Record<UnitCounter, Count>>
  unit_records?: Partial<Record<UnitCounter, Count>>
  /** Records of provider batch exchanges. Omitted when zero. */
  batch_records?: Count
  /** Latest-feedback totals, when the summary asked for them. Records without feedback are counted in without_feedback; they are not a neutral score. */
  feedback?: FeedbackTotals
}
export interface FeedbackTotals { records: Count; without_feedback: Count; positive: Count; negative: Count; neutral: Count; average_score: number | null }
export interface UsageSummary {
  group_by: GroupField[]; bucket?: string; groups: SummaryGroup[]; records_scanned: Count; truncated: boolean
  /** True when managed and reported records are separate groups (the source key was added). */
  source_split?: boolean
  /** Reported records left out because they describe a request that has a managed record. */
  excluded_correlated_reported?: Count
  /** Plain-words notes on how managed and reported records were treated. */
  notes?: string[]
}

export interface UsageFeedback { id: string; tenant_id: string; record_id: string; by: string; created_at: string; score: number; label?: string; comment?: string }
export interface UsageRecordDetail extends UsageRecord { feedback?: UsageFeedback }
/** Deployment settings the Console needs. */
export interface UsageConfig { trace_url_template: string; feedback: boolean; feedback_comments: boolean; tag_keys_allowed: string[] }

export const PRICE_METERS = ['input_tokens', 'output_tokens', 'cache_read_tokens', 'cache_write_tokens', 'reasoning_tokens', 'requests'] as const
/** Modality meters price a split at its own rate and remove it from its parent token total. */
export const MODALITY_METERS = MODALITY_COUNTERS
/** Unit meters price independent quantities (milliseconds, image counts). */
export const UNIT_METERS = UNIT_COUNTERS
export const ALL_PRICE_METERS = [...PRICE_METERS, ...MODALITY_METERS, ...UNIT_METERS] as const
export type PriceMeter = (typeof ALL_PRICE_METERS)[number]
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
  /** Selectors. A price without a selector matches any value; among matching prices the most selective wins, and an exact tie matches none. */
  batch?: boolean
  image_size?: string
  image_quality?: string
  /** Matches the tier the provider reported; a price without it covers standard-tier exchanges only. */
  service_tier?: string
  /** Context-length overrides: when a request's total input exceeds `above_input_tokens`, the whole request is priced at `meters`. */
  tiers?: PriceTier[]
  /** Provenance of a price imported from the bundled catalogue (a label only; it does not affect matching or cost). */
  source?: PriceSource
}
export interface PriceTier { above_input_tokens: number; meters: Partial<Record<string, PriceRate>> }
export interface PriceSource { catalog: string; commit?: string; retrieved_at?: string; source_url?: string }
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
export const getUsageRecord = (id: string) => usageRequest<UsageRecordDetail>(`/records/${encodeURIComponent(id)}`)
export const getUsageConfig = () => usageRequest<UsageConfig>('/config')
/** Adds feedback (append-only; the newest entry is the record's current feedback). score is between -1 and 1. */
export const postFeedback = (id: string, score: number, label = '') =>
  usageRequest<UsageFeedback>(`/records/${encodeURIComponent(id)}/feedback`, { method: 'POST', body: JSON.stringify(label ? { score, label } : { score }) })
export const usageSummary = (filter: UsageFilter, groupBy: readonly GroupField[], bucket: Bucket = '', options: { feedback?: boolean } = {}) => {
  const params = filterParams(filter)
  if (groupBy.length) params.set('group_by', groupBy.join(','))
  if (bucket) params.set('bucket', bucket)
  if (options.feedback) params.set('feedback', 'true')
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

export type UsageBound = 'lower_bound' | 'ambiguous' | 'partial'
export const USAGE_BOUND_LABEL: Record<UsageBound, string> = { lower_bound: 'lower bound', ambiguous: 'ambiguous', partial: 'partial' }
export const USAGE_BOUND_TEXT: Record<UsageBound, string> = {
  lower_bound: 'Lower bound: these counters are what was observed before the exchange ended. The true usage is at least this much, and may be more.',
  ambiguous: 'Ambiguous: this is a resumed session, and how its counters combine with earlier usage is unconfirmed. Do not treat them as exact.',
  partial: 'Partial: the provider also reported non-token usage (such as audio seconds) that this ledger cannot represent, so the token counters do not cover everything.',
}
/** Operations that are WebSocket sessions: metered as one request, with advisory usage. */
export const SESSION_OPERATIONS: readonly string[] = ['live.connect', 'openai.v1.realtime', 'openai.v1.responses-websocket']
export const isSessionOperation = (operation: string | undefined) => !!operation && SESSION_OPERATIONS.includes(operation)
/**
 * Whether a record is a WebSocket session. The server's `session` flag wins;
 * the operation ids are only a fallback for records that do not carry it
 * (the flag is omitted when false, and by servers that predate it).
 */
export const isSessionRecord = (record: Pick<UsageRecord, 'session' | 'operation'>) => record.session === true || (record.session === undefined && isSessionOperation(record.operation))

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
  if (!cost || (cost.basis !== 'settled' && cost.basis !== 'estimated' && cost.basis !== 'reported')) return UNKNOWN
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

/**
 * A count with its noun, pluralised: "1 record", "5 records". An unknown count
 * is "unknown records", never "0 records". The one place count strings are built.
 */
export function countNoun(value: Count | null | undefined, singular: string, plural = singular + 's'): string {
  const text = value === null || value === undefined ? '' : String(value)
  return `${formatCount(value)} ${text === '1' ? singular : plural}`
}

/** "has" for exactly one, "have" otherwise, to agree with countNoun. */
export const countVerb = (value: Count | null | undefined): string => (String(value) === '1' ? 'has' : 'have')

/** Milliseconds as a duration ("1m 30s"); an exact millisecond remainder is kept. null/undefined is unknown, never 0. */
export function formatDuration(ms: Count | null | undefined): string {
  if (ms === null || ms === undefined) return UNKNOWN
  const text = String(ms)
  if (!/^\d+$/.test(text)) return UNKNOWN
  let rest = BigInt(text)
  const parts: string[] = []
  for (const [unit, size] of [['d', 86_400_000n], ['h', 3_600_000n], ['m', 60_000n], ['s', 1000n], ['ms', 1n]] as const) {
    const n = rest / size
    rest %= size
    if (n > 0n) parts.push(`${n}${unit}`)
  }
  return parts.length ? parts.join(' ') : '0s'
}

/** One unit quantity: durations for milliseconds units, a count (with size and quality when known) for images. */
export function formatUnit(name: UnitCounter, value: Count | null | undefined, size?: string, quality?: string): string {
  if (value === null || value === undefined) return UNKNOWN
  if (name === 'generated_images') {
    const dims = [size, quality].filter(Boolean).join(' ')
    return `${formatCount(value)} ${String(value) === '1' ? 'image' : 'images'}${dims ? ` (${dims})` : ''}`
  }
  return formatDuration(value)
}

export interface UnitHeadline { total: bigint; records: bigint }
/** Per-unit totals over groups; units no record reported are absent. */
export function unitHeadlines(groups: readonly SummaryGroup[]): Partial<Record<UnitCounter, UnitHeadline>> {
  const out: Partial<Record<UnitCounter, UnitHeadline>> = {}
  for (const name of UNIT_COUNTERS) {
    const reported = groups.filter((g) => g.units?.[name] !== undefined)
    if (reported.length) out[name] = { total: sumCounts(reported.map((g) => g.units![name])), records: sumCounts(reported.map((g) => g.unit_records?.[name])) }
  }
  return out
}

/** Reasons the capacity policy rejected a request before it reached the provider. */
export const CAPACITY_REASONS: Record<string, { label: string; text: string }> = {
  capacity_cooldown: { label: 'capacity cooldown', text: 'Rejected by the capacity policy: the provider had just throttled or overloaded this surface and asked for a retry later, so the request was not sent.' },
  capacity_shed: { label: 'capacity shed', text: 'Rejected by the capacity policy: remaining provider capacity was below the headroom threshold and this caller\'s weight was below the minimum, so the request was shed.' },
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

// ---- trace links ----------------------------------------------------------

/**
 * The operator's trace viewer link for a trace id, or undefined. The template
 * comes from the server (AIPLEX_TRACE_URL_TEMPLATE) and must contain
 * {trace_id}; only http and https links are ever produced, so a template
 * (or a trace id) can never yield a javascript: or data: URL.
 */
export function traceURL(template: string | undefined, traceId: string | undefined): string | undefined {
  if (!template || !traceId || !/^[0-9a-f]{32}$/.test(traceId) || !template.includes('{trace_id}')) return undefined
  try {
    const url = new URL(template.split('{trace_id}').join(traceId))
    return (url.protocol === 'http:' || url.protocol === 'https:') && !url.username && !url.password ? url.toString() : undefined
  } catch { return undefined }
}

/** Mean feedback of the records that have some, and how many have none. No feedback is not a neutral score. */
export function feedbackText(f: FeedbackTotals | undefined): string {
  if (!f) return '—'
  const without = `${formatCount(f.without_feedback)} without`
  if (f.average_score === null || toBig(f.records) === 0n) return `none (${without})`
  const sign = f.average_score > 0 ? '+' : ''
  return `avg ${sign}${f.average_score.toFixed(2)} (n=${formatCount(f.records)}; ${without})`
}
