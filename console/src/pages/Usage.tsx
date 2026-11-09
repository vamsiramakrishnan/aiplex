import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import * as api from '../api/usage'
import UsagePrices from './UsagePrices'
import { ErrorPanel } from '../components/UsageError'

const field = 'w-full rounded border border-gray-300 bg-transparent px-3 py-2 text-sm dark:border-gray-700'
const button = 'rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900'
const card = 'rounded border border-gray-200 p-4 dark:border-gray-800'
const muted = 'text-xs text-gray-500 dark:text-gray-400'

export const GROUP_LABELS: Record<api.GroupField, string> = {
  plane: 'Plane', provider: 'Provider', model_id: 'Model', tool_name: 'Tool', actor_id: 'Agent', principal_id: 'Principal', client_id: 'Client', access_point_id: 'Access point', caller_kind: 'Caller kind', status: 'Status',
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
}
export const emptyDraft: FilterDraft = { preset: '24h', plane: '', model_id: '', tool_name: '', actor_id: '', principal_id: '', client_id: '', access_point_id: '', caller_kind: '', status: '' }

/** Turns the form into API filters; the time window is resolved once, at apply time. */
export function buildFilter(draft: FilterDraft, now: Date = new Date()): api.UsageFilter {
  const filter: api.UsageFilter = { since: api.presetSince(draft.preset, now) }
  for (const name of ['plane', 'model_id', 'tool_name', 'actor_id', 'principal_id', 'client_id', 'access_point_id', 'caller_kind', 'status'] as const) {
    const value = draft[name].trim()
    if (value) filter[name] = value
  }
  return filter
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="block text-sm"><span className="mb-1 block text-xs text-gray-500">{label}</span>{children}</label>
}

