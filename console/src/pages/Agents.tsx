import { useQuery } from '@tanstack/react-query'
import { listAgents } from '../api/client'
import StatusBadge from '../components/StatusBadge'

export default function Agents() {
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents })

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-2xl font-bold">Agents</h2>
          <p className="text-gray-500">Registered OAuth clients across all planes</p>
        </div>
        <button className="px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700">
          Register Agent
        </button>
      </div>

      <div className="bg-white rounded-lg shadow">
        <table className="w-full text-sm">
          <thead className="bg-gray-50">
            <tr>
              <th className="text-left px-4 py-3">Name</th>
              <th className="text-left px-4 py-3">Client ID</th>
              <th className="text-left px-4 py-3">Auth Method</th>
              <th className="text-left px-4 py-3">Scopes</th>
              <th className="text-left px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody>
            {agents.data?.map((agent) => (
              <tr key={agent.client_id} className="border-t">
                <td className="px-4 py-3 font-medium">{agent.display_name}</td>
                <td className="px-4 py-3 text-gray-500 font-mono text-xs">{agent.client_id}</td>
                <td className="px-4 py-3 text-gray-500">{agent.auth_method}</td>
                <td className="px-4 py-3 text-gray-500">{agent.allowed_scopes?.length ?? 0}</td>
                <td className="px-4 py-3"><StatusBadge status={agent.status} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
