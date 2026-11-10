import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import * as api from '../api/usage'
import UsagePrices from './UsagePrices'
import { useAccess } from '../hooks/useAccess'
import { ErrorPanel } from '../components/UsageError'
import { useFocusTrap } from '../hooks/useFocusTrap'
import ShortId from '../components/ShortId'

const field = 'w-full rounded border border-gray-300 bg-transparent px-3 py-2 text-sm dark:border-gray-700'
const button = 'rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900'
const card = 'rounded border border-gray-200 p-4 dark:border-gray-800'
const muted = 'text-xs text-gray-600 dark:text-gray-400'

export const GROUP_LABELS: Record<api.GroupField, string> = {
  plane: 'Plane', provider: 'Provider', model_id: 'Model', tool_name: 'Tool', actor_id: 'Agent', principal_id: 'Principal', client_id: 'Client', access_point_id: 'Access point', caller_kind: 'Caller kind', status: 'Status', source: 'Source', error_class: 'Error class', usage_bound: 'Usage bound', batch: 'Batch',
}

// ---- filters -------------------------------------------------------------

export interface FilterDraft {
  preset: api.TimePreset
  plane: string
  model_id: string
  tool_name: string
  actor_id: string
  principal_id: string
  client_id: string
  access_point_id: string
  caller_kind: string
  status: string
  source: string
  error_class: string
  usage_bound: string
  batch: string
}
export const emptyDraft: FilterDraft = { preset: '24h', plane: '', model_id: '', tool_name: '', actor_id: '', principal_id: '', client_id: '', access_point_id: '', caller_kind: '', status: '', source: '', error_class: '', usage_bound: '', batch: '' }

/** Turns the form into API filters; the time window is resolved once, at apply time. */
export function buildFilter(draft: FilterDraft, now: Date = new Date()): api.UsageFilter {
  const filter: api.UsageFilter = { since: api.presetSince(draft.preset, now) }
  for (const name of ['plane', 'model_id', 'tool_name', 'actor_id', 'principal_id', 'client_id', 'access_point_id', 'caller_kind', 'status', 'source', 'error_class', 'usage_bound', 'batch'] as const) {
    const value = draft[name].trim()
    if (value) filter[name] = value
  }
  return filter
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="block text-sm"><span className="mb-1 block text-xs text-gray-600 dark:text-gray-400">{label}</span>{children}</label>
}

/** Filters kept behind "More filters"; the six most used stay visible. */
export const MORE_FILTERS = ['actor_id', 'principal_id', 'client_id', 'caller_kind', 'source', 'error_class', 'usage_bound', 'batch'] as const
/** How many of the less common filters hold a value. */
export const activeMoreFilters = (draft: FilterDraft): number => MORE_FILTERS.filter((n) => draft[n].trim() !== '').length

