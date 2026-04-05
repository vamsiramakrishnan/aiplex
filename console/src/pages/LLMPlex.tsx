import { useQuery } from '@tanstack/react-query'
import { getCatalog, listInstances } from '../api/client'
import StatusBadge from '../components/StatusBadge'

export default function LLMPlex() {
  const catalog = useQuery({ queryKey: ['catalog', 'llmplex'], queryFn: () => getCatalog('llmplex') })
  const instances = useQuery({ queryKey: ['instances', 'llmplex'], queryFn: () => listInstances('llmplex') })

  return (
    <div>
      <h2 className="text-2xl font-bold mb-2">LLMPlex</h2>
      <p className="text-gray-500 mb-6">Agent &harr; Model (LLM providers)</p>

      {/* Available Providers */}
      <h3 className="font-semibold text-lg mb-3">Available Providers</h3>
      <div className="grid grid-cols-3 gap-4 mb-8">
        {catalog.data?.templates?.map((t) => (
          <div key={t.id} className="bg-white rounded-lg shadow p-4">
            <div className="flex items-center justify-between mb-2">
              <h4 className="font-semibold">{t.name}</h4>
              <span className="text-xs text-gray-500 capitalize">{t.provider}</span>
            </div>
            <p className="text-sm text-gray-500 mb-2">{t.description}</p>
            {t.pricing && (
              <p className="text-xs text-gray-400">
                ${t.pricing.input}/M input &middot; ${t.pricing.output}/M output
              </p>
            )}
            {t.capabilities && (
              <div className="flex gap-1 mt-2 flex-wrap">
                {t.capabilities.map((cap) => (
                  <span key={cap} className="px-1.5 py-0.5 bg-blue-50 text-blue-700 text-xs rounded">
                    {cap}
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>

      {/* Active Instances */}
      <h3 className="font-semibold text-lg mb-3">Active Model Routes</h3>
      {instances.data && instances.data.length > 0 ? (
        <div className="bg-white rounded-lg shadow">
          <table className="w-full text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="text-left px-4 py-3">Model</th>
                <th className="text-left px-4 py-3">Provider</th>
                <th className="text-left px-4 py-3">Status</th>
              </tr>
            </thead>
            <tbody>
              {instances.data.map((inst) => (
                <tr key={inst.id} className="border-t">
                  <td className="px-4 py-3 font-medium">{inst.display_name || inst.id}</td>
                  <td className="px-4 py-3 text-gray-500">{inst.template_id}</td>
                  <td className="px-4 py-3"><StatusBadge status={inst.status} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-gray-400 text-sm">No model routes configured yet.</p>
      )}
    </div>
  )
}
