import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { describeAuth, describePolicy, getToolAuthCatalog, getToolConnection, listToolConnections, type ToolConnectionRecord } from '../api/toolAuth'
import ToolAuthEditor from './ToolAuthEditor'

export default function ToolConnectionsPanel() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<string | null>(null)
  const connections = useQuery({ queryKey: ['tool-connections'], queryFn: listToolConnections })
  const catalog = useQuery({ queryKey: ['tool-auth-types'], queryFn: getToolAuthCatalog })

  const record = connections.data?.find((r) => r.id === editing)

  const replace = (updated: ToolConnectionRecord) => {
    queryClient.setQueryData<ToolConnectionRecord[]>(['tool-connections'], (prev) => (prev ?? []).map((r) => (r.id === updated.id ? updated : r)))
  }
  const reload = async (id: string) => {
    replace(await getToolConnection(id))
  }

  if (connections.isLoading || catalog.isLoading) return <p role="status">Loading tool connections…</p>
  if (connections.error) return <p role="alert" className="text-status-denied-fg mb-4">Could not load tool connections: {connections.error.message}</p>

  return (
    <div>
      {catalog.error && (
        <p role="alert" className="text-status-denied-fg mb-4">
          Could not load auth types, so auth cannot be edited: {catalog.error.message}{' '}
          <button type="button" className="underline" onClick={() => catalog.refetch()}>Retry</button>
        </p>
      )}
      {connections.data?.length === 0 ? (
        <p className="text-gray-600 dark:text-gray-400">No tool connections yet.</p>
      ) : (
        <div className="bg-white rounded-lg shadow dark:bg-gray-900">
          <table className="w-full text-sm">
            <caption className="sr-only">Tool connections with their auth type and tool policy</caption>
            <thead className="bg-gray-50 dark:bg-gray-900">
              <tr>
                <th scope="col" className="text-left px-4 py-3">Connection</th>
                <th scope="col" className="text-left px-4 py-3">Auth</th>
                <th scope="col" className="text-left px-4 py-3">Tool policy</th>
                <th scope="col" className="text-left px-4 py-3"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {connections.data?.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="px-4 py-3 font-medium">{r.id}</td>
                  <td className="px-4 py-3 text-gray-700 dark:text-gray-300">{describeAuth(r.spec, catalog.data)}</td>
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{describePolicy(r.spec.tools)}</td>
                  <td className="px-4 py-3 text-right">
                    <button type="button" disabled={!catalog.data} onClick={() => setEditing(r.id)} aria-label={`Edit auth for ${r.id}`}
                      className="px-3 py-1.5 bg-gray-100 text-sm rounded hover:bg-gray-200 disabled:opacity-50 dark:bg-gray-800">Edit auth</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {record && catalog.data && (
        <ToolAuthEditor key={`${record.id}@${record.revision}`} record={record} catalog={catalog.data}
          onSaved={(saved) => { replace(saved); setEditing(null) }}
          onReload={() => reload(record.id)}
          onCancel={() => setEditing(null)} />
      )}
    </div>
  )
}
