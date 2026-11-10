import { useId, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import * as api from '../api/modelSurfaces'
import { ErrorNotice } from './Forbidden'

const muted = 'text-xs text-gray-600 dark:text-gray-400'
const DAY_MS = 24 * 60 * 60 * 1000

type StateFilter = 'open' | 'settled' | ''
type AgeFilter = '' | '1d' | '7d'
const AGE_MS: Record<Exclude<AgeFilter, ''>, number> = { '1d': DAY_MS, '7d': 7 * DAY_MS }

/** How long ago an ISO time was, in whole units ("3 days"), or undefined when it has no usable time. */
export function ageText(iso: string | undefined, now: number = Date.now()): string | undefined {
  const t = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(t)) return undefined
  const ms = Math.max(0, now - t)
  const days = Math.floor(ms / DAY_MS)
  if (days >= 1) return `${days} ${days === 1 ? 'day' : 'days'}`
  const hours = Math.floor(ms / 3_600_000)
  if (hours >= 1) return `${hours} ${hours === 1 ? 'hour' : 'hours'}`
  return `${Math.floor(ms / 60_000)} min`
}
/** An open batch created more than a day ago: its results were probably never fetched. */
export const isStaleOpen = (b: Pick<api.ModelBatch, 'state' | 'created_at'>, now: number = Date.now()): boolean => {
  const t = b.created_at ? Date.parse(b.created_at) : NaN
  return b.state === 'open' && !Number.isNaN(t) && now - t > DAY_MS
}
const when = (iso?: string) => { if (!iso) return '—'; const d = new Date(iso); return Number.isNaN(d.getTime()) ? iso : d.toLocaleString() }

/** Provider batch jobs created through the gateway, newest first. */
export default function OpenBatches() {
  const uid = useId()
  const [state, setState] = useState<StateFilter>('open')
  const [order, setOrder] = useState<'newest' | 'id'>('newest')
  const [age, setAge] = useState<AgeFilter>('')
  // The cutoff is resolved when the choice changes so the query key stays stable between renders.
  const [olderThan, setOlderThan] = useState<string | undefined>(undefined)
  const [cursors, setCursors] = useState<string[]>([''])
  const cursor = cursors[cursors.length - 1]
  const q = useQuery({ queryKey: ['model-batches', state, order, olderThan ?? '', cursor], queryFn: () => api.listBatches({ state: state || undefined, order, older_than: olderThan, cursor, limit: 50 }), retry: false })
  const reset = () => setCursors([''])
  const now = Date.now()
  const select = 'rounded border border-gray-300 bg-transparent px-3 py-2 text-sm dark:border-gray-700'
  return <section aria-label="Open batches" className="space-y-3">
    <h3 className="text-lg font-medium">{state === 'open' ? 'Open batches' : state === 'settled' ? 'Settled batches' : 'All batches'}</h3>
    <p role="note" className="rounded border border-status-warning-fg bg-status-warning-bg p-3 text-sm text-status-warning-fg">An open batch holds quota exposure and a concurrency slot until its results are fetched through AIPlex. Until then its usage and cost stay unknown. Batches open for more than a day are highlighted: their results were probably never fetched.</p>
    <div className="flex flex-wrap gap-4">
      <div><label htmlFor={`${uid}-state`} className="mb-1 block text-xs text-gray-600 dark:text-gray-400">Show</label>
        <select id={`${uid}-state`} className={select} value={state} onChange={e => { setState(e.target.value as StateFilter); reset() }}><option value="open">Open (awaiting results)</option><option value="settled">Settled</option><option value="">All</option></select></div>
      <div><label htmlFor={`${uid}-order`} className="mb-1 block text-xs text-gray-600 dark:text-gray-400">Order</label>
        <select id={`${uid}-order`} className={select} value={order} onChange={e => { setOrder(e.target.value as 'newest' | 'id'); reset() }}><option value="newest">Newest first</option><option value="id">By record ID</option></select></div>
      <div><label htmlFor={`${uid}-age`} className="mb-1 block text-xs text-gray-600 dark:text-gray-400">Created</label>
        <select id={`${uid}-age`} className={select} value={age} onChange={e => { const v = e.target.value as AgeFilter; setAge(v); setOlderThan(v ? new Date(Date.now() - AGE_MS[v]).toISOString() : undefined); reset() }}><option value="">Any time</option><option value="1d">More than a day ago</option><option value="7d">More than 7 days ago</option></select></div>
    </div>
    {q.isPending ? <p role="status">Loading batches…</p>
      : q.isError ? <ErrorNotice error={q.error} need="admin" what="batches" onRetry={() => void q.refetch()} />
      : q.data.batches.length === 0 ? <p>{cursors.length > 1 ? 'No more batches.' : age ? 'No batches match. Batches without a creation time are left out of an age filter.' : state === 'open' ? 'No open batches. Nothing is holding quota for a batch.' : 'No batches match.'}</p>
      : <div className="overflow-x-auto"><table className="w-full text-left text-sm">
        <caption className="sr-only">Provider batches</caption>
        <thead><tr className="border-b border-gray-200 dark:border-gray-800">{['Surface', 'Provider', 'Batch', 'Created', 'Settled', 'Quota attempt', 'Model', 'State', 'Provider status', 'Items'].map(h => <th key={h} scope="col" className="py-2 pr-4">{h}</th>)}</tr></thead>
        <tbody>{q.data.batches.map(b => {
          const stale = isStaleOpen(b, now)
          return <tr key={b.id} className={`border-t border-gray-200 dark:border-gray-800 ${stale ? 'bg-status-warning-bg' : ''}`} data-stale={stale ? 'true' : undefined}>
            <th scope="row" className="py-2 pr-4 font-normal">{b.surface_id}</th><td className="pr-4">{b.provider}</td><td className="pr-4 font-mono text-xs">{b.batch_id}</td>
            <td className="pr-4 whitespace-nowrap">{when(b.created_at)}{stale && <span className="ml-1 text-xs font-medium text-status-warning-fg">open for {ageText(b.created_at, now)}</span>}</td>
            <td className="pr-4 whitespace-nowrap">{b.state === 'open' ? 'not yet' : when(b.settled_at)}</td>
            <td className="pr-4 font-mono text-xs">{b.attempt_id || '—'}</td><td className="pr-4">{b.model || '—'}</td>
            <td className="pr-4">{b.state === 'open' ? <span className="rounded bg-status-warning-bg px-1.5 py-0.5 text-xs text-status-warning-fg">open</span> : 'settled'}</td>
            <td className="pr-4">{b.provider_status || api.UNKNOWN}{b.ended && ' (ended)'}</td>
            <td>{b.expected === null || b.expected === undefined ? api.UNKNOWN : b.expected.toLocaleString('en-US')}</td>
          </tr>
        })}</tbody>
      </table></div>}
    {q.data && <div className="flex items-center gap-3">
      <button type="button" className="rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900" disabled={cursors.length <= 1} onClick={() => setCursors(c => c.slice(0, -1))}>Previous</button>
      <span className={muted}>Page {cursors.length}</span>
      <button type="button" className="rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900" disabled={!q.data.next_cursor} onClick={() => setCursors(c => [...c, q.data!.next_cursor!])}>Next</button>
    </div>}
  </section>
}
