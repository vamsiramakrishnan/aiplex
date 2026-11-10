import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSSE } from '../hooks/useSSE'
import { authenticatedRequest } from '../api/client'
import { presetSince, sumCosts, sumCounts, tokenHeadline, formatTotal, formatCount, usageSummary } from '../api/usage'

interface DashboardStats {
  total_instances: number
  running_instances: number
  registered_agents: number
  active_planes: number
  mcplex_instances: number
  a2aplex_instances: number
  llmplex_instances: number
  skillsplex_instances: number
  /** null = unknown or unavailable, never zero (see usage_source). */
  daily_cost_usd: number | null
  daily_tokens: number | null
  daily_requests: number | null
  tool_calls_24h: number | null
  usage_source?: string
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

const getStats = () => authenticatedRequest<DashboardStats>('/api/v1/dashboard/stats')
const getDenials = () => authenticatedRequest<PolicyDenial[]>('/api/v1/dashboard/denials')

const EMPTY_STATS: DashboardStats = { total_instances: 0, running_instances: 0, registered_agents: 0, active_planes: 0, mcplex_instances: 0, a2aplex_instances: 0, llmplex_instances: 0, skillsplex_instances: 0, daily_cost_usd: null, daily_tokens: null, daily_requests: null, tool_calls_24h: null, a2a_delegations_24h: 0, policy_denials_24h: 0 }

export default function Dashboard() {
  // Real-time SSE stats (5s updates)
  const sseStats = useSSE(true)

  // Fallback polling query (1 minute interval in case SSE disconnects)
  const statsQuery = useQuery({ queryKey: ['dashboard-stats'], queryFn: getStats, refetchInterval: 60000 })
  const denials = useQuery({ queryKey: ['dashboard-denials'], queryFn: getDenials })
  // Requests, tokens and cost come from the usage ledger (what `aiplex usage summary` shows),
  // not from the deployed-instance counters the dashboard endpoints report.
  const since = useMemo(() => presetSince('24h'), [])
  const ledger = useQuery({ queryKey: ['dashboard-ledger', since], queryFn: () => usageSummary({ since }, []), refetchInterval: 60000 })

  // The live stream carries instance counts only; every other figure keeps the polled value.
  const s: DashboardStats = { ...EMPTY_STATS, ...statsQuery.data, ...(sseStats ? {
    total_instances: sseStats.total_instances,
    running_instances: sseStats.running,
    registered_agents: sseStats.agents,
    active_planes: [sseStats.mcplex, sseStats.a2aplex, sseStats.llmplex, sseStats.skillsplex ?? 0].filter(n => n > 0).length,
    mcplex_instances: sseStats.mcplex,
    a2aplex_instances: sseStats.a2aplex,
    llmplex_instances: sseStats.llmplex,
    skillsplex_instances: sseStats.skillsplex ?? 0,
    a2a_delegations_24h: sseStats.delegations,
  } : {}) }
  const groups = ledger.data?.groups ?? []
  const requests = sumCounts(groups.map(g => g.requests))
  const denied = sumCounts(groups.map(g => g.denied))
  const input = tokenHeadline(groups, 'input_tokens')
  const output = tokenHeadline(groups, 'output_tokens')
  const costs = sumCosts(groups)
  const tokenTotal = input.total + output.total

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Dashboard</h1>

      {/* Top-level stats */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        <StatCard label="Total Instances" value={s?.total_instances ?? 0} />
        <StatCard label="Running" value={s?.running_instances ?? 0} color="green" />
        <StatCard label="Registered Agents" value={s?.registered_agents ?? 0} />
        <StatCard label="Active Planes" value={s?.active_planes ?? 0} />
      </div>

      {/* Per-plane + costs */}
      <div className="grid md:grid-cols-3 gap-6 mb-8">
        <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
          <h3 className="font-semibold text-lg mb-3">Per Plane</h3>
          <div className="space-y-2">
            <PlaneRow label="MCPlex" count={s?.mcplex_instances ?? 0} color="mcplex" />
            <PlaneRow label="A2APlex" count={s?.a2aplex_instances ?? 0} color="a2aplex" />
            <PlaneRow label="LLMPlex" count={s?.llmplex_instances ?? 0} color="llmplex" />
            <PlaneRow label="SkillsPlex" count={s?.skillsplex_instances ?? 0} color="skillsplex" />
          </div>
        </div>

        <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
          <h3 className="font-semibold text-lg mb-3">LLM Costs (24h)</h3>
          <div className="space-y-3">
            <div>
              <p className="text-sm text-gray-600 dark:text-gray-400">Total Spend</p>
              {ledger.isError ? <p className="text-sm text-gray-600 dark:text-gray-400">unknown (usage ledger unavailable)</p>
                : costs.length === 0 ? <p className="text-2xl font-bold">{groups.length ? 'unknown' : '0'}</p>
                : <ul>{costs.map(c => <li key={`${c.currency}-${c.basis}`} className="text-2xl font-bold">{formatTotal(c)} <span className="text-xs font-normal text-gray-600 dark:text-gray-400">{c.basis}</span></li>)}</ul>}
            </div>
            <div className="flex gap-4">
              <div>
                <p className="text-xs text-gray-600 dark:text-gray-400">Tokens</p>
                <p className="font-semibold">{input.reported || output.reported ? formatCount(tokenTotal.toString()) : groups.length ? 'unknown' : '0'}</p>
              </div>
              <div>
                <p className="text-xs text-gray-600 dark:text-gray-400">Requests</p>
                <p className="font-semibold">{formatCount(requests.toString())}</p>
              </div>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
          <h3 className="font-semibold text-lg mb-3">Activity (24h)</h3>
          <div className="space-y-2">
            <div className="flex justify-between text-sm">
              <span className="text-gray-600 dark:text-gray-400">A2A Delegations</span>
              <span className="font-semibold">{s?.a2a_delegations_24h ?? 0}</span>
            </div>
            <div className="flex justify-between text-sm">
              <span className="text-gray-600 dark:text-gray-400">Denied requests</span>
              <span className={`font-semibold ${(denied > 0n ? true : (s?.policy_denials_24h ?? 0) > 0) ? 'text-status-denied-fg' : ''}`}>
                {formatCount((denied > 0n ? denied : BigInt(s?.policy_denials_24h ?? 0)).toString())}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* Policy denials */}
      <h3 className="font-semibold text-lg mb-3">Recent Policy Denials</h3>
      {denials.data && denials.data.length > 0 ? (
        <div className="bg-white rounded-lg shadow dark:bg-gray-900">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 dark:bg-gray-900">
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
                  <td className="px-4 py-3 text-gray-600 dark:text-gray-400 text-xs">
                    {d.timestamp ? new Date(d.timestamp).toLocaleTimeString() : '-'}
                  </td>
                  <td className="px-4 py-3">
                    <span className="px-1.5 py-0.5 bg-gray-100 text-gray-700 text-xs rounded dark:bg-gray-800 dark:text-gray-300">{d.plane}</span>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs">{d.agent_id}</td>
                  <td className="px-4 py-3 font-mono text-xs">{d.action}</td>
                  <td className="px-4 py-3">
                    <span className="px-1.5 py-0.5 bg-status-denied-bg text-status-denied-fg text-xs rounded">{d.reason}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-gray-600 dark:text-gray-400 text-sm">No policy denials recorded.</p>
      )}
    </div>
  )
}

function StatCard({ label, value, color }: { label: string; value: number; color?: string }) {
  return (
    <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
      <p className="text-sm text-gray-600 dark:text-gray-400">{label}</p>
      <p className={`text-3xl font-bold mt-1 ${color === 'green' ? 'text-status-ok-fg' : ''}`}>{value}</p>
    </div>
  )
}

function PlaneRow({ label, count, color }: { label: string; count: number; color: string }) {
  const colors: Record<string, string> = {
    mcplex: 'bg-module-mcplex',
    a2aplex: 'bg-module-a2aplex',
    llmplex: 'bg-module-llmplex',
    skillsplex: 'bg-module-skillsplex',
  }
  return (
    <div className="flex items-center gap-3">
      <span aria-hidden="true" className={`w-3 h-3 rounded-full ${colors[color]}`} />
      <span className="text-sm flex-1">{label}</span>
      <span className="font-semibold">{count}</span>
    </div>
  )
}
