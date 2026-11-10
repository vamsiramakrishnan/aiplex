import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import * as api from '../api/modelSurfaces'

const muted = 'text-xs text-gray-500 dark:text-gray-400'

type StateFilter = 'open' | 'settled' | ''

/** Provider batch jobs created through the gateway, newest page first by record id. */
export default function OpenBatches() {
  const [state, setState] = useState<StateFilter>('open')
  const [cursors, setCursors] = useState<string[]>([''])
  const cursor = cursors[cursors.length - 1]
  const q = useQuery({ queryKey: ['model-batches', state, cursor], queryFn: () => api.listBatches({ state: state || undefined, cursor, limit: 50 }), retry: false })
  const change = (next: StateFilter) => { setState(next); setCursors(['']) }
  return <section aria-label="Open batches" className="space-y-3">
    <h3 className="text-lg font-medium">{state === 'open' ? 'Open batches' : state === 'settled' ? 'Settled batches' : 'All batches'}</h3>
    <p role="note" className="rounded border border-amber-400 p-3 text-sm text-amber-900 dark:text-amber-200">An open batch holds quota exposure and a concurrency slot until its results are fetched through AIPlex. Until then its usage and cost stay unknown.</p>
    <label className="block text-sm"><span className="mb-1 block text-xs text-gray-500">Show</span>
      <select className="rounded border border-gray-300 bg-transparent px-3 py-2 text-sm dark:border-gray-700" value={state} onChange={e => change(e.target.value as StateFilter)}><option value="open">Open (awaiting results)</option><option value="settled">Settled</option><option value="">All</option></select>
    </label>
    {q.isPending ? <p role="status">Loading batches…</p>
      : q.isError ? <p role="alert" className="text-sm text-red-700 dark:text-red-400">{(q.error as { status?: number }).status === 403 ? 'Listing batches needs an administrator.' : `Could not load batches: ${(q.error as Error).message}`}</p>
      : q.data.batches.length === 0 ? <p>{cursors.length > 1 ? 'No more batches.' : state === 'open' ? 'No open batches. Nothing is holding quota for a batch.' : 'No batches match.'}</p>
      : <div className="overflow-x-auto"><table className="w-full text-left text-sm">
        <caption className="sr-only">Provider batches</caption>
        <thead><tr className="border-b border-gray-200 dark:border-gray-800">{['Surface', 'Provider', 'Batch', 'Quota attempt', 'Model', 'State', 'Provider status', 'Items'].map(h => <th key={h} scope="col" className="py-2 pr-4">{h}</th>)}</tr></thead>
        <tbody>{q.data.batches.map(b => <tr key={b.id} className="border-t border-gray-200 dark:border-gray-800">
          <th scope="row" className="py-2 pr-4 font-normal">{b.surface_id}</th><td className="pr-4">{b.provider}</td><td className="pr-4 font-mono text-xs">{b.batch_id}</td>
          <td className="pr-4 font-mono text-xs">{b.attempt_id || '—'}</td><td className="pr-4">{b.model || '—'}</td>
          <td className="pr-4">{b.state === 'open' ? <span className="rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-800 dark:bg-amber-900 dark:text-amber-200">open</span> : 'settled'}</td>
          <td className="pr-4">{b.provider_status || api.UNKNOWN}{b.ended && ' (ended)'}</td>
          <td>{b.expected === null || b.expected === undefined ? api.UNKNOWN : b.expected.toLocaleString('en-US')}</td>
        </tr>)}</tbody>
      </table></div>}
    {q.data && <div className="flex items-center gap-3">
      <button type="button" className="rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900" disabled={cursors.length <= 1} onClick={() => setCursors(c => c.slice(0, -1))}>Previous</button>
      <span className={muted}>Page {cursors.length}</span>
      <button type="button" className="rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900" disabled={!q.data.next_cursor} onClick={() => setCursors(c => [...c, q.data!.next_cursor!])}>Next</button>
    </div>}
  </section>
}