function FilterBar({ draft, onChange, onApply }: { draft: FilterDraft; onChange: (next: FilterDraft) => void; onApply: () => void }) {
  const set = (name: keyof FilterDraft) => (e: { target: { value: string } }) => onChange({ ...draft, [name]: e.target.value })
  const submit = (e: FormEvent) => { e.preventDefault(); onApply() }
  return <form aria-label="Usage filters" onSubmit={submit} className={`${card} grid gap-3 sm:grid-cols-2 lg:grid-cols-4`}>
    <Field label="Time range"><select className={field} value={draft.preset} onChange={set('preset')}>{api.TIME_PRESETS.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}</select></Field>
    <Field label="Plane"><select className={field} value={draft.plane} onChange={set('plane')}><option value="">All planes</option><option value="llmplex">LLMPlex (models)</option><option value="mcplex">MCPlex (tools)</option><option value="a2aplex">A2APlex (agents)</option></select></Field>
    <Field label="Status"><select className={field} value={draft.status} onChange={set('status')}><option value="">Any status</option><option value="ok">ok</option><option value="error">error</option><option value="denied">denied</option><option value="unknown">unknown</option></select></Field>
    <Field label="Model"><input className={field} value={draft.model_id} onChange={set('model_id')} placeholder="e.g. gemini-2.5-flash" /></Field>
    <Field label="Tool"><input className={field} value={draft.tool_name} onChange={set('tool_name')} placeholder="e.g. search_curriculum" /></Field>
    <Field label="Agent"><input className={field} value={draft.actor_id} onChange={set('actor_id')} placeholder="agent / actor ID" /></Field>
    <Field label="Principal"><input className={field} value={draft.principal_id} onChange={set('principal_id')} placeholder="user / principal ID" /></Field>
    <Field label="Client"><input className={field} value={draft.client_id} onChange={set('client_id')} placeholder="client ID" /></Field>
    <Field label="Access point"><input className={field} value={draft.access_point_id} onChange={set('access_point_id')} placeholder="access point ID" /></Field>
    <Field label="Caller kind"><select className={field} value={draft.caller_kind} onChange={set('caller_kind')}><option value="">Any caller kind</option><option value="agent">agent</option><option value="user">user</option><option value="client">client</option></select></Field>
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
  return missing !== undefined && api.toBig(missing) > 0n ? `${api.formatCount(sum)} (+${api.formatCount(missing)} unknown)` : api.formatCount(sum)
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

function groupLabel(g: api.SummaryGroup, fields: readonly api.GroupField[]): string {
  return fields.map((f) => g.key[f] || '(none)').join(' · ') || 'All records'
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
  const tokenLine = (label: string, t: api.TokenHeadline) => <p>{label} <strong>{t.reported ? api.formatCount(t.total.toString()) : api.UNKNOWN}</strong>{t.reported && t.unknownRecords > 0n && <span className={muted}> (+{api.formatCount(t.unknownRecords.toString())} records unreported)</span>}</p>
  return <section aria-label="Usage totals" className="grid gap-4 md:grid-cols-3">
    <div className={card} role="group" aria-label="Requests"><p className={muted}>Requests</p><p className="mt-1 text-2xl font-semibold">{api.formatCount(requests.toString())}</p><p className={`${muted} mt-1`}>{api.formatCount(errors.toString())} errors · {api.formatCount(denied.toString())} denied · {api.formatCount(summary.records_scanned)} records scanned</p></div>
    <div className={card} role="group" aria-label="Tokens"><p className={muted}>Tokens</p><div className="mt-1 text-lg">{tokenLine('Input', input)}{tokenLine('Output', output)}</div>{unknownUsage > 0n && <p className={`${muted} mt-1`}>{api.formatCount(unknownUsage.toString())} records reported no usage at all</p>}</div>
    <div className={card} role="group" aria-label="Cost"><p className={muted}>Cost (per currency and basis)</p>
      {costs.length === 0 ? <p className="mt-1 text-lg">unknown</p> : <ul className="mt-1 space-y-1">{costs.map((c) => <li key={`${c.currency}-${c.basis}`} className="text-lg"><strong>{api.formatTotal(c)}</strong> <span className={`rounded px-1.5 py-0.5 text-xs ${c.basis === 'settled' ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200' : 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200'}`}>{c.basis}</span> <span className={muted}>{api.formatCount(c.records)} records</span></li>)}</ul>}
      {unknownCost > 0n && <p className={`${muted} mt-1`}>{api.formatCount(unknownCost.toString())} records have unknown cost (no price configured and not settled by quota); they are not counted above.</p>}
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
        {header('requests', 'Requests')}{header('input', 'Input tokens')}{header('output', 'Output tokens')}{header('cost', 'Cost')}
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
          <td className="pr-4">{g.cost.length === 0 ? api.UNKNOWN : g.cost.map((c) => <div key={`${c.currency}-${c.basis}`}>{api.formatTotal(c)} <span className={muted}>{c.basis}</span></div>)}</td>
          <td className="pr-4">{api.formatCount(g.errors)} / {api.formatCount(g.denied)}</td>
          <td>{api.toBig(g.unknown_usage_records) === 0n && api.toBig(g.unknown_cost_records) === 0n ? '—' : <span>usage: {api.formatCount(g.unknown_usage_records)}<br />cost: {api.formatCount(g.unknown_cost_records)}</span>}</td>
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
      <fieldset><legend className="mb-1 text-xs text-gray-500">Group by</legend><div className="flex flex-wrap gap-x-4 gap-y-1">{api.GROUP_FIELDS.map((f) => <label key={f} className="flex items-center gap-1 text-sm"><input type="checkbox" checked={groupBy.includes(f)} onChange={() => toggle(f)} />{GROUP_LABELS[f]}</label>)}</div></fieldset>
      <Field label="Time bucket"><select className={field} value={bucket} onChange={(e) => setBucket(e.target.value as api.Bucket)}><option value="">No buckets</option><option value="hour">Hourly</option><option value="day">Daily</option></select></Field>
    </div>
    {query.isPending ? <p role="status">Loading usage summary…</p>
      : query.isError ? <ErrorPanel error={query.error} what="usage summaries" onRetry={() => void query.refetch()} />
      : <>
        {query.data.truncated && <p role="alert" className="rounded border border-amber-400 p-3 text-sm text-amber-800 dark:text-amber-300">Truncated: totals cover only the newest {api.formatCount(query.data.records_scanned)} records scanned. Narrow the time range or add filters for complete totals.</p>}
        {query.data.groups.length === 0 ? <p>No usage recorded for this selection.</p> : <><Headline summary={query.data} /><Breakdown summary={query.data} /></>}
      </>}
  </section>
}

// ---- records -------------------------------------------------------------

const statusClass: Record<string, string> = { ok: 'text-green-700 dark:text-green-400', error: 'text-red-700 dark:text-red-400', denied: 'text-amber-700 dark:text-amber-400', unknown: 'text-gray-500' }
const none = '—'
const target = (r: api.UsageRecord) => r.model_id || r.tool_name || r.resource_id || none
const counter = (r: api.UsageRecord, name: api.TokenCounter) => api.formatCount(r.usage?.[name])
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
  ['Cost', (r) => api.formatCost(r.cost)], ['Cost basis', (r) => r.cost.basis], ['Cost currency', (r) => r.cost.currency || none], ['Cost amount (minor units)', (r) => r.cost.amount ?? none], ['Cost scale', (r) => (r.cost.scale === undefined ? none : String(r.cost.scale))],
]

