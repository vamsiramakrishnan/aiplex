import { useQuery } from '@tanstack/react-query'

interface DashboardStats {
  total_instances: number
  running_instances: number
  registered_agents: number
  active_planes: number
  mcplex_instances: number
  a2aplex_instances: number
  llmplex_instances: number
  daily_cost_usd: number
  daily_tokens: number
  daily_requests: number
  tool_calls_24h: number
  a2a_delegations_24h: number
  policy_denials_24h: number
}

interface PolicyDenial {
  id: string
  timestamp: string
  plane: string
  agent_id: string
  action: string
  scope: string
  reason: string
}

const getStats = () => fetch('/api/v1/dashboard/stats').then(r => r.json()) as Promise<DashboardStats>
const getDenials = () => fetch('/api/v1/dashboard/denials').then(r => r.json()) as Promise<PolicyDenial[]>

export default function Dashboard() {
  const stats = useQuery({ queryKey: ['dashboard-stats'], queryFn: getStats, refetchInterval: 30000 })
  const denials = useQuery({ queryKey: ['dashboard-denials'], queryFn: getDenials })

  const s = stats.data

  return (
    <div>
      <h2 className="text-2xl font-bold mb-6">Dashboard</h2>

      {/* Top-level stats */}
      <div className="grid grid-cols-4 gap-4 mb-8">
        <StatCard label="Total Instances" value={s?.total_instances ?? 0} />
        <StatCard label="Running" value={s?.running_instances ?? 0} color="green" />
        <StatCard label="Registered Agents" value={s?.registered_agents ?? 0} />
        <StatCard label="Active Planes" value={s?.active_planes ?? 0} />
      </div>

      {/* Per-plane + costs */}
      <div className="grid grid-cols-3 gap-6 mb-8">
        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold text-lg mb-3">Per Plane</h3>
          <div className="space-y-2">
            <PlaneRow label="MCPlex" count={s?.mcplex_instances ?? 0} color="blue" />
            <PlaneRow label="A2APlex" count={s?.a2aplex_instances ?? 0} color="purple" />
            <PlaneRow label="LLMPlex" count={s?.llmplex_instances ?? 0} color="amber" />
          </div>
        </div>

        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold text-lg mb-3">LLM Costs (24h)</h3>
          <div className="space-y-3">
            <div>
              <p className="text-sm text-gray-500">Total Spend</p>
              <p className="text-2xl font-bold">${(s?.daily_cost_usd ?? 0).toFixed(2)}</p>
            </div>
            <div className="flex gap-4">
              <div>
                <p className="text-xs text-gray-400">Tokens</p>
                <p className="font-semibold">{((s?.daily_tokens ?? 0) / 1000).toFixed(1)}K</p>
              </div>
              <div>
                <p className="text-xs text-gray-400">Requests</p>
                <p className="font-semibold">{s?.daily_requests ?? 0}</p>
              </div>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg shadow p-4">
          <h3 className="font-semibold text-lg mb-3">Activity (24h)</h3>
          <div className="space-y-2">
            <div className="flex justify-between text-sm">
              <span className="text-gray-500">A2A Delegations</span>
              <span className="font-semibold">{s?.a2a_delegations_24h ?? 0}</span>
            </div>
            <div className="flex justify-between text-sm">
              <span className="text-gray-500">Policy Denials</span>
              <span className={`font-semibold ${(s?.policy_denials_24h ?? 0) > 0 ? 'text-red-600' : ''}`}>
                {s?.policy_denials_24h ?? 0}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* Policy denials */}
      <h3 className="font-semibold text-lg mb-3">Recent Policy Denials</h3>
      {denials.data && denials.data.length > 0 ? (
        <div className="bg-white rounded-lg shadow">
          <table className="w-full text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="text-left px-4 py-3">Time</th>
                <th className="text-left px-4 py-3">Plane</th>
                <th className="text-left px-4 py-3">Agent</th>
                <th className="text-left px-4 py-3">Action</th>
                <th className="text-left px-4 py-3">Reason</th>
              </tr>
            </thead>
            <tbody>
              {denials.data.map((d) => (
                <tr key={d.id} className="border-t">
                  <td className="px-4 py-3 text-gray-500 text-xs">
                    {d.timestamp ? new Date(d.timestamp).toLocaleTimeString() : '-'}
                  </td>
                  <td className="px-4 py-3">
                    <span className="px-1.5 py-0.5 bg-gray-100 text-gray-700 text-xs rounded">{d.plane}</span>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs">{d.agent_id}</td>
                  <td className="px-4 py-3 font-mono text-xs">{d.action}</td>
                  <td className="px-4 py-3">
                    <span className="px-1.5 py-0.5 bg-red-50 text-red-700 text-xs rounded">{d.reason}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-gray-400 text-sm">No policy denials recorded.</p>
      )}
    </div>
  )
}

function StatCard({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <div className="bg-white rounded-lg shadow p-4">
      <p className="text-sm text-gray-500">{label}</p>
      <p className={`text-3xl font-bold mt-1 ${color === 'green' ? 'text-green-600' : ''}`}>{value}</p>
    </div>
  )
}

function PlaneRow({ label, count, color }: { label: string; count: number; color: string }) {
  const colors: Record<string, string> = {
    blue: 'bg-blue-500',
    purple: 'bg-purple-500',
    amber: 'bg-amber-500',
  }
  return (
    <div className="flex items-center gap-3">
      <span className={`w-3 h-3 rounded-full ${colors[color]}`} />
      <span className="text-sm flex-1">{label}</span>
      <span className="font-semibold">{count}</span>
    </div>
  )
}
