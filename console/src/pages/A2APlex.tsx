import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCatalog } from '../api/client'
import StatusBadge from '../components/StatusBadge'

interface Delegation {
  id: string
  caller_agent_id: string
  callee_agent_id: string
  task_type: string
  status: string
  user_id: string
  started_at: string
  completed_at?: string
  duration_ms?: number
}

interface AgentCardSummary {
  instance_id: string
  name: string
  url: string
  task_types: string[]
  status: string
}

const getAgentCards = () => fetch('/api/v1/a2a/agents').then(r => r.json()) as Promise<AgentCardSummary[]>
const getDelegations = () => fetch('/api/v1/a2a/delegations').then(r => r.json()) as Promise<Delegation[]>

export default function A2APlex() {
  const [tab, setTab] = useState<'agents' | 'catalog' | 'delegations'>('agents')
  const catalog = useQuery({ queryKey: ['catalog', 'a2aplex'], queryFn: () => getCatalog('a2aplex') })
  const agentCards = useQuery({ queryKey: ['a2a-agents'], queryFn: getAgentCards })
  const delegations = useQuery({ queryKey: ['a2a-delegations'], queryFn: getDelegations })

  return (
    <div>
      <h2 className="text-2xl font-bold mb-2">A2APlex</h2>
      <p className="text-gray-500 mb-6">Agent &harr; Agent (A2A delegation)</p>

      <div className="flex gap-2 mb-6">
        {(['agents', 'catalog', 'delegations'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 rounded text-sm capitalize ${tab === t ? 'bg-brand-600 text-white' : 'bg-gray-100'}`}
          >
            {t === 'agents' ? `Agents (${agentCards.data?.length ?? 0})` :
             t === 'catalog' ? `Catalog (${catalog.data?.total ?? 0})` :
             `Delegations (${delegations.data?.length ?? 0})`}
          </button>
        ))}
      </div>

      {tab === 'agents' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Running A2A Agents</h3>
          {agentCards.data && agentCards.data.length > 0 ? (
            <div className="grid grid-cols-2 gap-4">
              {agentCards.data.map((card) => (
                <div key={card.instance_id} className="bg-white rounded-lg shadow p-4">
                  <div className="flex items-center justify-between mb-2">
                    <h4 className="font-semibold">{card.name || card.instance_id}</h4>
                    <StatusBadge status={card.status} />
                  </div>
                  <p className="text-xs text-gray-400 mb-2 font-mono">{card.url}</p>
                  <div className="flex gap-1 flex-wrap">
                    {card.task_types?.map((scope) => {
                      const label = scope.replace('a2a:task:', '')
                      return (
                        <span key={scope} className="px-1.5 py-0.5 bg-purple-50 text-purple-700 text-xs rounded">
                          {label}
                        </span>
                      )
                    })}
                  </div>
                  <a
                    href={`${card.url}/.well-known/agent.json`}
                    className="text-xs text-brand-600 hover:underline mt-2 block"
                  >
                    View Agent Card
                  </a>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-gray-400 text-sm">No A2A agents deployed.</p>
          )}
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

      {tab === 'delegations' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Recent Delegations</h3>
          {delegations.data && delegations.data.length > 0 ? (
            <div className="bg-white rounded-lg shadow">
              <table className="w-full text-sm">
                <thead className="bg-gray-50">
                  <tr>
                    <th className="text-left px-4 py-3">Caller</th>
                    <th className="text-left px-4 py-3">Callee</th>
                    <th className="text-left px-4 py-3">Task</th>
                    <th className="text-left px-4 py-3">Status</th>
                    <th className="text-left px-4 py-3">Duration</th>
                    <th className="text-left px-4 py-3">User</th>
                  </tr>
                </thead>
                <tbody>
                  {delegations.data.map((d) => (
                    <tr key={d.id} className="border-t">
                      <td className="px-4 py-3 font-mono text-xs">{d.caller_agent_id}</td>
                      <td className="px-4 py-3 font-mono text-xs">{d.callee_agent_id}</td>
                      <td className="px-4 py-3">
                        <span className="px-1.5 py-0.5 bg-purple-50 text-purple-700 text-xs rounded">
                          {d.task_type}
                        </span>
                      </td>
                      <td className="px-4 py-3"><StatusBadge status={d.status} /></td>
                      <td className="px-4 py-3 text-gray-500">
                        {d.duration_ms ? `${d.duration_ms}ms` : '-'}
                      </td>
                      <td className="px-4 py-3 text-gray-500 text-xs">{d.user_id}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="text-gray-400 text-sm">No delegations recorded.</p>
          )}
        </div>
      )}
    </div>
  )
}