function FilterBar({ draft, onChange, onApply }: { draft: FilterDraft; onChange: (next: FilterDraft) => void; onApply: () => void }) {
  const set = (name: keyof FilterDraft) => (e: { target: { value: string } }) => onChange({ ...draft, [name]: e.target.value })
  const submit = (e: FormEvent) => { e.preventDefault(); onApply() }
  const [expanded, setExpanded] = useState(false)
  const active = activeMoreFilters(draft)
  // Collapsed filters that hold a value are still applied; the count stays on the toggle.
  const showMore = expanded
  return <form aria-label="Usage filters" onSubmit={submit} className={`${card} grid gap-3 sm:grid-cols-2 lg:grid-cols-4`}>
    <Field label="Time range"><select className={field} value={draft.preset} onChange={set('preset')}>{api.TIME_PRESETS.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}</select></Field>
    <Field label="Plane"><select className={field} value={draft.plane} onChange={set('plane')}><option value="">All planes</option><option value="llmplex">LLMPlex (models)</option><option value="mcplex">MCPlex (tools)</option><option value="a2aplex">A2APlex (agents)</option></select></Field>
    <Field label="Status"><select className={field} value={draft.status} onChange={set('status')}><option value="">Any status</option><option value="ok">ok</option><option value="error">error</option><option value="denied">denied</option><option value="unknown">unknown</option></select></Field>
    <Field label="Model"><input className={field} value={draft.model_id} onChange={set('model_id')} placeholder="e.g. gemini-2.5-flash" /></Field>
    <Field label="Tool"><input className={field} value={draft.tool_name} onChange={set('tool_name')} placeholder="e.g. search_curriculum" /></Field>
    <Field label="Access point"><input className={field} value={draft.access_point_id} onChange={set('access_point_id')} placeholder="access point ID" /></Field>
    <div className="sm:col-span-2 lg:col-span-4">
      <button type="button" aria-expanded={showMore} aria-controls="usage-more-filters" className="text-sm underline" onClick={() => setExpanded(!showMore)}>
        {showMore ? 'Fewer filters' : 'More filters'}{active > 0 ? ` (${active} active)` : ''}
      </button>
    </div>
    {showMore && <div id="usage-more-filters" role="group" aria-label="More filters" className="grid gap-3 sm:col-span-2 lg:col-span-4 sm:grid-cols-2 lg:grid-cols-4">
    <Field label="Agent"><input className={field} value={draft.actor_id} onChange={set('actor_id')} placeholder="agent / actor ID" /></Field>
    <Field label="Principal"><input className={field} value={draft.principal_id} onChange={set('principal_id')} placeholder="user / principal ID" /></Field>
    <Field label="Client"><input className={field} value={draft.client_id} onChange={set('client_id')} placeholder="client ID" /></Field>
    <Field label="Caller kind"><select className={field} value={draft.caller_kind} onChange={set('caller_kind')}><option value="">Any caller kind</option><option value="agent">agent</option><option value="user">user</option><option value="client">client</option></select></Field>
    <Field label="Source"><select className={field} value={draft.source} onChange={set('source')}><option value="">Any source</option><option value="managed">managed (through AIPlex)</option><option value="reported">reported (by the caller)</option></select></Field>
    <Field label="Error class"><input className={field} value={draft.error_class} onChange={set('error_class')} placeholder="e.g. rate_limited" /></Field>
    <Field label="Usage bound"><select className={field} value={draft.usage_bound} onChange={set('usage_bound')}><option value="">Any</option><option value="lower_bound">lower bound</option><option value="ambiguous">ambiguous</option><option value="partial">partial</option><option value="none">exact (no bound)</option></select></Field>
    <Field label="Batch"><select className={field} value={draft.batch} onChange={set('batch')}><option value="">Batch and not</option><option value="true">Provider batches only</option><option value="false">Not batches</option></select></Field>
    </div>}
    <div className="flex items-end gap-3 sm:col-span-2 lg:col-span-4"><button type="submit" className={button}>Apply filters</button><button type="button" className="text-sm underline" onClick={() => { onChange(emptyDraft); }}>Reset fields</button></div>
  </form>
}

// ---- shared states -------------------------------------------------------

// ---- summary -------------------------------------------------------------

type SortKey = 'requests' | 'input' | 'output' | 'cost'
const SCALE_FLOOR = 36

function tokenCell(g: api.SummaryGroup, name: api.TokenCounter): string {
  const sum = g.tokens[name]
  if (sum === undefined) return api.UNKNOWN
  const missing = g.tokens_unknown[name]
  const text = missing !== undefined && api.toBig(missing) > 0n ? `${api.formatCount(sum)} (+${api.formatCount(missing)} unknown)` : api.formatCount(sum)
  // The sum includes counters that are not final; never show it bare.
  return boundedOf(g) > 0n ? `${text} (incl. ${api.formatCount(boundedOf(g).toString())} bounded)` : text
}

const boundedOf = (g: api.SummaryGroup): bigint => api.toBig(g.bounded_usage_records ?? 0)

/** Modality token splits of a group: subsets of input/output tokens. Splits no record reported are not listed (unknown, not 0). */
function modalityCell(g: api.SummaryGroup): ReactNode {
  const present = api.MODALITY_COUNTERS.filter((n) => g.modality_tokens?.[n] !== undefined)
  if (!present.length) return '—'
  return present.map((n) => <div key={n}>{api.MODALITY_LABEL[n]}: {api.formatCount(g.modality_tokens![n])}</div>)
}
/** Units of a group, each with the number of records that reported it. Units no record reported are not listed. */
function unitsCell(g: api.SummaryGroup): ReactNode {
  const present = api.UNIT_COUNTERS.filter((n) => g.units?.[n] !== undefined)
  if (!present.length) return '—'
  return present.map((n) => <div key={n}>{api.UNIT_LABEL[n]}: {api.formatUnit(n, g.units![n])} <span className={muted}>({api.countNoun(g.unit_records?.[n], 'record')})</span></div>)
}
/** "2 lower bound, 1 partial" for the non-zero bound kinds of a group. */
function boundedBreakdown(groups: readonly api.SummaryGroup[]): string {
  const kinds: [string, (g: api.SummaryGroup) => api.Count | undefined][] = [['lower bound', (g) => g.lower_bound_records], ['ambiguous', (g) => g.ambiguous_usage_records], ['partial', (g) => g.partial_usage_records]]
  return kinds.map(([label, read]) => [label, api.sumCounts(groups.map(read))] as const).filter(([, n]) => n > 0n).map(([label, n]) => `${api.formatCount(n.toString())} ${label}`).join(', ')
}

