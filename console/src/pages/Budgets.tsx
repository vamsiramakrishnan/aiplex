import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ADMIN_REASON, useAccess } from '../hooks/useAccess'
import {
  PLANES, deleteBudget, errorStatus, getBudgetStatus, invalidField, isQuotaplexRequired, isRevisionConflict, listBudgets, previewBudgets, putBudget,
  type BudgetPreview, type BudgetRecord, type BudgetSpec, type BudgetStatus, type GroupRef,
} from '../api/budgets'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50'
const primary = 'px-3 py-1.5 text-sm bg-brand-600 text-brand-contrast rounded disabled:opacity-50'
const field = 'border rounded px-3 py-2 text-sm w-full bg-transparent'
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e))

export const QUOTAPLEX_OFF = 'QuotaPlex is not enabled on this server. Budgets are enforced by QuotaPlex, so none can be listed, saved or measured until it is enabled.'
const CHANGED = 'This budget changed since this page read it. Nothing was saved or deleted. Reload to see the current version, then try again.'
const STALE = 'Reload first: the list may be out of date.'

// ── value helpers: a value the API did not send is "unknown", never 0 or blank ──

const isList = (v: unknown): v is unknown[] => Array.isArray(v)
const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)
const strings = (v: unknown): string[] => (isList(v) ? v.filter((x): x is string => typeof x === 'string') : [])
const groupsOf = (v: unknown): GroupRef[] => (isList(v) ? v.filter((g): g is GroupRef => !!g && typeof (g as GroupRef).source === 'string' && typeof (g as GroupRef).id === 'string') : [])

function unitSuffix(unit: string | undefined, currency: string | undefined): string {
  if (unit === 'cost') return currency ? ` ${currency}` : ''
  return unit ? ` ${unit}` : ''
}
const amountText = (v: unknown, unit: string | undefined, currency: string | undefined) => (typeof v === 'string' && v !== '' ? `${v}${unitSuffix(unit, currency)}` : 'unknown')

function windowText(w: BudgetSpec['window'] | undefined): string {
  if (!w || typeof w !== 'object') return 'unknown'
  if (w.kind === 'calendar') return `${w.unit ?? 'unknown unit'}, ${w.time_zone ?? 'unknown time zone'}`
  if (w.kind === 'tumbling') return typeof w.hours === 'number' ? `every ${w.hours} hours` : 'tumbling, hours unknown'
  return typeof w.kind === 'string' ? w.kind : 'unknown'
}

const MEMBER_LABELS: [keyof NonNullable<BudgetSpec['members']>, string, string][] = [
  ['groups', 'group', 'groups'], ['principals', 'principal', 'principals'], ['agents', 'agent', 'agents'],
  ['client_registrations', 'client registration', 'client registrations'], ['access_points', 'access point', 'access points'], ['projects', 'project', 'projects'],
]

function membersSummary(m: BudgetSpec['members']): string {
  if (m == null) return 'none'
  if (typeof m !== 'object') return 'unknown'
  const parts = MEMBER_LABELS.flatMap(([k, one, many]) => {
    const v = (m as Record<string, unknown>)[k]
    return isList(v) && v.length > 0 ? [`${v.length} ${v.length === 1 ? one : many}`] : []
  })
  return parts.length ? parts.join(', ') : 'none'
}

function membersDetail(m: BudgetSpec['members']) {
  if (m == null || typeof m !== 'object') return <span>{m == null ? 'none' : 'unknown'}</span>
  const rows = MEMBER_LABELS.flatMap(([k, , many]) => {
    const v = (m as Record<string, unknown>)[k]
    const items = k === 'groups' ? groupsOf(v).map((g) => `${g.source} / ${g.id}`) : strings(v)
    return items.length ? [<li key={k}><span className="font-medium">{many}:</span> <span className="break-all">{items.join(', ')}</span></li>] : []
  })
  return rows.length ? <ul className="space-y-0.5">{rows}</ul> : <span>none</span>
}

function usedLine(s: BudgetStatus, unit: string | undefined) {
  // Only a cost budget has a count of requests it could not price. Absent on a cost budget is unknown, not 0.
  if (unit !== 'cost') return null
  const n = s.unknown_cost_requests
  if (typeof n !== 'number') return <span className="block text-xs">Requests of unknown cost: unknown. The amount used may be understated.</span>
  if (n === 0) return <span className="block text-xs">0 requests of unknown cost.</span>
  return <span className="block text-xs text-status-denied-fg">{n} request{n === 1 ? '' : 's'} of unknown cost in this window; their cost is not in this amount.</span>
}

/**
 * The operator-only block of a status. internal/api/budgets.go (budgetStatus, `if !full { return st }`) leaves it out for a member's
 * view, and in the full view always sets `thresholds` (EffectiveThresholds is never empty, pkg/budgets/budgets.go:284). So a list array
 * on `thresholds` proves this is the full view and that an absent sibling list was left out by the server's `omitempty`, not unsent.
 */
const fullView = (s: BudgetStatus) => isList(s.thresholds)

function thresholdsReached(s: BudgetStatus): string {
  if (isList(s.threshold_events)) return s.threshold_events.length ? s.threshold_events.map((e) => `${e.percent}%`).join(', ') : 'none'
  // threshold_events is appended only for crossed thresholds and is omitempty (budgets.go: `for _, c := range bal.Crossed`): absent in the full view means none.
  return fullView(s) ? 'none' : 'unknown'
}

// The ledger's token counters overlap: total_tokens covers input and output, and
// cache and reasoning counts can be subsets of those. They are shown side by
// side, total first, and never added together.
const TOKEN_COUNTER_LABELS: [string, string][] = [['total_tokens', 'total'], ['input_tokens', 'input'], ['output_tokens', 'output'], ['cache_read_tokens', 'cache read'], ['cache_write_tokens', 'cache write'], ['reasoning_tokens', 'reasoning']]

