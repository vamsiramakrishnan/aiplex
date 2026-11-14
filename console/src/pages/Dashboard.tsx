import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSSE } from '../hooks/useSSE'
import { authenticatedRequest } from '../api/client'
import { presetSince, sumCosts, sumCounts, tokenHeadline, formatTotal, formatCount, usageSummary, UNKNOWN } from '../api/usage'

/** A field the server did not send is unknown, never zero. */
interface DashboardStats {
  total_instances?: number | null
  running_instances?: number | null
  registered_agents?: number | null
  active_planes?: number | null
  mcplex_instances?: number | null
  a2aplex_instances?: number | null
  llmplex_instances?: number | null
  skillsplex_instances?: number | null
  /** null = unknown or unavailable, never zero (see usage_source). */
  daily_cost_usd?: number | null
  daily_tokens?: number | null
  daily_requests?: number | null
  tool_calls_24h?: number | null
  usage_source?: string
  a2a_delegations_24h?: number | null
  policy_denials_24h?: number | null
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
  const s: DashboardStats = { ...statsQuery.data, ...(sseStats ? {
    total_instances: sseStats.total_instances,
    running_instances: sseStats.running,
    registered_agents: sseStats.agents,
    ...(sseStats.skillsplex != null ? { active_planes: [sseStats.mcplex, sseStats.a2aplex, sseStats.llmplex, sseStats.skillsplex].filter(n => n > 0).length, skillsplex_instances: sseStats.skillsplex } : {}),
    mcplex_instances: sseStats.mcplex,
    a2aplex_instances: sseStats.a2aplex,
    llmplex_instances: sseStats.llmplex,
    a2a_delegations_24h: sseStats.delegations,
  } : {}) }
  const groups = ledger.data?.groups ?? []
  const requests = sumCounts(groups.map(g => g.requests))
  const denied = sumCounts(groups.map(g => g.denied))
  const input = tokenHeadline(groups, 'input_tokens')
  const output = tokenHeadline(groups, 'output_tokens')
  const costs = sumCosts(groups)
  const tokenTotal = input.total + output.total
  // A failed refresh keeps the last good data; it is shown, labelled with when it was fetched (see FailedRefresh).
  const ledgerKnown = !!ledger.data
  const statsDenied = s.policy_denials_24h

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Dashboard</h1>

      {statsQuery.isError && <FailedRefresh what="dashboard stats" error={statsQuery.error as Error} hasData={!!statsQuery.data} updatedAt={statsQuery.dataUpdatedAt} onRetry={() => void statsQuery.refetch()} />}
      {ledger.isError && <FailedRefresh what="usage ledger figures" error={ledger.error as Error} hasData={!!ledger.data} updatedAt={ledger.dataUpdatedAt} onRetry={() => void ledger.refetch()} />}