/** Comparable value of a group for one sort key; null means unknown and always sorts last. */
function sortValue(g: api.SummaryGroup, key: SortKey, currency: string): bigint | null {
  if (key === 'requests') return api.toBig(g.requests)
  if (key === 'input' || key === 'output') {
    const v = g.tokens[key === 'input' ? 'input_tokens' : 'output_tokens']
    return v === undefined ? null : api.toBig(v)
  }
  const costs = g.cost.filter((c) => c.currency === currency)
  if (!costs.length) return null
  return costs.reduce((sum, c) => sum + BigInt(c.amount) * 10n ** BigInt(SCALE_FLOOR - c.scale), 0n)
}

/** Display text of one group key; batch is a boolean the server sends as "true" or "false". */
export function keyText(field: api.GroupField, value: string | undefined): string {
  if (field === 'batch') return value === 'true' ? 'provider batch' : value === 'false' ? 'not a batch' : '(none)'
  if (!value && field === 'model_id') return '(no model)'
  if (!value && field === 'tool_name') return '(no tool)'
  return value || '(none)'
}
function groupLabel(g: api.SummaryGroup, fields: readonly api.GroupField[]): string {
  return fields.map((f) => keyText(f, g.key[f])).join(' · ') || 'All records'
}

function Headline({ summary }: { summary: api.UsageSummary }) {
  const costs = api.sumCosts(summary.groups)
  const input = api.tokenHeadline(summary.groups, 'input_tokens')
  const output = api.tokenHeadline(summary.groups, 'output_tokens')
  const requests = api.sumCounts(summary.groups.map((g) => g.requests))
  const errors = api.sumCounts(summary.groups.map((g) => g.errors))
  const denied = api.sumCounts(summary.groups.map((g) => g.denied))
  const unknownCost = api.sumCounts(summary.groups.map((g) => g.unknown_cost_records))
  const unknownUsage = api.sumCounts(summary.groups.map((g) => g.unknown_usage_records))
  const bounded = api.sumCounts(summary.groups.map((g) => g.bounded_usage_records))
  const units = api.unitHeadlines(summary.groups)
  const unitNames = api.UNIT_COUNTERS.filter((n) => units[n])
  const batchRecords = api.sumCounts(summary.groups.map((g) => g.batch_records))
  const tokenLine = (label: string, t: api.TokenHeadline) => <p>{label} <strong>{t.reported ? api.formatCount(t.total.toString()) : api.UNKNOWN}</strong>{t.reported && t.unknownRecords > 0n && <span className={muted}> (+{api.countNoun(t.unknownRecords.toString(), 'record')} unreported)</span>}</p>
  return <section aria-label="Usage totals" className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
    <div className={card} role="group" aria-label="Requests"><p className={muted}>Requests</p><p className="mt-1 text-2xl font-semibold">{api.formatCount(requests.toString())}</p><p className={`${muted} mt-1`}>{api.countNoun(errors.toString(), 'error')} · {api.formatCount(denied.toString())} denied · {api.countNoun(summary.records_scanned, 'record')} scanned</p></div>
    <div className={card} role="group" aria-label="Tokens"><p className={muted}>Tokens</p><div className="mt-1 text-lg">{tokenLine('Input', input)}{tokenLine('Output', output)}</div>{unknownUsage > 0n && <p className={`${muted} mt-1`}>{api.countNoun(unknownUsage.toString(), 'record')} reported no usage at all</p>}{bounded > 0n && <p role="note" className="mt-1 text-xs text-status-warning-fg">Not final: includes {api.countNoun(bounded.toString(), 'bounded record')} ({boundedBreakdown(summary.groups)}). Their counters are summed as reported.</p>}</div>
    {(unitNames.length > 0 || batchRecords > 0n) && <div className={card} role="group" aria-label="Units"><p className={muted}>Units (not tokens)</p>
      {unitNames.length === 0 ? <p className="mt-1 text-sm">No unit usage reported</p> : <ul className="mt-1 space-y-1">{unitNames.map((n) => <li key={n}>{api.UNIT_LABEL[n]} <strong>{api.formatUnit(n, units[n]!.total.toString())}</strong> <span className={muted}>({api.countNoun(units[n]!.records.toString(), 'record')})</span></li>)}</ul>}
      <p className={`${muted} mt-1`}>Durations and image counts are separate from token totals and are never added to them.{batchRecords > 0n && ` ${api.countNoun(batchRecords.toString(), 'batch record')}.`}</p></div>}
    <div className={card} role="group" aria-label="Cost"><p className={muted}>Cost (per currency and basis)</p>
      {costs.length === 0 ? <p className="mt-1 text-lg">unknown</p> : <ul className="mt-1 space-y-1">{costs.map((c) => <li key={`${c.currency}-${c.basis}`} className="text-lg"><strong>{api.formatTotal(c)}</strong> <span className={`rounded px-1.5 py-0.5 text-xs ${c.basis === 'settled' ? 'bg-status-ok-bg text-status-ok-fg' : 'bg-status-warning-bg text-status-warning-fg'}`}>{c.basis}</span> <span className={muted}>{api.countNoun(c.records, 'record')}</span></li>)}</ul>}
      {unknownCost > 0n && <p className={`${muted} mt-1`}>{api.countNoun(unknownCost.toString(), 'record')} {api.countVerb(unknownCost.toString())} unknown cost (no price configured and not settled by quota); they are not counted above.</p>}
    </div>
  </section>
}