function tokenCountersText(tokens: Record<string, number> | null | undefined): string {
  if (!tokens || typeof tokens !== 'object') return 'tokens unknown'
  const known = TOKEN_COUNTER_LABELS.map(([key]) => key)
  const parts = TOKEN_COUNTER_LABELS.filter(([key]) => typeof tokens[key] === 'number').map(([key, label]) => `${label} ${tokens[key]}`)
  for (const key of Object.keys(tokens).sort()) if (!known.includes(key) && typeof tokens[key] === 'number') parts.push(`${key} ${tokens[key]}`)
  return parts.length ? `tokens: ${parts.join(', ')}` : 'tokens unknown'
}

function spenderText(p: NonNullable<BudgetStatus['top_spenders']>[number], unit: string | undefined, currency: string | undefined): string {
  const parts: string[] = []
  if (unit === 'tokens') {
    parts.push(tokenCountersText(p.tokens))
  } else if (unit === 'cost') {
    const c = isList(p.cost) ? p.cost.find((x) => x && x.currency === currency) : undefined
    const amt = c ? [c.amount_decimal, c.amount].find((x) => typeof x === 'string' && x !== '') : undefined
    parts.push(amt ? `${amt} ${currency ?? ''}${c?.basis ? ` (${c.basis})` : ''}`.trim() : 'cost unknown')
    parts.push(typeof p.unknown_cost_records === 'number' ? `${p.unknown_cost_records} records of unknown cost` : 'records of unknown cost: unknown')
  } else {
    parts.push(typeof p.requests === 'number' ? `${p.requests} requests` : 'requests unknown')
  }
  if (unit !== 'requests' && typeof p.requests === 'number') parts.push(`${p.requests} requests`)
  return parts.join(', ')
}

const nameOf = (r: BudgetRecord) => (str(r.spec.display_name) || r.id)

// ── the tab ──

