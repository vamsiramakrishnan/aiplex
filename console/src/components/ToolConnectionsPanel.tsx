import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { describeAuth, describePolicy, getToolAuthCatalog, getToolConnection, listToolConnections, type ToolConnectionRecord } from '../api/toolAuth'
import { errorStatus, knownRevision, rulesOf } from '../api/toolConnections'
import ToolAuthEditor from './ToolAuthEditor'
import ToolConnectionCreate from './ToolConnectionCreate'
import ToolConnectionDelete from './ToolConnectionDelete'
import ToolConnectionTools from './ToolConnectionTools'
import ToolRulesPanel from './ToolRulesPanel'

type View = { kind: 'create' } | { kind: 'auth' | 'tools' | 'rules' | 'delete'; id: string }

const btn = 'px-3 py-1.5 bg-gray-100 text-sm rounded hover:bg-gray-200 disabled:opacity-50 dark:bg-gray-800'

export default function ToolConnectionsPanel() {
  const queryClient = useQueryClient()
  const [view, setView] = useState<View | null>(null)
  const connections = useQuery({ queryKey: ['tool-connections'], queryFn: listToolConnections })
  const catalog = useQuery({ queryKey: ['tool-auth-types'], queryFn: getToolAuthCatalog })

  const rows = connections.data
  // A failed refresh keeps the rows already on screen; they may be out of date, so nothing is editable until a reload succeeds.
  const stale = connections.isError && !!rows
  const record = view && view.kind !== 'create' ? rows?.find((r) => r.id === view.id) : undefined

  const replace = (updated: ToolConnectionRecord) => {
    queryClient.setQueryData<ToolConnectionRecord[]>(['tool-connections'], (prev) => (prev ?? []).map((r) => (r.id === updated.id ? updated : r)))
  }
  const reload = async (id: string) => {
    replace(await getToolConnection(id))
  }
  const refreshList = () => queryClient.invalidateQueries({ queryKey: ['tool-connections'] })

  if (connections.isLoading || catalog.isLoading) return <p role="status">Loading tool connections…</p>
  if (connections.isError && !rows) {
    return (
      <div role="alert" className="text-status-denied-fg mb-4">
        <p>Could not load tool connections: {connections.error.message}. This is an error, not an empty list.</p>
        <button type="button" className="underline" onClick={() => void connections.refetch()}>Retry</button>
      </div>
    )
  }

  const rowWhy = (r: ToolConnectionRecord) => (stale ? 'Reload first: the list may be out of date.' : !knownRevision(r.revision) ? 'The revision is unknown. Reload first.' : undefined)

  return (
    <div>
      {catalog.error && (
        <p role="alert" className="text-status-denied-fg mb-4">
          Could not load auth types, so connections cannot be created or their auth edited: {catalog.error.message}{' '}
          <button type="button" className="underline" onClick={() => catalog.refetch()}>Retry</button>
        </p>
      )}
      {stale && (
        <div role="alert" className="text-status-denied-fg mb-4">
          <p>Could not refresh tool connections: {connections.error?.message}. The rows below were read earlier and may be out of date.</p>
          <button type="button" className="underline" onClick={() => void connections.refetch()}>Retry</button>
        </div>
      )}
      <div className="flex gap-2 mb-4">
        <button type="button" className="px-3 py-1.5 bg-brand-600 text-brand-contrast text-sm rounded disabled:opacity-50" disabled={!catalog.data || stale || view?.kind === 'create'}
          title={stale ? 'Reload first: the list may be out of date.' : catalog.data ? undefined : 'The auth types did not load'} onClick={() => setView({ kind: 'create' })}>Add connection</button>
        <button type="button" className={btn} disabled={connections.isFetching} onClick={() => void connections.refetch()}>{connections.isFetching ? 'Loading…' : 'Reload'}</button>
      </div>

      {view?.kind === 'create' && catalog.data && (
        <ToolConnectionCreate catalog={catalog.data} onCancel={() => setView(null)}
          onCreated={(created) => {
            queryClient.setQueryData<ToolConnectionRecord[]>(['tool-connections'], (prev) => [...(prev ?? []).filter((r) => r.id !== created.id), created])
            setView(null)
            void refreshList()
          }} />
      )}

      {rows?.length === 0 ? (
        <p className="text-gray-600 dark:text-gray-400">No tool connections yet.</p>
      ) : (
        <div className="bg-white rounded-lg shadow dark:bg-gray-900 overflow-x-auto">
          <table className="w-full text-sm">
            <caption className="sr-only">{stale ? 'Tool connections (possibly out of date)' : 'Tool connections with their auth type and tool policy'}</caption>
            <thead className="bg-gray-50 dark:bg-gray-900">
              <tr>
                <th scope="col" className="text-left px-4 py-3">Connection</th>
                <th scope="col" className="text-left px-4 py-3">Auth</th>
                <th scope="col" className="text-left px-4 py-3">Tool policy</th>
                <th scope="col" className="text-left px-4 py-3">Rules</th>
                <th scope="col" className="text-left px-4 py-3"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {rows?.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="px-4 py-3 font-medium">{r.id}</td>
                  <td className="px-4 py-3 text-gray-700 dark:text-gray-300">{describeAuth(r.spec, catalog.data)}</td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{describePolicy(r.spec.tools)}</td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{rulesOf(r.spec).length}</td>
                  <td className="px-4 py-3 text-right whitespace-nowrap space-x-2">
                    <button type="button" disabled={!catalog.data || stale || !knownRevision(r.revision)} title={rowWhy(r)} onClick={() => setView({ kind: 'auth', id: r.id })} aria-label={`Edit auth for ${r.id}`} className={btn}>Edit auth</button>
                    <button type="button" onClick={() => setView({ kind: 'tools', id: r.id })} aria-label={`List tools of ${r.id}`} className={btn}>List tools</button>
                    <button type="button" onClick={() => setView({ kind: 'rules', id: r.id })} aria-label={`Rules of ${r.id}`} className={btn}>Rules</button>
                    <button type="button" disabled={stale || !knownRevision(r.revision)} title={rowWhy(r)} onClick={() => setView({ kind: 'delete', id: r.id })} aria-label={`Delete ${r.id}`} className={btn}>Delete</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {record && view?.kind === 'auth' && catalog.data && (
        <ToolAuthEditor key={`${record.id}@${record.revision}`} record={record} catalog={catalog.data}
          onSaved={(saved) => { replace(saved); setView(null) }}
          onReload={() => reload(record.id)}
          onCancel={() => setView(null)} />
      )}
      {record && view?.kind === 'tools' && <ToolConnectionTools key={record.id} id={record.id} onClose={() => setView(null)} />}
      {record && view?.kind === 'rules' && (
        <ToolRulesPanel key={record.id} record={record} stale={stale}
          onSaved={replace} onReload={() => reload(record.id)} onClose={() => setView(null)} />
      )}
      {record && view?.kind === 'delete' && (
        <ToolConnectionDelete key={`${record.id}@${record.revision}`} record={record}
          onDeleted={() => {
            queryClient.setQueryData<ToolConnectionRecord[]>(['tool-connections'], (prev) => (prev ?? []).filter((r) => r.id !== record.id))
            setView(null)
            void refreshList()
          }}
          onReload={async () => {
            try { await reload(record.id) } catch (e) {
              if (errorStatus(e) !== 404) throw e
              // Deleted by someone else: the list is stale, not the connection.
              setView(null)
              await refreshList()
            }
          }}
          onCancel={() => setView(null)} />
      )}
      {view && view.kind !== 'create' && !record && <p role="alert" className="text-status-denied-fg mt-4">{view.id} is no longer in the list. Reload the list.</p>}
    </div>
  )
}