function Breakdown({ summary }: { summary: api.UsageSummary }) {
  const [sort, setSort] = useState<{ key: SortKey; dir: 'asc' | 'desc' }>({ key: 'requests', dir: 'desc' })
  const currencies = useMemo(() => [...new Set(summary.groups.flatMap((g) => g.cost.map((c) => c.currency)))].sort(), [summary])
  const [currency, setCurrency] = useState('')
  const sortCurrency = currencies.includes(currency) ? currency : currencies[0] ?? ''
  const rows = useMemo(() => {
    const sign = sort.dir === 'asc' ? 1 : -1
    return summary.groups.map((g) => ({ g, v: sortValue(g, sort.key, sortCurrency) })).sort((a, b) => {
      if (a.v === null || b.v === null) return a.v === b.v ? 0 : a.v === null ? 1 : -1
      return sign * api.compareBig(a.v, b.v)
    })
  }, [summary, sort, sortCurrency])
  const maxRequests = summary.groups.reduce((m, g) => Math.max(m, Number(g.requests)), 0)
  const bucketed = Boolean(summary.bucket)
  const header = (key: SortKey, label: string) => <th scope="col" aria-sort={sort.key === key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'} className="py-2 pr-4">
    <button type="button" className="font-medium underline-offset-2 hover:underline" aria-label={`Sort by ${label}`} onClick={() => setSort((s) => ({ key, dir: s.key === key && s.dir === 'desc' ? 'asc' : 'desc' }))}>{label}{sort.key === key ? (sort.dir === 'asc' ? ' ▲' : ' ▼') : ''}</button>
  </th>
  return <div className="space-y-2">
    {currencies.length > 1 && <Field label="Sort cost by currency"><select className={`${field} max-w-xs`} value={sortCurrency} onChange={(e) => setCurrency(e.target.value)}>{currencies.map((c) => <option key={c}>{c}</option>)}</select></Field>}
    <div className="overflow-x-auto"><table className="w-full text-left text-sm">
      <caption className="sr-only">Usage breakdown</caption>
      <thead><tr className="border-b border-gray-200 dark:border-gray-800">
        <th scope="col" className="py-2 pr-4">{summary.group_by.length ? summary.group_by.map((f) => GROUP_LABELS[f]).join(' · ') : 'Group'}</th>
        {bucketed && <th scope="col" className="py-2 pr-4">Bucket</th>}
        {header('requests', 'Requests')}{header('input', 'Input tokens')}{header('output', 'Output tokens')}<th scope="col" className="py-2 pr-4">Cache write tokens</th><th scope="col" className="py-2 pr-4">Modality tokens (part of input / output)</th><th scope="col" className="py-2 pr-4">Units (not tokens)</th><th scope="col" className="py-2 pr-4">Batch</th>{header('cost', 'Cost')}
        <th scope="col" className="py-2 pr-4">Errors / denied</th><th scope="col" className="py-2">Unknown</th>
      </tr></thead>
      <tbody>{rows.map(({ g }, i) => {
        const width = maxRequests > 0 ? Math.max(2, Math.round((Number(g.requests) / maxRequests) * 100)) : 0
        return <tr key={`${groupLabel(g, summary.group_by)}-${g.bucket_start ?? ''}-${i}`} className="border-t border-gray-200 align-top dark:border-gray-800">
          <th scope="row" className="py-2 pr-4 font-normal">{groupLabel(g, summary.group_by)}</th>
          {bucketed && <td className="pr-4 font-mono text-xs">{g.bucket_start ?? '—'}</td>}
          <td className="pr-4"><div>{api.formatCount(g.requests)}</div><div aria-hidden="true" className="mt-1 h-1.5 w-32 rounded bg-gray-100 dark:bg-gray-800"><div className="h-1.5 rounded bg-brand-500" style={{ width: `${width}%` }} /></div></td>
          <td className="pr-4">{tokenCell(g, 'input_tokens')}</td>
          <td className="pr-4">{tokenCell(g, 'output_tokens')}</td>
          <td className="pr-4">{tokenCell(g, 'cache_write_tokens')}</td>
          <td className="pr-4 text-xs">{modalityCell(g)}</td>
          <td className="pr-4 text-xs">{unitsCell(g)}</td>
          <td className="pr-4">{g.batch_records === undefined ? '—' : api.formatCount(g.batch_records)}</td>
          <td className="pr-4">{g.cost.length === 0 ? api.UNKNOWN : g.cost.map((c) => <div key={`${c.currency}-${c.basis}`}>{api.formatTotal(c)} <span className={muted}>{c.basis}</span></div>)}</td>
          <td className="pr-4">{api.formatCount(g.errors)} / {api.formatCount(g.denied)}</td>
          <td>{api.toBig(g.unknown_usage_records) === 0n && api.toBig(g.unknown_cost_records) === 0n && boundedOf(g) === 0n ? '—' : <span>{(api.toBig(g.unknown_usage_records) > 0n || api.toBig(g.unknown_cost_records) > 0n) && <>usage: {api.formatCount(g.unknown_usage_records)}<br />cost: {api.formatCount(g.unknown_cost_records)}</>}{boundedOf(g) > 0n && <><br />bounded: {api.formatCount(boundedOf(g).toString())} ({boundedBreakdown([g])})</>}</span>}</td>
        </tr>
      })}</tbody>
    </table></div>
    {currencies.length > 0 && <p className={muted}>Cost sorting uses {sortCurrency}; groups with no {sortCurrency} cost sort last. Settled and estimated amounts are never added together.</p>}
  </div>
}

function SummarySection({ filter }: { filter: api.UsageFilter }) {
  const [groupBy, setGroupBy] = useState<api.GroupField[]>(['model_id'])
  const [bucket, setBucket] = useState<api.Bucket>('')
  const query = useQuery({ queryKey: ['usage-summary', filter, groupBy, bucket], queryFn: () => api.usageSummary(filter, groupBy, bucket) })
  const toggle = (f: api.GroupField) => setGroupBy((cur) => cur.includes(f) ? cur.filter((x) => x !== f) : api.GROUP_FIELDS.filter((x) => x === f || cur.includes(x)))
  return <section className="space-y-4" aria-label="Usage summary">
    <h2 className="text-lg font-medium">Summary</h2>
    <div className="flex flex-wrap items-end gap-6">
      <fieldset><legend className="mb-1 text-xs text-gray-600 dark:text-gray-400">Group by</legend><div className="flex flex-wrap gap-x-4 gap-y-1">{api.GROUP_FIELDS.map((f) => <label key={f} className="flex items-center gap-1 text-sm"><input type="checkbox" checked={groupBy.includes(f)} onChange={() => toggle(f)} />{GROUP_LABELS[f]}</label>)}</div></fieldset>
      <Field label="Time bucket"><select className={field} value={bucket} onChange={(e) => setBucket(e.target.value as api.Bucket)}><option value="">No buckets</option><option value="hour">Hourly</option><option value="day">Daily</option></select></Field>
    </div>
    {query.isPending ? <p role="status">Loading usage summary…</p>
      : query.isError ? <ErrorPanel error={query.error} what="usage summaries" onRetry={() => void query.refetch()} />
      : <>
        {query.data.truncated && <p role="alert" className="rounded border border-status-warning-fg p-3 text-sm text-status-warning-fg">Truncated: totals cover only the newest {api.countNoun(query.data.records_scanned, 'record')} scanned. Narrow the time range or add filters for complete totals.</p>}
        {query.data.groups.length === 0 ? <p>No usage recorded for this selection.</p> : <><Headline summary={query.data} /><Breakdown summary={query.data} /></>}
      </>}
  </section>
}

// ---- records -------------------------------------------------------------

const statusClass: Record<string, string> = { ok: 'text-status-ok-fg', error: 'text-status-denied-fg', denied: 'text-status-warning-fg', unknown: 'text-gray-600 dark:text-gray-400' }
const none = '—'
/** What the request was for: the model, the tool called, or for discovery the connection and what was done ("local-tools (list)"). Same rule as `aiplex usage records`. */
export function recordTarget(r: Pick<api.UsageRecord, 'model_id' | 'tool_name' | 'resource_id' | 'operation'>): string {
  if (r.model_id) return r.model_id
  if (r.tool_name) return r.tool_name
  if (!r.resource_id) return none
  const what = r.operation === 'tools/list' ? 'list' : r.operation === 'initialize' ? 'initialize' : r.operation && r.operation !== 'tools/call' ? r.operation : ''
  return what ? `${r.resource_id} (${what})` : r.resource_id
}
const target = recordTarget
const counter = (r: api.UsageRecord, name: api.TokenCounter) => {
  const text = api.formatCount(r.usage?.[name])
  if (!r.usage_bound || text === api.UNKNOWN) return text
  // A bounded count is never shown bare: it is not a final exact figure.
  return r.usage_bound === 'lower_bound' ? `≥ ${text} (lower bound)` : `${text} (${api.USAGE_BOUND_LABEL[r.usage_bound]})`
}
const unitsText = (r: api.UsageRecord): string => {
  const parts = api.UNIT_COUNTERS.filter((n) => r.usage?.[n] !== undefined && r.usage?.[n] !== null).map((n) => `${n === 'generated_images' ? '' : `${api.UNIT_LABEL[n]} `}${api.formatUnit(n, r.usage![n], r.image_size, r.image_quality)}`)
  return parts.length ? parts.join(', ') : '—'
}
const modalityText = (r: api.UsageRecord): string => {
  const parts = api.MODALITY_COUNTERS.filter((n) => r.usage?.[n] !== undefined && r.usage?.[n] !== null).map((n) => `${api.MODALITY_LABEL[n]} ${api.formatCount(r.usage![n])}`)
  return parts.length ? parts.join(', ') : '—'
}
/** Status plus, for capacity rejections, the distinct reason. */
function StatusCell({ r }: { r: api.UsageRecord }) {
  const reason = r.error_class ? api.CAPACITY_REASONS[r.error_class] : undefined
  return <>{r.status}{reason && <span className={`${muted} ml-1 rounded border px-1`} title={reason.text}>{reason.label}</span>}</>
}
const when = (iso: string) => { const d = new Date(iso); return Number.isNaN(d.getTime()) ? iso : d.toLocaleString() }

const DETAIL_FIELDS: [string, (r: api.UsageRecord) => string][] = [
  ['ID', (r) => r.id], ['Tenant', (r) => r.tenant_id], ['Started', (r) => r.started_at], ['Finished', (r) => r.finished_at], ['Latency', (r) => `${r.latency_ms} ms`],
  ['Plane', (r) => r.plane], ['Provider', (r) => r.provider || none], ['Resource', (r) => r.resource_id || none], ['Operation', (r) => r.operation || none],
  ['Model', (r) => r.model_id || none], ['Model version', (r) => r.model_version || none], ['Tool', (r) => r.tool_name || none],
  ['Principal', (r) => r.principal_id || none], ['Agent', (r) => r.actor_id || none], ['Client', (r) => r.client_id || none], ['Access point', (r) => r.access_point_id || none], ['Caller kind', (r) => r.caller_kind || none],
  ['Request ID', (r) => r.request_id || none], ['Trace ID', (r) => r.trace_id || none], ['Quota attempt', (r) => r.quota_attempt_id || none],
  ['Streaming', (r) => (r.streaming ? 'yes' : 'no')], ['Status', (r) => r.status], ['HTTP status', (r) => (r.http_status ? String(r.http_status) : none)],
  ['Error class', (r) => r.error_class || none], ['Source', (r) => r.source], ['Requests', (r) => api.formatCount(r.requests)],
  ['Input tokens', (r) => counter(r, 'input_tokens')], ['Output tokens', (r) => counter(r, 'output_tokens')], ['Cache read tokens', (r) => counter(r, 'cache_read_tokens')],
  ['Cache write tokens', (r) => counter(r, 'cache_write_tokens')], ['Reasoning tokens', (r) => counter(r, 'reasoning_tokens')], ['Total tokens', (r) => counter(r, 'total_tokens')],
  ['Batch', (r) => (r.batch ? 'yes' : 'no')], ['Image size', (r) => r.image_size || none], ['Image quality', (r) => r.image_quality || none],
  ...api.MODALITY_COUNTERS.map((n): [string, (r: api.UsageRecord) => string] => [`${api.MODALITY_LABEL[n]} tokens (part of ${n.startsWith('input') ? 'input' : 'output'})`, (r) => api.formatCount(r.usage?.[n])]),
  ...api.UNIT_COUNTERS.map((n): [string, (r: api.UsageRecord) => string] => [`${api.UNIT_LABEL[n]} (unit)`, (r) => api.formatUnit(n, r.usage?.[n], r.image_size, r.image_quality)]),
  ['Cost', (r) => api.formatCost(r.cost)], ['Cost basis', (r) => r.cost.basis], ['Cost currency', (r) => r.cost.currency || none], ['Cost amount (minor units)', (r) => r.cost.amount ?? none], ['Cost scale', (r) => (r.cost.scale === undefined ? none : String(r.cost.scale))],
]

export function RecordDrawer({ record, onClose }: { record: api.UsageRecord; onClose: () => void }) {
  const ref = useFocusTrap<HTMLElement>(true, onClose)
  return <aside ref={ref} role="dialog" aria-modal="true" aria-label="Usage record detail" tabIndex={-1} className="fixed inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-950">
    <div className="mb-4 flex items-start justify-between gap-3"><h3 className="text-lg font-medium">Usage record</h3><button type="button" className="underline" onClick={onClose}>Close</button></div>
    {record.usage_bound && <p role="note" className="mb-3 rounded border border-status-warning-fg p-2 text-sm text-status-warning-fg"><strong>Not a final count.</strong> {api.USAGE_BOUND_TEXT[record.usage_bound]} Any cost computed from these counters is not final either.</p>}
    {api.isSessionRecord(record) && <p role="note" className="mb-3 rounded border border-gray-300 p-2 text-sm dark:border-gray-700"><strong>WebSocket session.</strong> One request was admitted and settled for the whole session; token usage reported during the session is advisory.</p>}
    {record.error_class && api.CAPACITY_REASONS[record.error_class] && <p role="note" className="mb-3 rounded border border-gray-300 p-2 text-sm dark:border-gray-700"><strong>{api.CAPACITY_REASONS[record.error_class].label}.</strong> {api.CAPACITY_REASONS[record.error_class].text}</p>}
    {record.batch && <p role="note" className="mb-3 rounded border border-gray-300 p-2 text-sm dark:border-gray-700"><strong>Provider batch.</strong> This exchange created a batch job or fetched the results that settled it. Batch usage arrives only when the results are fetched through AIPlex.</p>}
    {record.usage === null && <p className="mb-3 rounded border border-gray-300 p-2 text-sm dark:border-gray-700">No usage was reported for this request. Token counts are unknown, not zero.</p>}
    <dl className="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm">{DETAIL_FIELDS.map(([label, read]) => <div key={label} className="contents"><dt className="text-gray-600 dark:text-gray-400">{label}</dt><dd className="break-all font-mono text-xs leading-5">{read(record)}</dd></div>)}</dl>
    {record.provider_limits && <section aria-label="Provider limits" className="mt-4"><h4 className="text-sm font-medium">Provider limits</h4><p className="text-xs text-gray-600 dark:text-gray-400">Rate-limit signals the upstream provider returned for this request, as received.</p><pre className="mt-1 max-h-48 overflow-auto rounded bg-gray-100 p-2 text-xs dark:bg-gray-900">{JSON.stringify(record.provider_limits, null, 2)}</pre></section>}
    <details className="mt-4"><summary className="cursor-pointer text-sm">Raw record</summary><pre className="mt-2 max-h-72 overflow-auto rounded bg-gray-100 p-3 text-xs dark:bg-gray-900">{JSON.stringify(record, null, 2)}</pre></details>
  </aside>
}

function RecordsSection({ filter }: { filter: api.UsageFilter }) {
  const [cursors, setCursors] = useState<string[]>([''])
  const [selected, setSelected] = useState<api.UsageRecord | null>(null)
  const filterKey = JSON.stringify(filter)
  useEffect(() => { setCursors(['']); setSelected(null) }, [filterKey])
  const cursor = cursors[cursors.length - 1]
  const query = useQuery({ queryKey: ['usage-records', filter, cursor], queryFn: () => api.listUsageRecords(filter, 50, cursor) })
  const page = query.data
  return <section className="space-y-3" aria-label="Requests">
    <h2 className="text-lg font-medium">Requests</h2>
    {query.isPending ? <p role="status">Loading requests…</p>
      : query.isError ? <ErrorPanel error={query.error} what="usage records" onRetry={() => void query.refetch()} />
      : !page || page.records.length === 0 ? <p>{cursors.length > 1 ? 'No older requests.' : 'No requests match these filters.'}</p>
      : <div className="overflow-x-auto"><table className="w-full text-left text-sm">
        <caption className="sr-only">Usage records, newest first</caption>
        <thead><tr className="border-b border-gray-200 dark:border-gray-800">{['Time', 'Plane', 'Model / tool', 'Agent', 'Principal', 'Client', 'Status', 'Tokens in', 'Tokens out', 'Cache write', 'Modality tokens', 'Units', 'Cost', 'Latency'].map((h) => <th key={h} scope="col" className="py-2 pr-4">{h}</th>)}</tr></thead>
        <tbody>{page?.records.map((r) => <tr key={r.id} className="border-t border-gray-200 dark:border-gray-800">
          <td className="py-2 pr-4 whitespace-nowrap"><button type="button" className="underline" aria-label={`Open details for record ${r.id}`} onClick={() => setSelected(r)}>{when(r.started_at)}</button></td>
          <td className="pr-4">{r.plane}</td><td className="pr-4">{target(r)}{api.isSessionRecord(r) && <span className={`${muted} ml-1 rounded border px-1`} title="WebSocket session: metered as one request; usage is advisory">session</span>}{r.batch && <span className={`${muted} ml-1 rounded border px-1`} title="Part of a provider batch job">batch</span>}</td><td className="pr-4"><ShortId value={r.actor_id} /></td><td className="pr-4"><ShortId value={r.principal_id} /></td><td className="pr-4"><ShortId value={r.client_id} /></td>
          <td className={`pr-4 ${statusClass[r.status] ?? ''}`}><StatusCell r={r} /></td>
          <td className="pr-4">{counter(r, 'input_tokens')}</td><td className="pr-4">{counter(r, 'output_tokens')}</td><td className="pr-4">{counter(r, 'cache_write_tokens')}</td>
          <td className="pr-4 text-xs">{modalityText(r)}</td><td className="pr-4 text-xs">{unitsText(r)}</td>
          <td className="pr-4 whitespace-nowrap">{api.formatCost(r.cost)}{r.usage_bound && <span className={`${muted} ml-1`}>(from bounded usage)</span>}{(r.cost.basis === 'settled' || r.cost.basis === 'estimated') && <span className={`${muted} ml-1`}>{r.cost.basis}</span>}</td>
          <td className="whitespace-nowrap">{r.latency_ms} ms</td>
        </tr>)}</tbody>
      </table></div>}
    {page && <div className="flex items-center gap-3"><button type="button" className={button} disabled={cursors.length <= 1} onClick={() => setCursors((c) => c.slice(0, -1))}>Newer</button><span className={muted}>Page {cursors.length}</span><button type="button" className={button} disabled={!page.next_cursor} onClick={() => setCursors((c) => [...c, page.next_cursor!])}>Older</button></div>}
    {selected && <RecordDrawer record={selected} onClose={() => setSelected(null)} />}
  </section>
}

// ---- page ----------------------------------------------------------------

export default function Usage() {
  const access = useAccess()
  const [draft, setDraft] = useState<FilterDraft>(emptyDraft)
  const [applied, setApplied] = useState<api.UsageFilter>(() => buildFilter(emptyDraft))
  return <div className="space-y-8">
    <header>
      <h1 className="text-2xl font-semibold">Usage</h1>
      <p className="mt-2 text-sm text-gray-600 dark:text-gray-400">What each request to any model or tool, by any client or agent, cost in requests, tokens and dollars. Unknown usage and cost are shown as “unknown”, never as zero. {access.isAdmin ? 'You see every record in the tenant.' : 'You see only the requests where you are the principal or the agent.'}</p>
    </header>
    <FilterBar draft={draft} onChange={setDraft} onApply={() => setApplied(buildFilter(draft))} />
    <SummarySection filter={applied} />
    <RecordsSection filter={applied} />
    {access.isOperator && <UsagePrices />}
  </div>
}
