import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { getCatalog, listInstances } from '../api/client'
import StatusBadge from '../components/StatusBadge'

export default function MCPlex() {
  const navigate = useNavigate()
  const [tab, setTab] = useState<'catalog' | 'instances'>('instances')
  const [search, setSearch] = useState('')
  const catalog = useQuery({ queryKey: ['catalog', 'tool'], queryFn: () => getCatalog('tool') })
  const instances = useQuery({ queryKey: ['instances', 'tool'], queryFn: () => listInstances('tool') })

  const filteredInstances = instances.data?.filter(i =>
    i.display_name?.toLowerCase().includes(search.toLowerCase()) ||
    i.id?.toLowerCase().includes(search.toLowerCase()) ||
    i.template_id?.toLowerCase().includes(search.toLowerCase())
  ) || []

  return (
    <div>
      <h2 className="text-2xl font-bold mb-2">MCPlex</h2>
      <p className="text-gray-500 mb-6">Agent &harr; Tool (MCP servers)</p>

      <div className="flex gap-2 mb-6">
        <button
          onClick={() => setTab('instances')}
          className={`px-4 py-2 rounded text-sm ${tab === 'instances' ? 'bg-brand-600 text-white' : 'bg-gray-100'}`}
        >
          Instances ({instances.data?.length ?? 0})
        </button>
        <button
          onClick={() => setTab('catalog')}
          className={`px-4 py-2 rounded text-sm ${tab === 'catalog' ? 'bg-brand-600 text-white' : 'bg-gray-100'}`}
        >
          Catalog ({catalog.data?.total ?? 0})
        </button>
      </div>

      {tab === 'instances' && (
        <div>
          <div className="mb-4">
            <input
              type="text"
              placeholder="Search instances..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
            />
          </div>
          <div className="bg-white rounded-lg shadow">
            <table className="w-full text-sm">
              <thead className="bg-gray-50">
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
                    <td className="px-4 py-3 font-medium">{inst.display_name || inst.id}</td>
                    <td className="px-4 py-3 text-gray-500">{inst.template_id}</td>
                    <td className="px-4 py-3"><StatusBadge status={inst.status} /></td>
                    <td className="px-4 py-3 text-gray-500">{inst.capabilities?.length ?? 0} tools</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {tab === 'catalog' && (
        <div className="grid grid-cols-2 gap-4">
          {catalog.data?.templates?.map((t) => (
            <div key={t.id} className="bg-white rounded-lg shadow p-4">
              <div className="flex items-center justify-between mb-2">
                <h3 className="font-semibold">{t.name}</h3>
                {t.verified && <span className="text-xs text-green-600">Verified</span>}
              </div>
              <p className="text-sm text-gray-500 mb-3">{t.description}</p>
              <button
                onClick={() => navigate(`/deploy?kind=tool&template=${t.id}`)}
                className="px-3 py-1.5 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
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
