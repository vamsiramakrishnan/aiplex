import { useQuery } from '@tanstack/react-query'
import { listInstances, listAgents } from '../api/client'
import StatusBadge from '../components/StatusBadge'

export default function Dashboard() {
  const instances = useQuery({ queryKey: ['instances'], queryFn: () => listInstances() })
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents })

  const byPlane = (plane: string) =>
    instances.data?.filter((i) => i.plane === plane) ?? []

  const running = instances.data?.filter((i) => i.status === 'running').length ?? 0

  return (
    <div>
      <h2 className="text-2xl font-bold mb-6">Dashboard</h2>

      {/* Stats */}
      <div className="grid grid-cols-4 gap-4 mb-8">
        <StatCard label="Total Instances" value={instances.data?.length ?? 0} />
        <StatCard label="Running" value={running} />
        <StatCard label="Registered Agents" value={agents.data?.length ?? 0} />
        <StatCard label="Planes Active" value={
          new Set(instances.data?.map((i) => i.plane)).size
        } />
      </div>

      {/* Per-plane breakdown */}
      <div className="grid grid-cols-3 gap-6">
        {(['mcplex', 'a2aplex', 'llmplex'] as const).map((plane) => (
          <div key={plane} className="bg-white rounded-lg shadow p-4">
            <h3 className="font-semibold text-lg mb-3 capitalize">{plane}</h3>
            {byPlane(plane).length === 0 ? (
              <p className="text-gray-400 text-sm">No instances</p>
            ) : (
              <ul className="space-y-2">
                {byPlane(plane).map((inst) => (
                  <li key={inst.id} className="flex items-center justify-between text-sm">
                    <span>{inst.display_name || inst.id}</span>
                    <StatusBadge status={inst.status} />
                  </li>
                ))}
              </ul>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function StatCard({ label, value }: { label: string; value: number }) {
  return (
    <div className="bg-white rounded-lg shadow p-4">
      <p className="text-sm text-gray-500">{label}</p>
      <p className="text-3xl font-bold mt-1">{value}</p>
    </div>
  )
}