export function RecordDrawer({ record, onClose }: { record: api.UsageRecord; onClose: () => void }) {
  const closeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    closeRef.current?.focus()
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return <aside role="dialog" aria-label="Usage record detail" className="fixed inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-950">
    <div className="mb-4 flex items-start justify-between gap-3"><h3 className="text-lg font-medium">Usage record</h3><button ref={closeRef} type="button" className="underline" onClick={onClose}>Close</button></div>
    {record.usage === null && <p className="mb-3 rounded border border-gray-300 p-2 text-sm dark:border-gray-700">No usage was reported for this request. Token counts are unknown, not zero.</p>}
    <dl className="grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm">{DETAIL_FIELDS.map(([label, read]) => <div key={label} className="contents"><dt className="text-gray-500">{label}</dt><dd className="break-all font-mono text-xs leading-5">{read(record)}</dd></div>)}</dl>
    {record.provider_limits && <section aria-label="Provider limits" className="mt-4"><h4 className="text-sm font-medium">Provider limits</h4><p className="text-xs text-gray-500">Rate-limit signals the upstream provider returned for this request, as received.</p><pre className="mt-1 max-h-48 overflow-auto rounded bg-gray-100 p-2 text-xs dark:bg-gray-900">{JSON.stringify(record.provider_limits, null, 2)}</pre></section>}
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
        <thead><tr className="border-b border-gray-200 dark:border-gray-800">{['Time', 'Plane', 'Model / tool', 'Agent', 'Principal', 'Client', 'Status', 'Tokens in', 'Tokens out', 'Cost', 'Latency'].map((h) => <th key={h} scope="col" className="py-2 pr-4">{h}</th>)}</tr></thead>
        <tbody>{page?.records.map((r) => <tr key={r.id} className="border-t border-gray-200 dark:border-gray-800">
          <td className="py-2 pr-4 whitespace-nowrap"><button type="button" className="underline" aria-label={`Open details for record ${r.id}`} onClick={() => setSelected(r)}>{when(r.started_at)}</button></td>
          <td className="pr-4">{r.plane}</td><td className="pr-4">{target(r)}</td><td className="pr-4">{r.actor_id || none}</td><td className="pr-4">{r.principal_id || none}</td><td className="pr-4">{r.client_id || none}</td>
          <td className={`pr-4 ${statusClass[r.status] ?? ''}`}>{r.status}</td>
          <td className="pr-4">{counter(r, 'input_tokens')}</td><td className="pr-4">{counter(r, 'output_tokens')}</td>
          <td className="pr-4 whitespace-nowrap">{api.formatCost(r.cost)}{(r.cost.basis === 'settled' || r.cost.basis === 'estimated') && <span className={`${muted} ml-1`}>{r.cost.basis}</span>}</td>
          <td className="whitespace-nowrap">{r.latency_ms} ms</td>
        </tr>)}</tbody>
      </table></div>}
    {page && <div className="flex items-center gap-3"><button type="button" className={button} disabled={cursors.length <= 1} onClick={() => setCursors((c) => c.slice(0, -1))}>Newer</button><span className={muted}>Page {cursors.length}</span><button type="button" className={button} disabled={!page.next_cursor} onClick={() => setCursors((c) => [...c, page.next_cursor!])}>Older</button></div>}
    {selected && <RecordDrawer record={selected} onClose={() => setSelected(null)} />}
  </section>
}

// ---- page ----------------------------------------------------------------

export default function Usage() {
  const [draft, setDraft] = useState<FilterDraft>(emptyDraft)
  const [applied, setApplied] = useState<api.UsageFilter>(() => buildFilter(emptyDraft))
  return <div className="space-y-8">
    <header>
      <h1 className="text-2xl font-semibold">Usage</h1>
      <p className="mt-2 text-sm text-gray-500">What each request to any model or tool, by any client or agent, cost in requests, tokens and dollars. Unknown usage and cost are shown as “unknown”, never as zero. Administrators see every record in the tenant; other users see only requests where they are the principal or the agent.</p>
    </header>
    <FilterBar draft={draft} onChange={setDraft} onApply={() => setApplied(buildFilter(draft))} />
    <SummarySection filter={applied} />
    <RecordsSection filter={applied} />
    <UsagePrices />
  </div>
}
