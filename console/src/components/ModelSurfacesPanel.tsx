import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import * as api from '../api/modelSurfaces'
import ModelSurfaceEditor from './ModelSurfaceEditor'
import SurfaceCapacityPanel from './SurfaceCapacityPanel'

const button = 'rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900'

const authCell = (s: api.SurfaceSpec) => s.identity_ref ? `identity reference: ${s.identity_ref}` : s.auth?.secret_ref ? `secret reference: ${s.auth.secret_ref}` : s.auth ? `credential: ${s.auth.type}` : '—'

/** Model surfaces: list, add/edit with the provider-driven editor, capacity, delete. */
export default function ModelSurfacesPanel() {
  const client = useQueryClient()
  const query = useQuery({ queryKey: ['model-surfaces'], queryFn: api.listSurfaces })
  const [editing, setEditing] = useState<{ record?: api.SurfaceRecord } | null>(null)
  const [capacityOf, setCapacityOf] = useState<string | null>(null)
  const [confirmDelete, setConfirmDelete] = useState<api.SurfaceRecord | null>(null)
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)

  async function remove(record: api.SurfaceRecord) {
    setBusy(true); setMessage('')
    try {
      await api.deleteSurface(record.id, record.revision)
      setConfirmDelete(null)
      await client.invalidateQueries({ queryKey: ['model-surfaces'] })
    } catch (err) {
      const status = (err as { status?: number } | null)?.status
      setMessage(status === 409 ? `${record.id} was changed by someone else, so it was not deleted. The list has been refreshed.` : status === 403 ? 'Only administrators can delete model surfaces.' : err instanceof Error ? err.message : String(err))
      if (status === 409) { setConfirmDelete(null); await client.invalidateQueries({ queryKey: ['model-surfaces'] }) }
    } finally { setBusy(false) }
  }

  return <section aria-label="Model surfaces" className="space-y-4">
    <div className="flex items-center justify-between gap-3"><h3 className="text-lg font-semibold">Model surfaces</h3><button type="button" className={button} onClick={() => setEditing({})}>Add surface</button></div>
    <p className="text-sm text-gray-600 dark:text-gray-400">A surface connects AIPlex to a provider endpoint (OpenAI, Anthropic, Azure OpenAI, Bedrock, an OpenAI-compatible server or the Gemini Developer API) and fixes which operations and models callers may use. Credentials are references; no key is stored or shown here.</p>
    {message && <p role="alert" className="rounded border border-status-denied-fg p-3 text-sm text-status-denied-fg">{message}</p>}

    {query.isPending ? <p role="status">Loading model surfaces…</p>
      : query.isError ? <p role="alert" className="text-status-denied-fg">Could not load model surfaces: {(query.error as Error).message}</p>
      : query.data.length === 0 ? <p className="text-sm text-gray-600 dark:text-gray-400">No model surfaces yet.</p>
      : <div className="overflow-x-auto"><table className="w-full text-left text-sm">
        <caption className="sr-only">Model surfaces</caption>
        <thead><tr className="border-b border-gray-200 dark:border-gray-800">{['ID', 'Provider', 'Protocol', 'Operations', 'Models', 'Credential (reference)', 'Capacity policy', 'Revision', 'Actions'].map(h => <th key={h} scope="col" className="py-2 pr-4">{h}</th>)}</tr></thead>
        <tbody>{query.data.map(r => {
          const provider = r.spec.surface.provider?.id
          return <tr key={r.id} className="border-t border-gray-200 align-top dark:border-gray-800">
            <th scope="row" className="py-2 pr-4 font-normal">{r.id}</th>
            <td className="pr-4">{provider ?? 'google-cloud'}</td><td className="pr-4">{r.spec.surface.protocol}</td>
            <td className="pr-4">{r.spec.surface.operations?.length ?? 0}</td><td className="pr-4">{(r.spec.surface.models ?? []).join(', ') || '—'}</td>
            <td className="pr-4">{authCell(r.spec)}</td><td className="pr-4">{r.spec.capacity_policy ? 'on' : 'off'}</td><td className="pr-4">{r.revision}</td>
            <td className="whitespace-nowrap">{confirmDelete?.id === r.id
              ? <span>Delete {r.id}? <button type="button" className="underline" disabled={busy} onClick={() => void remove(r)}>Confirm delete</button> <button type="button" className="underline" onClick={() => setConfirmDelete(null)}>Cancel</button></span>
              : <>
                {provider ? <button type="button" className="mr-3 underline" aria-label={`Edit surface ${r.id}`} onClick={() => { setEditing({ record: r }); setMessage('') }}>Edit</button> : <span className="mr-3 text-xs text-gray-600 dark:text-gray-400" title="Google Cloud surfaces are edited with the CLI or API">Google Cloud</span>}
                {provider && <button type="button" className="mr-3 underline" aria-label={`Capacity of surface ${r.id}`} onClick={() => setCapacityOf(capacityOf === r.id ? null : r.id)}>Capacity</button>}
                <button type="button" className="underline" aria-label={`Delete surface ${r.id}`} onClick={() => { setConfirmDelete(r); setMessage('') }}>Delete</button>
              </>}</td>
          </tr>
        })}</tbody>
      </table></div>}

    {capacityOf && <SurfaceCapacityPanel key={capacityOf} surfaceId={capacityOf} />}
    {editing && <ModelSurfaceEditor key={editing.record ? `${editing.record.id}@${editing.record.revision}` : 'new'} record={editing.record} onClose={() => setEditing(null)} />}
  </section>
}