/** Budgets: team and project limits above per-caller quota. Everyone with an operator role reads; only admins write. */
export default function Budgets() {
  const access = useAccess()
  const cache = useQueryClient()
  const [form, setForm] = useState<{ mode: 'create' } | { mode: 'edit'; record: BudgetRecord } | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const list = useQuery({ queryKey: ['budgets'], queryFn: listBudgets })
  const rows = list.data
  const off = list.isError && isQuotaplexRequired(list.error)
  const loadFailed = list.isError && !off
  const reload = () => { void list.refetch(); void cache.invalidateQueries({ queryKey: ['budget-status'] }) }
  const refresh = () => { void cache.invalidateQueries({ queryKey: ['budgets'] }); void cache.invalidateQueries({ queryKey: ['budget-status'] }) }
  const sorted = rows ? [...rows].sort((a, b) => a.id.localeCompare(b.id)) : undefined
  const detail = selected && sorted ? sorted.find((r) => r.id === selected) : undefined
  return <section className="space-y-4" aria-label="Budgets">
    <p className="text-sm text-gray-600 dark:text-gray-400">A budget caps spend, tokens or requests for a team or project, above each caller's own quota. QuotaPlex enforces it.</p>
    <div className="flex gap-2">
      {access.isAdmin && <button type="button" className={primary} disabled={!!form || off} onClick={() => setForm({ mode: 'create' })}>Add budget</button>}
      <button type="button" className={btn} disabled={list.isFetching} onClick={reload}>{list.isFetching ? 'Loading…' : 'Reload'}</button>
    </div>
    {!access.isAdmin && <p className="text-xs text-gray-600 dark:text-gray-400">{ADMIN_REASON} You can read budgets but not change them.</p>}
    {access.isAdmin && form && <BudgetForm key={form.mode === 'edit' ? `${form.record.id}@${form.record.revision ?? '?'}` : 'create'} form={form}
      others={(sorted ?? []).filter((r) => form.mode !== 'edit' || r.id !== form.record.id).map((r) => r.id)}
      onClose={() => setForm(null)} onSaved={() => { setForm(null); refresh() }} onReload={() => { setForm(null); reload() }} />}
    {list.isPending && <p role="status">Loading budgets…</p>}
    {off && <div role="alert" className="text-status-denied-fg"><p>{QUOTAPLEX_OFF}</p></div>}
    {loadFailed && !rows && <div role="alert" className="text-status-denied-fg">
      <p>Could not load budgets: {msg(list.error)}. This is an error, not an empty list.</p>
      <button type="button" className={`${btn} mt-2`} onClick={() => void list.refetch()}>Retry</button>
    </div>}
    {loadFailed && rows && <div role="alert" className="text-status-denied-fg">
      <p>Could not refresh budgets: {msg(list.error)}. The rows below were read earlier and may be out of date.</p>
      <button type="button" className={`${btn} mt-2`} onClick={() => void list.refetch()}>Retry</button>
    </div>}
    {sorted && !off && sorted.length === 0 && !loadFailed && <p role="status">No budgets. Nothing limits a team or project beyond each caller's own quota.</p>}
    {sorted && !off && sorted.length > 0 && <div className="overflow-x-auto"><table className="w-full text-sm text-left">
      <caption className="sr-only">{loadFailed ? 'Budgets (possibly out of date)' : 'Budgets'}</caption>
      <thead><tr><th className="py-2 pr-3">Budget</th><th className="pr-3">Limit</th><th className="pr-3">Window</th><th className="pr-3">Members</th><th className="pr-3">Used</th><th className="pr-3">Remaining</th><th className="pr-3">Thresholds reached</th><th className="pr-3">Revision</th><th><span className="sr-only">Actions</span></th></tr></thead>
      <tbody>{sorted.map((r) => <Row key={r.id} record={r} stale={loadFailed} admin={access.isAdmin} editing={form?.mode === 'edit' && form.record.id === r.id} open={selected === r.id}
        onToggle={() => setSelected(selected === r.id ? null : r.id)} onEdit={() => setForm({ mode: 'edit', record: r })} onChanged={refresh} onReload={reload} />)}</tbody>
    </table></div>}
    {detail && <Detail key={detail.id} record={detail} />}
  </section>
}

function useStatus(id: string) {
  return useQuery({ queryKey: ['budget-status', id], queryFn: () => getBudgetStatus(id) })
}

function StatusCells({ record }: { record: BudgetRecord }) {
  const q = useStatus(record.id)
  const unit = str(record.spec.limit?.unit)
  const currency = str(record.spec.limit?.currency)
  const s = q.data
  if (q.isPending) return <td colSpan={3} className="pr-3" role="status">Loading status…</td>
  if (!s) {
    const why = isQuotaplexRequired(q.error) ? QUOTAPLEX_OFF : `Status unavailable: ${msg(q.error)}.`
    return <td colSpan={3} className="pr-3 text-status-denied-fg">{why} Used, remaining and thresholds are unknown.</td>
  }
  return <>
    <td className="pr-3">
      <span>{amountText(s.spent, unit, currency)}</span>
      {s.over_limit === true && <span className="block text-xs text-status-denied-fg">over the limit</span>}
      {usedLine(s, unit)}
      {record.spec.include_tool_calls === true && <span className="block text-xs">includes tool calls</span>}
      {q.isError && <span role="alert" className="block text-xs text-status-denied-fg">The latest refresh of this status failed ({msg(q.error)}). Shown from the last successful refresh at {new Date(q.dataUpdatedAt).toLocaleTimeString()}.</span>}
    </td>
    <td className="pr-3">{amountText(s.remaining, unit, currency)}</td>
    <td className="pr-3">{thresholdsReached(s)}</td>
  </>
}

function Row({ record: r, stale, admin, editing, open, onToggle, onEdit, onChanged, onReload }: {
  record: BudgetRecord; stale: boolean; admin: boolean; editing: boolean; open: boolean; onToggle: () => void; onEdit: () => void; onChanged: () => void; onReload: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const known = typeof r.revision === 'number'
  const remove = useMutation({
    mutationFn: () => deleteBudget(r.id, r.revision as number),
    onSuccess: () => { setConfirming(false); onChanged() },
  })
  const gone = remove.isError && errorStatus(remove.error) === 404
  const conflict = remove.isError && (isRevisionConflict(remove.error) || gone)
  const why = known ? (stale ? STALE : undefined) : 'The revision is unknown, so a change cannot be checked against it. Reload first.'
  const lim = r.spec.limit
  return <>
    <tr className="border-t align-top">
      <td className="py-2 pr-3"><span className="font-medium">{nameOf(r)}</span>{nameOf(r) !== r.id && <span className="block text-xs break-all">{r.id}</span>}{str(r.spec.parent) && <span className="block text-xs">under {r.spec.parent}</span>}</td>
      <td className="pr-3">{amountText(lim?.amount, str(lim?.unit), str(lim?.currency))}</td>
      <td className="pr-3">{windowText(r.spec.window)}</td>
      <td className="pr-3">{membersSummary(r.spec.members)}</td>
      <StatusCells record={r} />
      <td className="pr-3">{known ? r.revision : 'unknown'}</td>
      <td className="whitespace-nowrap space-x-2">
        <button type="button" className={btn} aria-expanded={open} aria-label={`${open ? 'Hide' : 'Show'} details of ${r.id}`} onClick={onToggle}>{open ? 'Hide' : 'Details'}</button>
        {admin && <button type="button" className={btn} disabled={!known || stale || editing} title={why} aria-label={`Edit ${r.id}`} onClick={onEdit}>Edit</button>}
        {admin && <button type="button" className={btn} disabled={!known || stale || confirming} title={why} aria-label={`Delete ${r.id}`} onClick={() => { remove.reset(); setConfirming(true) }}>Delete</button>}
      </td>
    </tr>
    {admin && (confirming || remove.isError) && <tr><td colSpan={9} className="pb-3"><div className="border rounded p-3 space-y-2">
      {confirming && !conflict && <p>Delete the budget <strong>{r.id}</strong>? Members stop drawing from it at once; its counters stay in the quota store until their windows end.</p>}
      {conflict && <div role="alert" className="text-status-denied-fg"><p>{gone ? 'This budget no longer exists. Nothing was deleted.' : CHANGED}</p>
        <button type="button" className={`${btn} mt-2`} onClick={() => { setConfirming(false); remove.reset(); onReload() }}>Reload latest version</button></div>}
      {remove.isError && !conflict && <p role="alert" className="text-status-denied-fg">{isQuotaplexRequired(remove.error) ? QUOTAPLEX_OFF : `Could not delete: ${msg(remove.error)}`}</p>}
      {confirming && !conflict && <div className="flex gap-2">
        <button type="button" className={primary} disabled={remove.isPending} onClick={() => remove.mutate()}>{remove.isPending ? 'Deleting…' : 'Confirm delete'}</button>
        <button type="button" className={btn} disabled={remove.isPending} onClick={() => { setConfirming(false); remove.reset() }}>Cancel</button>
      </div>}
    </div></td></tr>}
  </>
}

// ── detail ──

function Detail({ record: r }: { record: BudgetRecord }) {
  const q = useStatus(r.id)
  const s = q.data
  const unit = str(r.spec.limit?.unit)
  const currency = str(r.spec.limit?.currency)
  const w = s?.window
  const list = (v: unknown) => (isList(v) ? v : undefined)
  const children = list(s?.children) as NonNullable<BudgetStatus['children']> | undefined
  const spenders = list(s?.top_spenders) as NonNullable<BudgetStatus['top_spenders']> | undefined
  const notes = list(s?.notes) as string[] | undefined
  const events = list(s?.threshold_events) as NonNullable<BudgetStatus['threshold_events']> | undefined
  const full = !!s && fullView(s)
  const childNote = !!notes?.some((n) => /counters are unavailable/.test(n))
  const onUnknown = unit === 'cost' ? (str(r.spec.on_unknown_cost) ?? 'deny (default)') : 'not applicable'
  return <div className="border rounded-lg p-4 space-y-3" role="region" aria-label={`Budget ${r.id}`}>
    <h2 className="font-semibold">Budget {r.id}</h2>
    <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 text-sm">
      <div><dt className="inline font-medium">Members: </dt><dd className="inline">{membersDetail(r.spec.members)}</dd></div>
      <div><dt className="inline font-medium">Parent: </dt><dd className="inline">{str(r.spec.parent) ?? 'none'}</dd></div>
      <div><dt className="inline font-medium">When a cost is unknown: </dt><dd className="inline">{onUnknown}</dd></div>
      <div><dt className="inline font-medium">Counts tool calls: </dt><dd className="inline">{unit === 'cost' ? (r.spec.include_tool_calls === true ? 'yes' : 'no, model calls only') : 'not applicable'}</dd></div>
      <div><dt className="inline font-medium">Thresholds: </dt><dd className="inline">{isList(r.spec.thresholds) && r.spec.thresholds.length ? r.spec.thresholds.map((t) => `${t}%`).join(', ') : '50%, 80%, 100% (default)'}</dd></div>
      <div><dt className="inline font-medium">Current window: </dt><dd className="inline">{w && typeof w.start === 'string' && typeof w.end === 'string' ? `${w.start} to ${w.end}` : 'unknown'}</dd></div>
      <div><dt className="inline font-medium">Reserved: </dt><dd className="inline">{amountText(s?.reserved, unit, currency)}</dd></div>
    </dl>
    {q.isPending && <p role="status">Loading status…</p>}
    {q.isError && <p role="alert" className="text-status-denied-fg">{isQuotaplexRequired(q.error) ? QUOTAPLEX_OFF : `Could not load this budget's status: ${msg(q.error)}.`}{s ? ` Shown from the last successful refresh at ${new Date(q.dataUpdatedAt).toLocaleTimeString()}.` : ' Its counters are unknown.'}</p>}
    {s && <>
      <div><h3 className="text-sm font-medium">Threshold events</h3>
        {events ? (events.length ? <ul className="text-sm">{events.map((e) => <li key={e.percent}>{e.percent}% reached{e.at ? ` at ${e.at}` : ''}{typeof e.consumed === 'string' ? `, ${e.consumed} used` : ''}</li>)}</ul> : <p className="text-sm">none</p>)
          : <p className="text-sm">{full ? 'none' : 'unknown: this view does not include them'}</p>}</div>
      <div><h3 className="text-sm font-medium">Child budgets</h3>
        {/* children is omitempty and a child whose counters could not be read is skipped with a note (budgets.go budgetStatus), so absent means none only without such a note. */}
        {children ? (children.length ? <ul className="text-sm">{children.map((c) => <li key={c.id}>{c.display_name || c.id}: {amountText(c.spent, c.unit, c.currency)} used of {amountText(c.limit, c.unit, c.currency)}, {amountText(c.remaining, c.unit, c.currency)} remaining</li>)}</ul> : <p className="text-sm">none</p>)
          : <p className="text-sm">{!full ? 'unknown: this view does not include them' : childNote ? 'unknown: some child counters are unavailable (see the notes)' : 'none'}</p>}</div>
      <div><h3 className="text-sm font-medium">Top spenders</h3>
        {/* top_spenders is omitted both when nobody spent and when the ledger is unavailable or the caller is not an admin (budgets.go: budgetSpenders, `spenders` flag), so absent is unknown. */}
        {spenders ? (spenders.length ? <ul className="text-sm">{spenders.map((p) => <li key={p.principal_id}>{p.display || p.principal_id}: {spenderText(p, unit, currency)}</li>)}</ul> : <p className="text-sm">none in this window</p>)
          : <p className="text-sm">unknown: not reported (admins only, and the usage ledger must be readable)</p>}</div>
      {notes && notes.length > 0 && <ul className="text-sm list-disc pl-5">{notes.map((n) => <li key={n}>{n}</li>)}</ul>}
    </>}
  </div>
}

// ── form ──

interface FormValues {
  id: string; displayName: string; parent: string
  unit: string; currency: string; amount: string
  windowKind: string; windowUnit: string; timeZone: string; hours: string
  groups: GroupRef[]; principals: string; agents: string; clients: string; accessPoints: string; projects: string
  onUnknown: string; includeTools: boolean; thresholds: string
}

export const UNITS = ['cost', 'tokens', 'requests']
export const WINDOW_KINDS = ['calendar', 'tumbling']   // pkg/budgets/budgets.go:49-50: the only kinds Period() accepts, so an unmodelled kind is not reachable today
const WINDOW_UNITS = ['day', 'week', 'month']
const UNKNOWN_CONSOLE = '(not known to this Console)'
const MEMBER_KEYS = ['groups', 'principals', 'agents', 'client_registrations', 'access_points', 'projects'] as const

const isGroup = (g: unknown) => !!g && typeof g === 'object' && typeof (g as GroupRef).source === 'string' && typeof (g as GroupRef).id === 'string'
/**
 * Stored values the form cannot show or edit faithfully. They are shown as they are and sent back exactly as stored,
 * so saving some other field never rewrites them.
 */
function keptKeys(base: BudgetSpec | null): Set<string> {
  const keep = new Set<string>()
  if (!base) return keep
  for (const k of ['display_name', 'parent'] as const) if (base[k] !== undefined && typeof base[k] !== 'string') keep.add(k)
  if (base.thresholds != null && !(isList(base.thresholds) && base.thresholds.every((t) => typeof t === 'number'))) keep.add('thresholds')
  if (base.window && !WINDOW_KINDS.includes(base.window.kind)) keep.add('window')
  const m = base.members
  if (m && typeof m === 'object') for (const k of MEMBER_KEYS) {
    const v = (m as Record<string, unknown>)[k]
    if (v == null) continue
    if (!isList(v) || !v.every((x) => (k === 'groups' ? isGroup(x) : typeof x === 'string'))) keep.add(`members.${k}`)
  }
  return keep
}
const rawJSON = (v: unknown) => JSON.stringify(v)

const KEEP_OF: Record<string, string> = { displayName: 'display_name', thresholds: 'thresholds', principals: 'principals', agents: 'agents', clients: 'client_registrations', accessPoints: 'access_points', projects: 'projects' }
const lines = (v: unknown) => strings(v).join('\n')
const splitLines = (t: string) => t.split('\n').map((x) => x.trim()).filter(Boolean)

function initial(spec: BudgetSpec | null): FormValues {
  const m = spec?.members && typeof spec.members === 'object' ? spec.members : {}
  const w = spec?.window
  return {
    id: spec?.id ?? '', displayName: str(spec?.display_name) ?? '', parent: str(spec?.parent) ?? '',
    unit: str(spec?.limit?.unit) ?? 'cost', currency: str(spec?.limit?.currency) ?? '', amount: str(spec?.limit?.amount) ?? '',
    windowKind: str(w?.kind) ?? 'calendar', windowUnit: str(w?.unit) ?? 'month', timeZone: str(w?.time_zone) ?? 'UTC', hours: typeof w?.hours === 'number' ? String(w.hours) : '',
    groups: groupsOf(m.groups).map((g) => ({ ...g })), principals: lines(m.principals), agents: lines(m.agents), clients: lines(m.client_registrations),
    accessPoints: lines(m.access_points), projects: lines(m.projects),
    onUnknown: str(spec?.on_unknown_cost) ?? '', includeTools: spec?.include_tool_calls === true,
    thresholds: isList(spec?.thresholds) ? spec.thresholds.join(', ') : '',
  }
}

function parseThresholds(t: string): number[] | null {
  const parts = t.split(',').map((x) => x.trim()).filter(Boolean)
  if (parts.some((p) => !/^\d{1,3}$/.test(p))) return null
  return parts.map(Number)
}

/** The specification the form states. Starts from the stored one, so a field the form does not model is sent back unchanged. */
function buildSpec(base: BudgetSpec | null, f: FormValues): BudgetSpec {
  const keep = keptKeys(base)
  const spec: BudgetSpec = { ...(base ?? {}), id: f.id.trim() } as BudgetSpec
  const opt = (key: 'display_name' | 'parent', v: string) => { if (keep.has(key)) return; if (v.trim()) spec[key] = v.trim(); else delete spec[key] }
  opt('display_name', f.displayName); opt('parent', f.parent)
  const cost = f.unit === 'cost'
  const modelled = UNITS.includes(f.unit)   // an unit this Console does not know keeps its cost-only fields as stored
  const limit: BudgetSpec['limit'] = { ...(base?.limit ?? {}), unit: f.unit, amount: f.amount.trim() }
  if (cost) limit.currency = f.currency.trim(); else if (modelled) delete limit.currency
  spec.limit = limit
  if (!keep.has('window')) {
    const w: BudgetSpec['window'] = { ...(base?.window ?? {}), kind: f.windowKind }
    if (f.windowKind === 'calendar') { w.unit = f.windowUnit; w.time_zone = f.timeZone.trim(); delete w.hours } else { w.hours = Number(f.hours); delete w.unit; delete w.time_zone }
    spec.window = w
  } else if (base) spec.window = base.window
  const members: NonNullable<BudgetSpec['members']> = { ...(base?.members && typeof base.members === 'object' ? base.members : {}) }
  const setList = (key: string, v: unknown[]) => { if (keep.has(`members.${key}`)) return; if (v.length) members[key] = v; else delete members[key] }
  // a group keeps any fields of its own the form does not model
  const stored = base?.members && typeof base.members === 'object' && isList(base.members.groups) ? base.members.groups as GroupRef[] : []
  setList('groups', f.groups.map((g) => ({ ...(stored.find((x) => x.source === g.source && x.id === g.id) ?? {}), source: g.source.trim(), id: g.id.trim() })).filter((g) => g.source || g.id))
  setList('principals', splitLines(f.principals)); setList('agents', splitLines(f.agents)); setList('client_registrations', splitLines(f.clients))
  setList('access_points', splitLines(f.accessPoints)); setList('projects', splitLines(f.projects))
  if (Object.keys(members).length) spec.members = members; else delete spec.members
  if (cost) { if (f.onUnknown) spec.on_unknown_cost = f.onUnknown; else delete spec.on_unknown_cost } else if (modelled) delete spec.on_unknown_cost
  if (cost) { if (f.includeTools) spec.include_tool_calls = true; else delete spec.include_tool_calls } else if (modelled) delete spec.include_tool_calls
  if (!keep.has('thresholds')) { const th = parseThresholds(f.thresholds); if (th && th.length) spec.thresholds = th; else delete spec.thresholds }
  return spec
}

function problems(f: FormValues, creating: boolean, keep: Set<string>): string[] {
  const out: string[] = []
  if (creating && !f.id.trim()) out.push('Enter an id.')
  if (!f.amount.trim()) out.push('Enter the limit amount.')
  if (f.unit === 'cost' && !f.currency.trim()) out.push('A cost limit needs a currency.')
  if (!keep.has('window') && f.windowKind === 'calendar' && !f.timeZone.trim()) out.push('A calendar window needs a time zone.')
  if (!keep.has('window') && f.windowKind === 'tumbling' && !(Number.isInteger(Number(f.hours)) && Number(f.hours) > 0 && f.hours.trim() !== '')) out.push('A tumbling window needs a whole number of hours.')
  if (!keep.has('thresholds') && parseThresholds(f.thresholds) === null) out.push('Thresholds are whole percentages separated by commas.')
  return out
}

function GroupRows({ prefix, groups, disabled, onChange }: { prefix: string; groups: GroupRef[]; disabled: boolean; onChange: (g: GroupRef[]) => void }) {
  return <div className="space-y-2">
    {groups.map((g, i) => <div key={i} className="flex gap-2">
      <input aria-label={`${prefix} group source ${i + 1}`} className={field} placeholder="Source (issuer or provider)" value={g.source} disabled={disabled} onChange={(e) => onChange(groups.map((x, j) => (j === i ? { ...x, source: e.target.value } : x)))} />
      <input aria-label={`${prefix} group id ${i + 1}`} className={field} placeholder="Group id" value={g.id} disabled={disabled} onChange={(e) => onChange(groups.map((x, j) => (j === i ? { ...x, id: e.target.value } : x)))} />
      <button type="button" className={btn} disabled={disabled} aria-label={`Remove ${prefix.toLowerCase()} group ${i + 1}`} onClick={() => onChange(groups.filter((_, j) => j !== i))}>Remove</button>
    </div>)}
    <button type="button" className={btn} disabled={disabled} onClick={() => onChange([...groups, { source: '', id: '' }])}>Add {prefix.toLowerCase()} group</button>
  </div>
}

function BudgetForm({ form, others, onClose, onSaved, onReload }: {
  form: { mode: 'create' } | { mode: 'edit'; record: BudgetRecord }; others: string[]
  onClose: () => void; onSaved: () => void; onReload: () => void
}) {
  const editing = form.mode === 'edit' ? form.record : null
  const [f, setF] = useState<FormValues>(() => initial(editing?.spec ?? null))
  const set = <K extends keyof FormValues>(k: K, v: FormValues[K]) => { setF((p) => ({ ...p, [k]: v })); save.reset(); setConflict(false) }
  const [conflict, setConflict] = useState(false)
  const save = useMutation({
    mutationFn: () => putBudget(buildSpec(editing?.spec ?? null, f), editing ? (editing.revision as number) : 0),
    onSuccess: onSaved,
    // A conflict on create (If-Match 0) means the id exists: the form stays editable and says so. Only an edit is "changed since read".
    onError: (e) => setConflict(editing !== null && (isRevisionConflict(e) || errorStatus(e) === 404)),
  })
  const keep = keptKeys(editing?.spec ?? null)
  const issues = problems(f, !editing, keep)
  const locked = save.isPending || conflict
  const cost = f.unit === 'cost'
  const parents = f.parent && !others.includes(f.parent) ? [...others, f.parent] : others
  const kept = (label: string, raw: unknown) => <p className="text-sm"><span className="font-medium">{label}:</span> <code className="break-all">{rawJSON(raw)}</code> <span className="text-xs">This value is not one the form can edit. It is kept as stored.</span></p>
  const stored = editing?.spec as BudgetSpec | undefined
  const input = (id: string, label: string, key: 'id' | 'displayName' | 'currency' | 'amount' | 'timeZone' | 'hours' | 'thresholds', extra?: { placeholder?: string; disabled?: boolean }) =>
    keep.has(KEEP_OF[key] ?? '') ? kept(label, stored?.[KEEP_OF[key] as string])
    : <div><label htmlFor={id} className="block text-sm font-medium mb-1">{label}</label>
      <input id={id} className={field} value={f[key]} disabled={locked || extra?.disabled} placeholder={extra?.placeholder} onChange={(e) => set(key, e.target.value)} /></div>
  const area = (id: string, label: string, key: 'principals' | 'agents' | 'clients' | 'accessPoints' | 'projects') =>
    keep.has(`members.${KEEP_OF[key]}`) ? kept(label, (stored?.members as Record<string, unknown> | undefined)?.[KEEP_OF[key] as string])
    : <div><label htmlFor={id} className="block text-sm font-medium mb-1">{label}</label>
      <textarea id={id} rows={2} className={field} value={f[key]} disabled={locked} placeholder="One per line" onChange={(e) => set(key, e.target.value)} /></div>
  return <div className="border rounded-lg p-4 space-y-3" role="group" aria-label={editing ? 'Edit budget' : 'Add budget'}>
    <h2 className="font-semibold">{editing ? `Edit budget ${editing.id}` : 'Add budget'}</h2>
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      {input('bd-id', 'Budget id', 'id', { disabled: !!editing, placeholder: 'For example platform-eng-monthly' })}
      {input('bd-name', 'Display name, optional', 'displayName')}
      <div><label htmlFor="bd-parent" className="block text-sm font-medium mb-1">Parent budget, optional</label>
        {keep.has('parent') ? kept('Parent budget', stored?.parent) : <select id="bd-parent" className={field} value={f.parent} disabled={locked} onChange={(e) => set('parent', e.target.value)}>
          <option value="">none</option>{parents.map((p) => <option key={p} value={p}>{p}</option>)}</select>}</div>
    </div>
    <fieldset className="grid grid-cols-1 sm:grid-cols-3 gap-3" disabled={locked}>
      <legend className="text-sm font-medium">Limit</legend>
      <div><label htmlFor="bd-unit" className="block text-sm font-medium mb-1">Limit unit</label>
        <select id="bd-unit" className={field} value={f.unit} onChange={(e) => set('unit', e.target.value)}>{UNITS.map((u) => <option key={u} value={u}>{u}</option>)}{!UNITS.includes(f.unit) && <option value={f.unit}>{f.unit} {UNKNOWN_CONSOLE}</option>}</select></div>
      {cost && input('bd-currency', 'Currency', 'currency', { placeholder: 'USD' })}
      {input('bd-amount', 'Limit amount', 'amount', { placeholder: cost ? '250.00' : 'A whole number' })}
    </fieldset>
    {keep.has('window') ? kept('Window', stored?.window) : <>
    <fieldset className="grid grid-cols-1 sm:grid-cols-3 gap-3" disabled={locked}>
      <legend className="text-sm font-medium">Window</legend>
      <div><label htmlFor="bd-wkind" className="block text-sm font-medium mb-1">Window kind</label>
        <select id="bd-wkind" className={field} value={f.windowKind} onChange={(e) => set('windowKind', e.target.value)}><option value="calendar">calendar</option><option value="tumbling">tumbling</option></select></div>
      {f.windowKind === 'calendar' ? <>
        <div><label htmlFor="bd-wunit" className="block text-sm font-medium mb-1">Window unit</label>
          <select id="bd-wunit" className={field} value={f.windowUnit} onChange={(e) => set('windowUnit', e.target.value)}>{WINDOW_UNITS.map((u) => <option key={u} value={u}>{u}</option>)}{!WINDOW_UNITS.includes(f.windowUnit) && <option value={f.windowUnit}>{f.windowUnit} {UNKNOWN_CONSOLE}</option>}</select></div>
        {input('bd-tz', 'Time zone', 'timeZone', { placeholder: 'An IANA zone, for example Europe/Berlin' })}
      </> : input('bd-hours', 'Window hours', 'hours', { placeholder: 'Length of each window in hours' })}
    </fieldset>
    </>}
    <fieldset className="space-y-3" disabled={locked}>
      <legend className="text-sm font-medium">Members (a request draws from the budget when any selector matches)</legend>
      {keep.has('members.groups') ? kept('Groups', (stored?.members as Record<string, unknown> | undefined)?.groups) : <div><span className="block text-sm font-medium mb-1">Groups</span><GroupRows prefix="Member" groups={f.groups} disabled={locked} onChange={(g) => set('groups', g)} /></div>}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        {area('bd-principals', 'Principals', 'principals')}{area('bd-agents', 'Agents', 'agents')}{area('bd-clients', 'Client registrations', 'clients')}
        {area('bd-aps', 'Access points', 'accessPoints')}{area('bd-projects', 'Projects', 'projects')}
      </div>
    </fieldset>
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      {cost && <div><label htmlFor="bd-unknown" className="block text-sm font-medium mb-1">On unknown cost</label>
        <select id="bd-unknown" className={field} value={f.onUnknown} disabled={locked} onChange={(e) => set('onUnknown', e.target.value)}>
          <option value="">default (deny)</option><option value="deny">deny: refuse a request whose cost cannot be computed</option><option value="allow_and_flag">allow_and_flag: admit it and count it as unknown cost</option>{!['', 'deny', 'allow_and_flag'].includes(f.onUnknown) && <option value={f.onUnknown}>{f.onUnknown} {UNKNOWN_CONSOLE}</option>}</select></div>}
      {input('bd-thresholds', 'Thresholds (percent), optional', 'thresholds', { placeholder: 'Empty: 50, 80, 100' })}
    </div>
    {cost && <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={f.includeTools} disabled={locked} onChange={(e) => set('includeTools', e.target.checked)} />
      <span>Include tool calls: also count managed tool calls at the quota policy's price. Without it a cost budget counts model calls only.</span></label>}
    {!cost && UNITS.includes(f.unit) && <p className="text-xs">Currency, unknown-cost handling and tool-call counting apply to cost budgets only and are not sent for a {f.unit} budget.</p>}
    <Preview base={editing?.spec ?? null} f={f} issues={issues} />
    {conflict && <div role="alert" className="text-status-denied-fg"><p>{editing && errorStatus(save.error) === 404 ? 'This budget no longer exists. Nothing was saved.' : CHANGED}</p>
      <button type="button" className={`${btn} mt-2`} onClick={onReload}>Reload latest version</button></div>}
    {save.isError && !conflict && !(!editing && isRevisionConflict(save.error)) && <p role="alert" className="text-status-denied-fg">{isQuotaplexRequired(save.error) ? QUOTAPLEX_OFF : `Could not save: ${msg(save.error)}${invalidField(save.error) ? ` (field ${invalidField(save.error)})` : ''}`}</p>}
    {save.isError && !editing && isRevisionConflict(save.error) && <p role="alert" className="text-status-denied-fg">A budget with this id already exists. Edit that one instead.</p>}
    {issues.length > 0 && <ul className="text-xs list-disc pl-5">{issues.map((i) => <li key={i}>{i}</li>)}</ul>}
    <div className="flex gap-2">
      <button type="button" className={primary} disabled={issues.length > 0 || locked} onClick={() => { setConflict(false); save.mutate() }}>{save.isPending ? 'Saving…' : editing ? 'Save changes' : 'Create budget'}</button>
      <button type="button" className={btn} disabled={save.isPending} onClick={onClose}>Cancel</button>
    </div>
  </div>
}

// ── preview ──

function Preview({ base, f, issues }: { base: BudgetSpec | null; f: FormValues; issues: string[] }) {
  const [principal, setPrincipal] = useState('')
  const [agent, setAgent] = useState('')
  const [client, setClient] = useState('')
  const [groups, setGroups] = useState<GroupRef[]>([])
  const [accessPoint, setAccessPoint] = useState('')
  const [plane, setPlane] = useState<string>('llmplex')
  const request = () => ({
    subject: {
      ...(principal.trim() ? { principal: principal.trim() } : {}), ...(agent.trim() ? { agent: agent.trim() } : {}), ...(client.trim() ? { client: client.trim() } : {}),
      ...(groups.some((g) => g.source.trim() || g.id.trim()) ? { groups: groups.filter((g) => g.source.trim() || g.id.trim()).map((g) => ({ source: g.source.trim(), id: g.id.trim() })) } : {}),
    },
    ...(accessPoint.trim() ? { access_point: accessPoint.trim() } : {}), plane, draft: buildSpec(base, f),
  })
  const snapshot = JSON.stringify(request())
  // Each answer carries the snapshot it was asked for, and only the newest request may set the answer: an older one arriving last is dropped.
  const [answer, setAnswer] = useState<{ snapshot: string; result: BudgetPreview } | null>(null)
  const latest = useRef(0)
  const run = useMutation({
    mutationFn: (v: { req: ReturnType<typeof request>; snapshot: string; seq: number }) => previewBudgets(v.req),
    onSuccess: (result, v) => { if (v.seq === latest.current) setAnswer({ snapshot: v.snapshot, result }) },
  })
  const current = answer !== null && answer.snapshot === snapshot
  return <fieldset className="border rounded p-3 space-y-3">
    <legend className="text-sm font-medium px-1">Preview this draft</legend>
    <p className="text-xs">Ask the server which budgets a hypothetical request would draw from, with this draft included. Nothing is saved and nothing in the caller is verified. Name a caller or an access point: with neither, no member selector can match.</p>
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <div><label htmlFor="pv-principal" className="block text-sm font-medium mb-1">Preview principal</label><input id="pv-principal" className={field} value={principal} onChange={(e) => setPrincipal(e.target.value)} /></div>
      <div><label htmlFor="pv-agent" className="block text-sm font-medium mb-1">Preview agent</label><input id="pv-agent" className={field} value={agent} onChange={(e) => setAgent(e.target.value)} /></div>
      <div><label htmlFor="pv-client" className="block text-sm font-medium mb-1">Preview client registration</label><input id="pv-client" className={field} value={client} onChange={(e) => setClient(e.target.value)} /></div>
      <div><label htmlFor="pv-ap" className="block text-sm font-medium mb-1">Preview access point</label><input id="pv-ap" className={field} value={accessPoint} onChange={(e) => setAccessPoint(e.target.value)} /></div>
      <div><label htmlFor="pv-plane" className="block text-sm font-medium mb-1">Preview plane</label>
        <select id="pv-plane" className={field} value={plane} onChange={(e) => setPlane(e.target.value)}>{PLANES.map((p) => <option key={p} value={p}>{p}</option>)}</select></div>
    </div>
    <div><span className="block text-sm font-medium mb-1">Preview groups</span><GroupRows prefix="Preview" groups={groups} disabled={false} onChange={setGroups} /></div>
    <button type="button" className={btn} disabled={issues.length > 0} title={issues[0]} onClick={() => { latest.current += 1; run.mutate({ req: request(), snapshot, seq: latest.current }) }}>Preview</button>
    {run.isPending && <p role="status" className="text-sm">Previewing…</p>}
    {run.isError && <p role="alert" className="text-status-denied-fg text-sm">{isQuotaplexRequired(run.error) ? QUOTAPLEX_OFF : `Could not preview: ${msg(run.error)}`}</p>}
    {answer && !current && <p role="status" className="text-sm">The form changed since this preview. Preview again.</p>}
    {current && answer && <PreviewResult p={answer.result} />}
  </fieldset>
}

function PreviewResult({ p }: { p: BudgetPreview }) {
  const draws = isList(p.draws) ? p.draws : undefined
  const skipped = isList(p.skipped) ? p.skipped : undefined
  const notes = isList(p.notes) ? p.notes : undefined
  return <div className="space-y-2 text-sm" role="region" aria-label="Preview result">
    {p.draft ? (p.draft.valid
      ? <p>The draft is valid.</p>
      : <p role="alert" className="text-status-denied-fg">The draft is not valid{p.draft.field ? ` (field ${p.draft.field})` : ''}: {p.draft.error ?? 'no reason given'}. It is not part of the answer below and could not be saved.</p>)
      : <p>The server did not report whether the draft is valid.</p>}
    <p>Plane {p.plane ?? 'unknown'}{p.access_point ? `, access point ${p.access_point}` : ''}{p.project ? `, project ${p.project}` : ''}.</p>
    <div><h4 className="font-medium">Would draw from</h4>
      {draws ? (draws.length ? <ul className="space-y-1">{draws.map((d) => <li key={d.id}>
        <strong>{d.display_name || d.id}</strong>{d.draft && ' (this draft)'}: {amountText(d.limit, d.unit, d.currency)}, {windowText(d.window)}
        {d.parent ? `, under ${d.parent}` : ''}
        {d.direct === true ? ', matched directly' : d.via_child ? `, through its child ${d.via_child}` : ''}
        {d.include_tool_calls === true && ', counts tool calls'}
        {isList(d.reasons) && d.reasons.length > 0 && <span className="block text-xs">Matched by {d.reasons.join('; ')}</span>}
      </li>)}</ul> : <p>No budget would count this request.</p>) : <p>unknown: the server did not list the budgets.</p>}</div>
    <div><h4 className="font-medium">Matched but not counted</h4>
      {skipped ? (skipped.length ? <ul>{skipped.map((s) => <li key={s.id}><strong>{s.id}</strong>: {s.reason}</li>)}</ul> : <p>none</p>) : <p>unknown</p>}</div>
    {notes && notes.length > 0 && <ul className="list-disc pl-5">{notes.map((n) => <li key={n}>{n}</li>)}</ul>}
  </div>
}
