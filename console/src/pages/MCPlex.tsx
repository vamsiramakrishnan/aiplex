import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { getCatalog, listInstances } from '../api/client'
import { countOf, tabLabel } from '../api/counts'
import StatusBadge from '../components/StatusBadge'
import ToolConnectionsPanel from '../components/ToolConnectionsPanel'

export default function MCPlex() {
  const navigate = useNavigate()
  const [tab, setTab] = useState<'catalog' | 'instances' | 'connections'>('instances')
  const [search, setSearch] = useState('')
  const catalog = useQuery({ queryKey: ['catalog', 'mcplex'], queryFn: () => getCatalog('mcplex') })
  const instances = useQuery({ queryKey: ['instances', 'mcplex'], queryFn: () => listInstances('mcplex') })

  const activeQuery = tab === 'catalog' ? catalog : instances

  const filteredInstances = instances.data?.filter(i =>
    i.display_name?.toLowerCase().includes(search.toLowerCase()) ||
    i.id?.toLowerCase().includes(search.toLowerCase()) ||
    i.template_id?.toLowerCase().includes(search.toLowerCase())
  ) || []

  return (
    <div>
      <h1 className="text-2xl font-bold mb-2">MCPlex</h1>
      <p className="text-gray-600 dark:text-gray-400 mb-6">Agent &harr; Tool (MCP servers)</p>

      <div className="flex flex-wrap gap-2 mb-6">
        <button
          onClick={() => setTab('instances')}
          className={`px-4 py-2 rounded text-sm ${tab === 'instances' ? 'bg-brand-600 text-brand-contrast' : 'bg-gray-100 dark:bg-gray-800'}`}
        >
          {tabLabel('Instances', instances.data?.length)}
        </button>
        <button
          onClick={() => setTab('catalog')}
          className={`px-4 py-2 rounded text-sm ${tab === 'catalog' ? 'bg-brand-600 text-brand-contrast' : 'bg-gray-100 dark:bg-gray-800'}`}
        >
          {tabLabel('Catalog', catalog.data?.total)}
        </button>
        <button
          onClick={() => setTab('connections')}
          className={`px-4 py-2 rounded text-sm ${tab === 'connections' ? 'bg-brand-600 text-brand-contrast' : 'bg-gray-100 dark:bg-gray-800'}`}
        >
          Tool connections
        </button>
      </div>

      {tab === 'connections' && <ToolConnectionsPanel />}
      {tab !== 'connections' && activeQuery.isLoading && <p role="status">Loading module data…</p>}
      {tab !== 'connections' && activeQuery.error && <p role="alert" className="text-status-denied-fg mb-4">Could not load module data: {activeQuery.error.message}</p>}

      {tab === 'instances' && !activeQuery.isLoading && !activeQuery.error && (
        <div>
          <div className="mb-4">
            <input
              type="text"
              aria-label="Search instances" placeholder="Search instances..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
            />
          </div>
          <div className="bg-white rounded-lg shadow dark:bg-gray-900">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 dark:bg-gray-900">
                <tr>
                  <th className="text-left px-4 py-3">Name</th>
                  <th className="text-left px-4 py-3">Template</th>
                  <th className="text-left px-4 py-3">Status</th>
                  <th className="text-left px-4 py-3">Scopes</th>
                </tr>
              </thead>
              <tbody>
                {filteredInstances.map((inst) => (
                  <tr key={inst.id} className="border-t">
                    <td className="px-4 py-3 font-medium"><Link to={`/instances/${encodeURIComponent(inst.id)}`} className="hover:underline">{inst.display_name || inst.id}</Link></td>
                    <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{inst.template_id}</td>
                    <td className="px-4 py-3"><StatusBadge status={inst.status} /></td>
                    <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{countOf(inst.scopes, 'tool')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {tab === 'catalog' && !activeQuery.isLoading && !activeQuery.error && (
        <div className="grid grid-cols-2 gap-4">
          {catalog.data?.templates?.map((t) => (
            <div key={t.id} className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
              <div className="flex items-center justify-between mb-2">
                <h3 className="font-semibold">{t.name}</h3>
                {t.verified && <span className="text-xs text-status-ok-fg">Verified</span>}
              </div>
              <p className="text-sm text-gray-600 dark:text-gray-400 mb-3">{t.description}</p>
              <button
                onClick={() => navigate(`/deploy?plane=mcplex&template=${t.id}`)}
                className="px-3 py-1.5 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700"
              >
                Deploy
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
