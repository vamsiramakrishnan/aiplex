import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getCatalog, listInstances } from '../api/client'
import StatusBadge from '../components/StatusBadge'

interface RouteConfig {
  model_id: string
  backends: { provider: string; model_id: string; weight: number; enabled: boolean }[]
  fallbacks?: string[]
  cache_ttl_seconds?: number
  budget?: { max_daily_cost_usd: number; alert_threshold_pct: number }
}

interface UsageSummary {
  model_id: string
  period: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  total_cost_usd: number
  request_count: number
  cache_hits: number
  avg_latency_ms: number
}

const getRoutes = () => fetch('/api/v1/llm/routes').then(r => r.json()) as Promise<RouteConfig[]>
const getProviders = () => fetch('/api/v1/llm/providers').then(r => r.json()) as Promise<{ provider: string; display_name: string; enabled: boolean }[]>
const getUsageSummary = (period: string) => fetch(`/api/v1/llm/usage/summary?period=${period}`).then(r => r.json()) as Promise<UsageSummary>

export default function LLMPlex() {
  const [tab, setTab] = useState<'providers' | 'routes' | 'costs'>('providers')
  const catalog = useQuery({ queryKey: ['catalog', 'llmplex'], queryFn: () => getCatalog('llmplex') })
  const instances = useQuery({ queryKey: ['instances', 'llmplex'], queryFn: () => listInstances('llmplex') })
  const routes = useQuery({ queryKey: ['llm-routes'], queryFn: getRoutes })
  const usage = useQuery({ queryKey: ['llm-usage', 'day'], queryFn: () => getUsageSummary('day') })

  return (
    <div>
      <h2 className="text-2xl font-bold mb-2">LLMPlex</h2>
      <p className="text-gray-500 mb-6">Agent &harr; Model (LLM providers)</p>

      <div className="flex gap-2 mb-6">
        {(['providers', 'routes', 'costs'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 rounded text-sm capitalize ${tab === t ? 'bg-brand-600 text-white' : 'bg-gray-100'}`}
          >
            {t}
          </button>
        ))}
      </div>

      {tab === 'providers' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Available Models</h3>
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
      )}

      {tab === 'routes' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Routing Rules</h3>
          {routes.data && routes.data.length > 0 ? (
            <div className="space-y-4">
              {routes.data.map((rc) => (
                <div key={rc.model_id} className="bg-white rounded-lg shadow p-4">
                  <h4 className="font-semibold mb-3">{rc.model_id}</h4>
                  <div className="space-y-2">
                    {rc.backends.map((b, i) => (
                      <div key={i} className="flex items-center gap-3 text-sm">
                        <span className={`w-2 h-2 rounded-full ${b.enabled ? 'bg-green-500' : 'bg-gray-300'}`} />
                        <span className="font-medium capitalize">{b.provider}</span>
                        <span className="text-gray-500">{b.model_id}</span>
                        <div className="flex-1">
                          <div className="bg-gray-100 rounded-full h-2">
                            <div className="bg-brand-600 rounded-full h-2" style={{ width: `${b.weight}%` }} />
                          </div>
                        </div>
                        <span className="text-gray-500 w-12 text-right">{b.weight}%</span>
                      </div>
                    ))}
                  </div>
                  {rc.fallbacks && rc.fallbacks.length > 0 && (
                    <p className="text-xs text-gray-400 mt-2">
                      Fallback: {rc.fallbacks.join(' → ')}
                    </p>
                  )}
                  {rc.budget && (
                    <p className="text-xs text-gray-400 mt-1">
                      Budget: ${rc.budget.max_daily_cost_usd}/day
                      {rc.budget.alert_threshold_pct > 0 && ` (alert at ${rc.budget.alert_threshold_pct}%)`}
                    </p>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <p className="text-gray-400 text-sm">No routing rules configured. Deploy a model to get started.</p>
          )}
        </div>
      )}

      {tab === 'costs' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Cost Tracking (Last 24h)</h3>
          {usage.data ? (
            <div className="grid grid-cols-4 gap-4 mb-6">
              <div className="bg-white rounded-lg shadow p-4">
                <p className="text-sm text-gray-500">Total Cost</p>
                <p className="text-2xl font-bold">${usage.data.total_cost_usd.toFixed(2)}</p>
              </div>
              <div className="bg-white rounded-lg shadow p-4">
                <p className="text-sm text-gray-500">Total Tokens</p>
                <p className="text-2xl font-bold">{(usage.data.total_tokens / 1000).toFixed(1)}K</p>
              </div>
              <div className="bg-white rounded-lg shadow p-4">
                <p className="text-sm text-gray-500">Requests</p>
                <p className="text-2xl font-bold">{usage.data.request_count}</p>
              </div>
              <div className="bg-white rounded-lg shadow p-4">
                <p className="text-sm text-gray-500">Avg Latency</p>
                <p className="text-2xl font-bold">{usage.data.avg_latency_ms.toFixed(0)}ms</p>
              </div>
            </div>
          ) : (
            <p className="text-gray-400 text-sm">No usage data yet.</p>
          )}
        </div>
      )}
    </div>
  )
}
