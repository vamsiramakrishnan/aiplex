import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getCatalog, listInstances, putLLMRoute, deleteLLMRoute, type LLMBackend } from '../api/client'
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
const getUsageSummary = (period: string) => fetch(`/api/v1/llm/usage/summary?period=${period}`).then(r => r.json()) as Promise<UsageSummary>

interface RouteFormData {
  model_id: string
  backends: LLMBackend[]
  fallbacks: string[]
  budget: {
    max_daily_cost_usd: number
    alert_threshold_pct: number
  }
}

export default function LLMPlex() {
  const [tab, setTab] = useState<'providers' | 'routes' | 'costs'>('providers')
  const [showRouteForm, setShowRouteForm] = useState(false)
  const [editingRoute, setEditingRoute] = useState<RouteConfig | null>(null)
  const [formData, setFormData] = useState<RouteFormData>({
    model_id: '',
    backends: [],
    fallbacks: [],
    budget: { max_daily_cost_usd: 0, alert_threshold_pct: 80 }
  })
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error', message: string } | null>(null)
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null)

  const queryClient = useQueryClient()
  const catalog = useQuery({ queryKey: ['catalog', 'llmplex'], queryFn: () => getCatalog('llmplex') })
  const instances = useQuery({ queryKey: ['instances', 'llmplex'], queryFn: () => listInstances('llmplex') })
  const routes = useQuery({ queryKey: ['llm-routes'], queryFn: getRoutes })
  const usage = useQuery({ queryKey: ['llm-usage', 'day'], queryFn: () => getUsageSummary('day') })

  const putRouteMutation = useMutation({
    mutationFn: ({ modelId, config }: { modelId: string; config: Partial<RouteConfig> }) =>
      putLLMRoute(modelId, config),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['llm-routes'] })
      setFeedback({ type: 'success', message: 'Route saved successfully' })
      setShowRouteForm(false)
      setEditingRoute(null)
      setTimeout(() => setFeedback(null), 3000)
    },
    onError: (error: Error) => {
      setFeedback({ type: 'error', message: error.message })
      setTimeout(() => setFeedback(null), 5000)
    },
  })

  const deleteRouteMutation = useMutation({
    mutationFn: (modelId: string) => deleteLLMRoute(modelId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['llm-routes'] })
      setFeedback({ type: 'success', message: 'Route deleted successfully' })
      setDeleteConfirm(null)
      setTimeout(() => setFeedback(null), 3000)
    },
    onError: (error: Error) => {
      setFeedback({ type: 'error', message: error.message })
      setTimeout(() => setFeedback(null), 5000)
    },
  })

  const openNewRouteForm = () => {
    setFormData({
      model_id: '',
      backends: [],
      fallbacks: [],
      budget: { max_daily_cost_usd: 0, alert_threshold_pct: 80 }
    })
    setEditingRoute(null)
    setShowRouteForm(true)
  }

  const openEditRouteForm = (route: RouteConfig) => {
    setFormData({
      model_id: route.model_id,
      backends: route.backends,
      fallbacks: route.fallbacks || [],
      budget: route.budget || { max_daily_cost_usd: 0, alert_threshold_pct: 80 }
    })
    setEditingRoute(route)
    setShowRouteForm(true)
  }

  const addBackend = () => {
    setFormData({
      ...formData,
      backends: [
        ...formData.backends,
        { provider: 'google', model_id: '', weight: 100, enabled: true }
      ]
    })
  }

  const updateBackend = (index: number, field: keyof LLMBackend, value: string | number | boolean) => {
    const updated = [...formData.backends]
    updated[index] = { ...updated[index], [field]: value }
    setFormData({ ...formData, backends: updated })
  }

  const removeBackend = (index: number) => {
    setFormData({
      ...formData,
      backends: formData.backends.filter((_, i) => i !== index)
    })
  }

  const addFallback = () => {
    const fallback = prompt('Enter fallback model ID:')
    if (fallback) {
      setFormData({ ...formData, fallbacks: [...formData.fallbacks, fallback] })
    }
  }

  const removeFallback = (index: number) => {
    setFormData({
      ...formData,
      fallbacks: formData.fallbacks.filter((_, i) => i !== index)
    })
  }

  const saveRoute = () => {
    if (!formData.model_id) {
      setFeedback({ type: 'error', message: 'Model ID is required' })
      setTimeout(() => setFeedback(null), 3000)
      return
    }
    if (formData.backends.length === 0) {
      setFeedback({ type: 'error', message: 'At least one backend is required' })
      setTimeout(() => setFeedback(null), 3000)
      return
    }

    putRouteMutation.mutate({
      modelId: formData.model_id,
      config: {
        model_id: formData.model_id,
        backends: formData.backends,
        fallbacks: formData.fallbacks.length > 0 ? formData.fallbacks : undefined,
        budget: formData.budget.max_daily_cost_usd > 0 ? formData.budget : undefined
      }
    })
  }

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
          <div className="flex justify-between items-center mb-3">
            <h3 className="font-semibold text-lg">Routing Rules</h3>
            <button
              onClick={openNewRouteForm}
              className="px-4 py-2 bg-brand-600 text-white rounded text-sm hover:bg-brand-700"
            >
              + New Route
            </button>
          </div>

          {feedback && (
            <div className={`mb-4 p-3 rounded ${feedback.type === 'success' ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800'}`}>
              {feedback.message}
            </div>
          )}

          {routes.data && routes.data.length > 0 ? (
            <div className="space-y-4">
              {routes.data.map((rc) => (
                <div key={rc.model_id} className="bg-white rounded-lg shadow p-4">
                  <div className="flex justify-between items-start mb-3">
                    <h4 className="font-semibold">{rc.model_id}</h4>
                    <div className="flex gap-2">
                      <button
                        onClick={() => openEditRouteForm(rc)}
                        className="px-3 py-1 text-sm bg-gray-100 hover:bg-gray-200 rounded"
                      >
                        Edit
                      </button>
                      <button
                        onClick={() => setDeleteConfirm(rc.model_id)}
                        className="px-3 py-1 text-sm bg-red-100 text-red-700 hover:bg-red-200 rounded"
                      >
                        Delete
                      </button>
                    </div>
                  </div>
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
            <p className="text-gray-400 text-sm">No routing rules configured. Click "New Route" to get started.</p>
          )}

          {/* Delete confirmation modal */}
          {deleteConfirm && (
            <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
              <div className="bg-white rounded-lg p-6 max-w-md w-full mx-4">
                <h3 className="font-semibold text-lg mb-2">Delete Route</h3>
                <p className="text-gray-600 mb-4">
                  Are you sure you want to delete the route for <strong>{deleteConfirm}</strong>? This action cannot be undone.
                </p>
                <div className="flex justify-end gap-2">
                  <button
                    onClick={() => setDeleteConfirm(null)}
                    className="px-4 py-2 bg-gray-100 hover:bg-gray-200 rounded"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={() => deleteRouteMutation.mutate(deleteConfirm)}
                    className="px-4 py-2 bg-red-600 text-white hover:bg-red-700 rounded"
                    disabled={deleteRouteMutation.isPending}
                  >
                    {deleteRouteMutation.isPending ? 'Deleting...' : 'Delete'}
                  </button>
                </div>
              </div>
            </div>
          )}

          {/* Route form modal */}
          {showRouteForm && (
            <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50 overflow-y-auto">
              <div className="bg-white rounded-lg p-6 max-w-2xl w-full mx-4 my-8">
                <h3 className="font-semibold text-lg mb-4">
                  {editingRoute ? `Edit Route: ${editingRoute.model_id}` : 'New Route'}
                </h3>

                <div className="space-y-4">
                  {/* Model ID */}
                  <div>
                    <label className="block text-sm font-medium mb-1">Model ID</label>
                    <input
                      type="text"
                      value={formData.model_id}
                      onChange={(e) => setFormData({ ...formData, model_id: e.target.value })}
                      disabled={!!editingRoute}
                      className="w-full px-3 py-2 border rounded disabled:bg-gray-100"
                      placeholder="e.g., gemini-2.5-flash"
                    />
                  </div>

                  {/* Backends */}
                  <div>
                    <div className="flex justify-between items-center mb-2">
                      <label className="block text-sm font-medium">Backends</label>
                      <button
                        onClick={addBackend}
                        className="px-3 py-1 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
                      >
                        + Add Backend
                      </button>
                    </div>
                    <div className="space-y-2">
                      {formData.backends.map((backend, idx) => (
                        <div key={idx} className="bg-gray-50 p-3 rounded space-y-2">
                          <div className="flex gap-2">
                            <select
                              value={backend.provider}
                              onChange={(e) => updateBackend(idx, 'provider', e.target.value)}
                              className="px-2 py-1 border rounded text-sm"
                            >
                              <option value="google">Google</option>
                              <option value="anthropic">Anthropic</option>
                              <option value="openai">OpenAI</option>
                              <option value="bedrock">Bedrock</option>
                              <option value="ollama">Ollama</option>
                            </select>
                            <input
                              type="text"
                              value={backend.model_id}
                              onChange={(e) => updateBackend(idx, 'model_id', e.target.value)}
                              placeholder="Model ID"
                              className="flex-1 px-2 py-1 border rounded text-sm"
                            />
                            <input
                              type="number"
                              value={backend.weight}
                              onChange={(e) => updateBackend(idx, 'weight', parseInt(e.target.value) || 0)}
                              placeholder="Weight"
                              className="w-20 px-2 py-1 border rounded text-sm"
                            />
                            <label className="flex items-center gap-1 text-sm">
                              <input
                                type="checkbox"
                                checked={backend.enabled}
                                onChange={(e) => updateBackend(idx, 'enabled', e.target.checked)}
                              />
                              Enabled
                            </label>
                            <button
                              onClick={() => removeBackend(idx)}
                              className="px-2 py-1 bg-red-100 text-red-700 text-sm rounded hover:bg-red-200"
                            >
                              Remove
                            </button>
                          </div>
                        </div>
                      ))}
                      {formData.backends.length === 0 && (
                        <p className="text-sm text-gray-400">No backends added yet. Click "Add Backend" to start.</p>
                      )}
                    </div>
                  </div>

                  {/* Fallbacks */}
                  <div>
                    <div className="flex justify-between items-center mb-2">
                      <label className="block text-sm font-medium">Fallback Models (optional)</label>
                      <button
                        onClick={addFallback}
                        className="px-3 py-1 bg-gray-200 text-sm rounded hover:bg-gray-300"
                      >
                        + Add Fallback
                      </button>
                    </div>
                    <div className="space-y-1">
                      {formData.fallbacks.map((fb, idx) => (
                        <div key={idx} className="flex items-center gap-2">
                          <span className="text-sm text-gray-600">{fb}</span>
                          <button
                            onClick={() => removeFallback(idx)}
                            className="text-xs text-red-600 hover:text-red-800"
                          >
                            Remove
                          </button>
                        </div>
                      ))}
                    </div>
                  </div>

                  {/* Budget */}
                  <div className="border-t pt-4">
                    <label className="block text-sm font-medium mb-2">Budget (optional)</label>
                    <div className="grid grid-cols-2 gap-3">
                      <div>
                        <label className="block text-xs text-gray-600 mb-1">Max Daily Cost (USD)</label>
                        <input
                          type="number"
                          value={formData.budget.max_daily_cost_usd}
                          onChange={(e) => setFormData({
                            ...formData,
                            budget: { ...formData.budget, max_daily_cost_usd: parseFloat(e.target.value) || 0 }
                          })}
                          className="w-full px-3 py-2 border rounded"
                          placeholder="0"
                          step="0.01"
                        />
                      </div>
                      <div>
                        <label className="block text-xs text-gray-600 mb-1">Alert Threshold (%)</label>
                        <input
                          type="number"
                          value={formData.budget.alert_threshold_pct}
                          onChange={(e) => setFormData({
                            ...formData,
                            budget: { ...formData.budget, alert_threshold_pct: parseInt(e.target.value) || 0 }
                          })}
                          className="w-full px-3 py-2 border rounded"
                          placeholder="80"
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <div className="flex justify-end gap-2 mt-6">
                  <button
                    onClick={() => {
                      setShowRouteForm(false)
                      setEditingRoute(null)
                    }}
                    className="px-4 py-2 bg-gray-100 hover:bg-gray-200 rounded"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={saveRoute}
                    className="px-4 py-2 bg-brand-600 text-white hover:bg-brand-700 rounded"
                    disabled={putRouteMutation.isPending}
                  >
                    {putRouteMutation.isPending ? 'Saving...' : 'Save Route'}
                  </button>
                </div>
              </div>
            </div>
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