      {/* Top-level stats */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4 mb-8">
        <StatCard label="Total Instances" value={s.total_instances} />
        <StatCard label="Running" value={s.running_instances} color="green" />
        <StatCard label="Registered Agents" value={s.registered_agents} />
        <StatCard label="Active Planes" value={s.active_planes} />
      </div>

      {/* Per-plane + costs */}
      <div className="grid md:grid-cols-3 gap-6 mb-8">
        <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
          <h3 className="font-semibold text-lg mb-3">Per Plane</h3>
          <div className="space-y-2">
            <PlaneRow label="MCPlex" count={s.mcplex_instances} color="mcplex" />
            <PlaneRow label="A2APlex" count={s.a2aplex_instances} color="a2aplex" />
            <PlaneRow label="LLMPlex" count={s.llmplex_instances} color="llmplex" />
            <PlaneRow label="SkillsPlex" count={s.skillsplex_instances} color="skillsplex" />
          </div>
        </div>

        <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
          <h3 className="font-semibold text-lg mb-3">LLM Costs (24h)</h3>
          <div className="space-y-3">
            <div>
              <p className="text-sm text-gray-600 dark:text-gray-400">Total Spend</p>
              {!ledger.data ? <p className="text-2xl font-bold">{UNKNOWN}{ledger.isError && <span className="text-sm font-normal text-gray-600 dark:text-gray-400"> (usage ledger unavailable)</span>}</p>
                : costs.length === 0 ? <p className="text-2xl font-bold">{groups.length ? UNKNOWN : '0'}</p>
                : <ul>{costs.map(c => <li key={`${c.currency}-${c.basis}`} className="text-2xl font-bold">{formatTotal(c)} <span className="text-xs font-normal text-gray-600 dark:text-gray-400">{c.basis}</span></li>)}</ul>}
            </div>
            <div className="flex gap-4">
              <div>
                <p className="text-xs text-gray-600 dark:text-gray-400">Tokens</p>
                <p className="font-semibold">{!ledgerKnown ? UNKNOWN : input.reported || output.reported ? formatCount(tokenTotal.toString()) : groups.length ? UNKNOWN : '0'}</p>
              </div>
              <div>
                <p className="text-xs text-gray-600 dark:text-gray-400">Requests</p>
                <p className="font-semibold">{ledgerKnown ? formatCount(requests.toString()) : UNKNOWN}</p>
              </div>
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
          <h3 className="font-semibold text-lg mb-3">Activity (24h)</h3>
          <div className="space-y-2">
            <div className="flex justify-between text-sm">
              <span className="text-gray-600 dark:text-gray-400">A2A Delegations</span>
              <span className="font-semibold">{formatCount(s.a2a_delegations_24h)}</span>
            </div>
            <div className="flex justify-between text-sm">
              <span className="text-gray-600 dark:text-gray-400">Denied requests</span>
              <span className={`font-semibold ${(denied > 0n || (statsDenied ?? 0) > 0) ? 'text-status-denied-fg' : ''}`}>
                {denied > 0n ? formatCount(denied.toString()) : statsDenied != null ? formatCount(statsDenied) : ledgerKnown ? formatCount(denied.toString()) : UNKNOWN}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* Policy denials */}
      <h3 className="font-semibold text-lg mb-3">Recent Policy Denials</h3>
      {denials.isError && <FailedRefresh what="policy denials" error={denials.error as Error} hasData={!!denials.data} updatedAt={denials.dataUpdatedAt} onRetry={() => void denials.refetch()} />}
      {denials.isError && !denials.data ? null : denials.isPending ? (
        <p role="status" className="text-gray-600 dark:text-gray-400 text-sm">Loading policy denials…</p>
      ) : denials.data && denials.data.length > 0 ? (
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
                    {d.timestamp ? new Date(d.timestamp).toLocaleTimeString() : UNKNOWN}
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

/** Says what is true after a failed fetch: nothing was ever loaded (figures are unknown), or the figures shown are from the last good refresh. */
function FailedRefresh({ what, error, hasData, updatedAt, onRetry }: { what: string; error: Error; hasData: boolean; updatedAt: number; onRetry: () => void }) {
  return (
    <div role="alert" className="mb-4 flex items-center gap-3 text-sm text-status-denied-fg">
      <span>
        {hasData
          ? `The latest refresh of ${what} failed (${error.message}). The figures shown are from the last successful refresh at ${new Date(updatedAt).toLocaleTimeString()}.`
          : `Could not load ${what}: ${error.message}. Figures that depend on them are shown as unknown.`}
      </span>
      <button type="button" onClick={onRetry} className="px-3 py-1 text-sm rounded bg-gray-100 text-gray-900 dark:bg-gray-800 dark:text-gray-100">Retry</button>
    </div>
  )
}

function StatCard({ label, value, color }: { label: string; value: number | null | undefined; color?: string }) {
  return (
    <div className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
      <p className="text-sm text-gray-600 dark:text-gray-400">{label}</p>
      <p className={`text-3xl font-bold mt-1 ${color === 'green' && value != null ? 'text-status-ok-fg' : ''}`}>{value ?? UNKNOWN}</p>
    </div>
  )
}

function PlaneRow({ label, count, color }: { label: string; count: number | null | undefined; color: string }) {
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
      <span className="font-semibold">{count ?? UNKNOWN}</span>
    </div>
  )
}
