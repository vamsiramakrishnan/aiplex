import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCatalog, listInstances } from '../api/client'
import StatusBadge from '../components/StatusBadge'

export default function A2APlex() {
  const [tab, setTab] = useState<'catalog' | 'instances'>('instances')
  const catalog = useQuery({ queryKey: ['catalog', 'a2aplex'], queryFn: () => getCatalog('a2aplex') })
  const instances = useQuery({ queryKey: ['instances', 'a2aplex'], queryFn: () => listInstances('a2aplex') })

  return (
    <div>
      <h2 className="text-2xl font-bold mb-2">A2APlex</h2>
      <p className="text-gray-500 mb-6">Agent &harr; Agent (A2A delegation)</p>

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
        <div className="bg-white rounded-lg shadow">
          <table className="w-full text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="text-left px-4 py-3">Name</th>
                <th className="text-left px-4 py-3">Template</th>
                <th className="text-left px-4 py-3">Status</th>
                <th className="text-left px-4 py-3">Task Types</th>
              </tr>
            </thead>
            <tbody>
              {instances.data?.map((inst) => (
                <tr key={inst.id} className="border-t">
                  <td className="px-4 py-3 font-medium">{inst.display_name || inst.id}</td>
                  <td className="px-4 py-3 text-gray-500">{inst.template_id}</td>
                  <td className="px-4 py-3"><StatusBadge status={inst.status} /></td>
                  <td className="px-4 py-3 text-gray-500">{inst.scopes?.length ?? 0} tasks</td>
                </tr>
              ))}
            </tbody>
          </table>
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
              <button className="px-3 py-1.5 bg-brand-600 text-white text-sm rounded hover:bg-brand-700">
                Deploy
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
