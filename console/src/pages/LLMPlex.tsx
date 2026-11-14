import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import LedgerCosts from '../components/LedgerCosts'
import { getCatalog, listInstances, putLLMRoute, deleteLLMRoute, authenticatedRequest, type LLMBackend } from '../api/client'
import StatusBadge from '../components/StatusBadge'
import ModelSurfacesPanel from '../components/ModelSurfacesPanel'
import OpenBatches from '../components/OpenBatches'
import { UNKNOWN } from '../api/usage'
import { PROVIDER_IDS, PROVIDER_LABELS, type ProviderId } from '../api/modelSurfaces'

const providerLabel = (id: string) => PROVIDER_LABELS[id as ProviderId] ?? id

interface RouteConfig {
 revision?: number
  model_id: string
  backends: { provider: string; model_id: string; weight: number; enabled: boolean }[]
  fallbacks?: string[]
  cache_ttl_seconds?: number
  budget?: { max_daily_cost_usd?: number; max_monthly_cost_usd?: number; max_daily_tokens?: number; alert_threshold_pct?: number }
}


const getRoutes = () => authenticatedRequest<RouteConfig[]>('/api/v1/llm/routes')

interface RouteFormData {
  model_id: string
  backends: LLMBackend[]
  fallbacks: string[]
  /** The cost is the text typed: empty means no budget, so a typed 0 can be refused instead of dropped. */
  budget: {
    max_daily_cost_usd: string
    alert_threshold_pct: number
  }
}

export default function LLMPlex() {
  const [tab, setTab] = useState<'providers' | 'routes' | 'costs' | 'surfaces' | 'batches'>('providers')
  const [showRouteForm, setShowRouteForm] = useState(false)
  const [editingRoute, setEditingRoute] = useState<RouteConfig | null>(null)
  const [formData, setFormData] = useState<RouteFormData>({
    model_id: '',
    backends: [],
    fallbacks: [],
    budget: { max_daily_cost_usd: '', alert_threshold_pct: 80 }
  })
  const [fallbackDraft, setFallbackDraft] = useState('')
  const [formError, setFormError] = useState('')
  const [feedback, setFeedback] = useState<{ type: 'success' | 'error', message: string } | null>(null)
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null)

  const queryClient = useQueryClient()
  const catalog = useQuery({ queryKey: ['catalog', 'llmplex'], queryFn: () => getCatalog('llmplex') })
  const instances = useQuery({ queryKey: ['instances', 'llmplex'], queryFn: () => listInstances('llmplex') })
  const routes = useQuery({ queryKey: ['llm-routes'], queryFn: getRoutes })
  const ownTab = tab === 'surfaces' || tab === 'batches' // these tabs load and report their own data
  const activeLoading = ownTab ? false : tab === 'providers' ? catalog.isLoading || instances.isLoading : tab === 'routes' ? routes.isLoading : false
  const activeError = ownTab ? null : tab === 'providers' ? catalog.error ?? instances.error : tab === 'routes' ? routes.error : null

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
    mutationFn: (modelId: string) => deleteLLMRoute(modelId, routes.data?.find(route => route.model_id === modelId)?.revision),
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
      budget: { max_daily_cost_usd: '', alert_threshold_pct: 80 }
    })
    setFallbackDraft('')
    setFormError('')
    setEditingRoute(null)
    setShowRouteForm(true)
  }

  const openEditRouteForm = (route: RouteConfig) => {
    setFormData({
      model_id: route.model_id,
      backends: route.backends,
      fallbacks: route.fallbacks || [],
      budget: route.budget
        ? { max_daily_cost_usd: route.budget.max_daily_cost_usd == null ? '' : String(route.budget.max_daily_cost_usd), alert_threshold_pct: route.budget.alert_threshold_pct ?? 80 }
        : { max_daily_cost_usd: '', alert_threshold_pct: 80 }
    })
    setFallbackDraft('')
    setFormError('')
    setEditingRoute(route)
    setShowRouteForm(true)
  }

  const addBackend = () => {
    setFormData({
      ...formData,
      backends: [
        ...formData.backends,
        { provider: PROVIDER_IDS[0], model_id: '', weight: 100, enabled: true }
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
    const fallback = fallbackDraft.trim()
    if (!fallback) return
    if (!formData.fallbacks.includes(fallback)) setFormData({ ...formData, fallbacks: [...formData.fallbacks, fallback] })
    setFallbackDraft('')
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

    // An empty cost means no budget. A typed 0 would be dropped by the API (omitempty) and read as "no cap", so it is refused.
    const costText = formData.budget.max_daily_cost_usd.trim()
    let budget: { max_daily_cost_usd: number; alert_threshold_pct: number } | undefined
    if (costText !== '') {
      const cost = Number(costText)
      if (!Number.isFinite(cost) || cost <= 0) {
        setFormError('Max daily cost must be greater than 0. Leave it empty for no budget.')
        return
      }
      budget = { max_daily_cost_usd: cost, alert_threshold_pct: formData.budget.alert_threshold_pct }
    }
    setFormError('')

    putRouteMutation.mutate({
      modelId: formData.model_id,
      config: {
        revision: editingRoute?.revision ?? 0,
        model_id: formData.model_id,
        backends: formData.backends,
        fallbacks: formData.fallbacks.length > 0 ? formData.fallbacks : undefined,
        budget
      }
    })
  }

  return (
    <div>
      <h1 className="text-2xl font-bold mb-2">LLMPlex</h1>
      <p className="text-gray-600 dark:text-gray-400 mb-6">Agent &harr; Model (LLM providers)</p>

      <div className="flex flex-wrap gap-2 mb-6">
        {(['providers', 'routes', 'costs', 'surfaces', 'batches'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 rounded text-sm capitalize ${tab === t ? 'bg-brand-600 text-brand-contrast' : 'bg-gray-100 dark:bg-gray-800'}`}
          >
            {t}
          </button>
        ))}
      </div>

      {activeLoading && <p role="status">Loading module data…</p>}
      {activeError && <p role="alert" className="text-status-denied-fg mb-4">Could not load module data: {activeError.message}</p>}

      {tab === 'surfaces' && <ModelSurfacesPanel />}
      {tab === 'batches' && <OpenBatches />}

      {tab === 'providers' && !activeLoading && !activeError && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Available Models</h3>
          <div className="grid grid-cols-3 gap-4 mb-8">
            {catalog.data?.templates?.map((t) => (
              <div key={t.id} className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
                <div className="flex items-center justify-between mb-2">
                  <h4 className="font-semibold">{t.name}</h4>
                  <span className="text-xs text-gray-600 dark:text-gray-400 capitalize">{t.provider}</span>
                </div>
                <p className="text-sm text-gray-600 dark:text-gray-400 mb-2">{t.description}</p>
                {t.pricing && (
                  <p className="text-xs text-gray-600 dark:text-gray-400">
                    {t.pricing.input == null ? UNKNOWN : `$${t.pricing.input}`}/M input &middot; {t.pricing.output == null ? UNKNOWN : `$${t.pricing.output}`}/M output
                  </p>
                )}
                {t.capabilities && (
                  <div className="flex gap-1 mt-2 flex-wrap">
                    {t.capabilities.map((cap) => (
                      <span key={cap} className="px-1.5 py-0.5 bg-brand-50 text-brand-700 text-xs rounded">
                        {cap}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>

          <h3 className="font-semibold text-lg mb-3">Deployed LLM instances</h3>
          {instances.data && instances.data.length > 0 ? (
            <div className="bg-white rounded-lg shadow dark:bg-gray-900">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 dark:bg-gray-900">
                  <tr>
                    <th className="text-left px-4 py-3">Model</th>
                    <th className="text-left px-4 py-3">Provider</th>
                    <th className="text-left px-4 py-3">Status</th>
                  </tr>
                </thead>
                <tbody>
                  {instances.data.map((inst) => (
                    <tr key={inst.id} className="border-t">
                      <td className="px-4 py-3 font-medium"><Link to={`/instances/${encodeURIComponent(inst.id)}`} className="hover:underline">{inst.display_name || inst.id}</Link></td>
                      <td className="px-4 py-3 text-gray-600 dark:text-gray-400">{inst.template_id}</td>
                      <td className="px-4 py-3"><StatusBadge status={inst.status} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="text-gray-600 dark:text-gray-400 text-sm">No LLM instances deployed yet.</p>
          )}
        </div>
      )}

      {tab === 'routes' && !activeLoading && !activeError && (
        <div>
          <div className="flex justify-between items-center mb-3">
            <h3 className="font-semibold text-lg">Routing Rules</h3>
            <button
              onClick={openNewRouteForm}
              className="px-4 py-2 bg-brand-600 text-brand-contrast rounded text-sm hover:bg-brand-700"
            >
              + New Route
            </button>
          </div>

          {feedback && (
            <div className={`mb-4 p-3 rounded ${feedback.type === 'success' ? 'bg-status-ok-bg text-status-ok-fg' : 'bg-status-denied-bg text-status-denied-fg'}`}>
              {feedback.message}
            </div>
          )}

          {routes.data && routes.data.length > 0 ? (
            <div className="space-y-4">
              {routes.data.map((rc) => (
                <div key={rc.model_id} className="bg-white rounded-lg shadow p-4 dark:bg-gray-900">
                  <div className="flex justify-between items-start mb-3">
                    <h4 className="font-semibold">{rc.model_id}</h4>
                    <div className="flex gap-2">
                      <button
                        onClick={() => openEditRouteForm(rc)}
                        className="px-3 py-1 text-sm bg-gray-100 hover:bg-gray-200 rounded dark:bg-gray-800"
                      >
                        Edit
                      </button>
                      <button
                        onClick={() => setDeleteConfirm(rc.model_id)}
                        className="px-3 py-1 text-sm bg-status-denied-bg text-status-denied-fg hover:bg-status-denied-bg rounded"
                      >
                        Delete
                      </button>
                    </div>
                  </div>
                  <div className="space-y-2">
                    {rc.backends.map((b, i) => (
                      <div key={i} className="flex items-center gap-3 text-sm">
                        <span className={`w-2 h-2 rounded-full ${b.enabled ? 'bg-status-ok-fg' : 'bg-gray-300'}`} />
                        <span className="font-medium capitalize">{b.provider}</span>
                        <span className="text-gray-600 dark:text-gray-400">{b.model_id}</span>
                        <div className="flex-1">
                          <div className="bg-gray-100 rounded-full h-2 dark:bg-gray-800">
                            <div className="bg-brand-600 rounded-full h-2" style={{ width: `${b.weight ?? 0}%` }} />
                          </div>
                        </div>
                        <span className="text-gray-600 dark:text-gray-400 w-12 text-right">{b.weight == null ? UNKNOWN : `${b.weight}%`}</span>
                      </div>
                    ))}
                  </div>
                  {rc.fallbacks && rc.fallbacks.length > 0 && (
                    <p className="text-xs text-gray-600 dark:text-gray-400 mt-2">
                      Fallback: {rc.fallbacks.join(' → ')}
                    </p>
                  )}
                  {rc.budget && (
                    <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">
                      Budget: {rc.budget.max_daily_cost_usd == null ? 'no daily cap' : `$${rc.budget.max_daily_cost_usd}/day`}
                      {rc.budget.max_monthly_cost_usd != null && `, $${rc.budget.max_monthly_cost_usd}/month`}
                      {rc.budget.max_daily_tokens != null && `, ${rc.budget.max_daily_tokens} tokens/day`}
                      {(rc.budget.alert_threshold_pct ?? 0) > 0 && ` (alert at ${rc.budget.alert_threshold_pct}%)`}
                    </p>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <p className="text-gray-600 dark:text-gray-400 text-sm">No routing rules configured. Click "New Route" to get started.</p>
          )}

          {/* Delete confirmation modal */}
          {deleteConfirm && (
            <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center z-50">
              <div className="bg-white rounded-lg p-6 max-w-md w-full mx-4 dark:bg-gray-900">
                <h3 className="font-semibold text-lg mb-2">Delete Route</h3>
                <p className="text-gray-600 mb-4 dark:text-gray-400">
                  Are you sure you want to delete the route for <strong>{deleteConfirm}</strong>? This action cannot be undone.
                </p>
                <div className="flex justify-end gap-2">
                  <button
                    onClick={() => setDeleteConfirm(null)}
                    className="px-4 py-2 bg-gray-100 hover:bg-gray-200 rounded dark:bg-gray-800"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={() => deleteRouteMutation.mutate(deleteConfirm)}
                    className="px-4 py-2 bg-status-denied-fg text-status-denied-bg hover:bg-status-denied-fg rounded"
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
              <div className="bg-white rounded-lg p-6 max-w-2xl w-full mx-4 my-8 dark:bg-gray-900">
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
                      aria-label="Model ID" placeholder="e.g., gemini-2.5-flash"
                    />
                  </div>

                  {/* Backends */}
                  <div>
                    <div className="flex justify-between items-center mb-2">
                      <label className="block text-sm font-medium">Backends</label>
                      <button
                        onClick={addBackend}
                        className="px-3 py-1 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700"
                      >
                        + Add Backend
                      </button>
                    </div>
                    <div className="space-y-2">
                      {formData.backends.map((backend, idx) => (
                        <div key={idx} className="bg-gray-50 p-3 rounded space-y-2 dark:bg-gray-900">
                          <div className="flex gap-2">
                            <select
                              aria-label="Provider"
                              value={backend.provider}
                              onChange={(e) => updateBackend(idx, 'provider', e.target.value)}
                              className="px-2 py-1 border rounded text-sm"
                            >
                              {!(PROVIDER_IDS as readonly string[]).includes(backend.provider) && <option value={backend.provider}>{backend.provider}</option>}
                              {PROVIDER_IDS.map(p => <option key={p} value={p}>{providerLabel(p)}</option>)}
                            </select>
                            <input
                              type="text"
                              value={backend.model_id}
                              onChange={(e) => updateBackend(idx, 'model_id', e.target.value)}
                              aria-label="Model ID" placeholder="Model ID"
                              className="flex-1 px-2 py-1 border rounded text-sm"
                            />
                            <input
                              type="number"
                              value={backend.weight}
                              onChange={(e) => updateBackend(idx, 'weight', parseInt(e.target.value) || 0)}
                              aria-label="Weight" placeholder="Weight"
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
                              className="px-2 py-1 bg-status-denied-bg text-status-denied-fg text-sm rounded hover:bg-status-denied-bg"
                            >
                              Remove
                            </button>
                          </div>
                        </div>
                      ))}
                      {formData.backends.length === 0 && (
                        <p className="text-sm text-gray-600 dark:text-gray-400">No backends added yet. Click "Add Backend" to start.</p>
                      )}
                    </div>
                  </div>

                  {/* Fallbacks */}
                  <div>
                    <div className="flex justify-between items-center mb-2">
                      <label className="block text-sm font-medium">Fallback Models (optional)</label>
                      <button
                        onClick={addFallback}
                        className="px-3 py-1 bg-gray-200 text-sm rounded hover:bg-gray-300 dark:bg-gray-700"
                      >
                        + Add Fallback
                      </button>
                    </div>
                    <div className="flex gap-2 mb-2">
                      <input
                        type="text"
                        value={fallbackDraft}
                        onChange={(e) => setFallbackDraft(e.target.value)}
                        onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addFallback() } }}
                        aria-label="Fallback model ID" placeholder="Fallback model ID"
                        className="flex-1 px-2 py-1 border rounded text-sm"
                      />
                    </div>
                    <div className="space-y-1">
                      {formData.fallbacks.map((fb, idx) => (
                        <div key={idx} className="flex items-center gap-2">
                          <span className="text-sm text-gray-600 dark:text-gray-400">{fb}</span>
                          <button
                            onClick={() => removeFallback(idx)}
                            className="text-xs text-status-denied-fg hover:text-status-denied-fg"
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
                        <label htmlFor="route-max-daily-cost" className="block text-xs text-gray-600 mb-1 dark:text-gray-400">Max Daily Cost (USD)</label>
                        <input
                          id="route-max-daily-cost"
                          type="number"
                          value={formData.budget.max_daily_cost_usd}
                          onChange={(e) => setFormData({
                            ...formData,
                            budget: { ...formData.budget, max_daily_cost_usd: e.target.value }
                          })}
                          className="w-full px-3 py-2 border rounded"
                          placeholder="empty = no budget"
                          step="0.01"
                        />
                      </div>
                      <div>
                        <label className="block text-xs text-gray-600 mb-1 dark:text-gray-400">Alert Threshold (%)</label>
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

                {formError && <p role="alert" className="mt-4 text-sm text-status-denied-fg">{formError}</p>}

                <div className="flex justify-end gap-2 mt-6">
                  <button
                    onClick={() => {
                      setShowRouteForm(false)
                      setEditingRoute(null)
                    }}
                    className="px-4 py-2 bg-gray-100 hover:bg-gray-200 rounded dark:bg-gray-800"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={saveRoute}
                    className="px-4 py-2 bg-brand-600 text-brand-contrast hover:bg-brand-700 rounded"
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

      {tab === 'costs' && <LedgerCosts />}
    </div>
  )
}
