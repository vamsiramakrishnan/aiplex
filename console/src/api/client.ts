const BASE = '/api/v1'
const TOKEN_KEY = 'aiplex_token'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const token = (() => {
    try { return localStorage.getItem(TOKEN_KEY) } catch { return null }
  })()

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init?.headers as Record<string, string> ?? {}),
  }
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ message: res.statusText }))
    throw new Error(err.message ?? `HTTP ${res.status}`)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}

// Types matching Go models
export interface Instance {
  id: string
  plane: string
  template_id: string
  owner: string
  namespace: string
  spiffe_id?: string
  scopes: string[]
  config?: Record<string, unknown>
  status: string
  replicas: number
  display_name?: string
  deployed_at: string
  updated_at: string
  deployed_by: string
  health?: { last_check: string; status: string; latency_ms: number }
}

export interface Template {
  id: string
  source: string
  plane: string
  name: string
  description: string
  image?: string
  model_id?: string
  provider?: string
  capabilities?: string[]
  category: string
  verified: boolean
  tags?: string[]
  pricing?: { input: number; output: number }
}

export interface Agent {
  client_id: string
  display_name: string
  description?: string
  auth_method: string
  grant_types: string[]
  allowed_scopes: string[]
  status: string
  registered_at: string
}

export interface CatalogPage {
  templates: Template[]
  total: number
  page: number
  page_size: number
  sources_failed?: { source: string; error: string }[]
}

// Catalog
export const getCatalog = (plane?: string, page = 0) =>
  request<CatalogPage>(`/catalog?plane=${plane ?? ''}&page=${page}`)

export const getTemplate = (id: string) =>
  request<Template>(`/catalog/${id}`)

// Instances
export const listInstances = (plane?: string) =>
  request<Instance[]>(`/instances?plane=${plane ?? ''}`)

export const getInstance = (id: string) =>
  request<Instance>(`/instances/${id}`)

export const deployInstance = (body: {
  plane: string
  template_id: string
  config?: Record<string, unknown>
  display_name?: string
}) => request<Instance>('/instances', { method: 'POST', body: JSON.stringify(body) })

export const undeployInstance = (id: string) =>
  request<void>(`/instances/${id}`, { method: 'DELETE' })

// Agents
export const listAgents = () => request<Agent[]>('/agents')
export const getAgent = (id: string) => request<Agent>(`/agents/${id}`)
export const registerAgent = (body: {
  client_id: string
  display_name: string
  description?: string
  auth_method: string
  grant_types: string[]
  allowed_scopes: string[]
}) => request<Agent>('/agents', { method: 'POST', body: JSON.stringify(body) })
export const deleteAgent = (id: string) =>
  request<void>(`/agents/${id}`, { method: 'DELETE' })

// LLM Routes
export interface LLMBackend {
  provider: string
  model_id: string
  weight: number
  enabled: boolean
  secret_ref?: string
}

export interface UsageBudget {
  max_daily_cost_usd?: number
  max_monthly_cost_usd?: number
  max_daily_tokens?: number
  alert_threshold_pct?: number
}

export interface LLMRouteConfig {
  id: string
  model_id: string
  backends: LLMBackend[]
  fallbacks?: string[]
  budget?: UsageBudget
}

export const listLLMRoutes = () => request<LLMRouteConfig[]>('/llm/routes')
export const putLLMRoute = (modelId: string, body: Partial<LLMRouteConfig>) =>
  request<LLMRouteConfig>(`/llm/routes/${modelId}`, {
    method: 'PUT', body: JSON.stringify(body)
  })
export const deleteLLMRoute = (modelId: string) =>
  request<void>(`/llm/routes/${modelId}`, { method: 'DELETE' })

// Manifest (apply YAML/JSON from UI)
export interface Manifest {
  version: string
  instances?: Array<{ name: string; plane: string; template: string; config?: Record<string, unknown> }>
  agents?: Array<{
    client_id: string; display_name: string; description?: string
    auth_method: string; grant_types: string[]; allowed_scopes: string[]
  }>
  routes?: Array<{
    model_id: string; backends: LLMBackend[]; fallbacks?: string[]
    budget?: UsageBudget
  }>
}

// Apply manifest (applies each resource sequentially)
export async function applyManifest(manifest: Manifest): Promise<{
  applied: number; failed: string[]
}> {
  const failed: string[] = []
  let applied = 0

  for (const inst of manifest.instances ?? []) {
    try {
      await deployInstance({ plane: inst.plane, template_id: inst.template, display_name: inst.name, config: inst.config })
      applied++
    } catch (e) {
      failed.push(`instance ${inst.name}: ${(e as Error).message}`)
    }
  }

  for (const agent of manifest.agents ?? []) {
    try {
      await registerAgent(agent)
      applied++
    } catch (e) {
      failed.push(`agent ${agent.client_id}: ${(e as Error).message}`)
    }
  }

  for (const route of manifest.routes ?? []) {
    try {
      await putLLMRoute(route.model_id, route)
      applied++
    } catch (e) {
      failed.push(`route ${route.model_id}: ${(e as Error).message}`)
    }
  }

  return { applied, failed }
}

// Deploy history
export interface HistoryEntry {
  timestamp: string
  action: string
  actor: string
  details?: string
}

export const getInstanceHistory = (id: string) =>
  request<HistoryEntry[]>(`/instances/${id}/history`)

// Dashboard stats
export interface DashboardStats {
  total_instances: number
  instances_by_plane: Record<string, number>
  total_agents: number
  total_tool_calls: number
  total_a2a_delegations: number
  total_llm_requests: number
  policy_denials: number
  cost_usd: number
}

export const getDashboardStats = () =>
  request<DashboardStats>('/dashboard/stats')

// Role bindings
export interface RoleBinding {
  id: string
  subject: string
  subject_type: 'user' | 'agent'
  scopes: string[]
  granted_at: string
  granted_by: string
}

export const listRoleBindings = () =>
  request<RoleBinding[]>('/access/bindings')

// Whoami
export interface WhoamiResponse {
  sub: string
  email: string
  scopes: string[]
  agents: string[]
}

// ── Runs (AIPlex ↔ Tape audit; mirrors models.ExecutionRun + ExecutionEvent) ──
//
// The Console reads from these endpoints (PR 7). Identity columns and
// counters drive the run-list view; per-run /events feeds the timeline
// panel. Effect / obligation / budget routes are convenience filters
// over /events for the dedicated tabs in the run detail.

export type ExecutionRunStatus =
  | 'runnable' | 'running' | 'waiting' | 'terminal'
  | 'failed' | 'compensating' | 'stuck' | 'cancelled'

export type ExecutionEventKind =
  | 'run.started' | 'run.completed' | 'run.failed'
  | 'decision.recorded'
  | 'effect.begin' | 'effect.confirmed' | 'effect.failed'
  | 'effect.unknown' | 'effect.duplicate'
  | 'obligation.created' | 'gate.waiting' | 'timer.scheduled'
  | 'budget.charged' | 'policy.violation'

export interface ExecutionRun {
  run_id: string
  tenant_id: string
  agent_id: string
  plane: string
  actor: string
  subject: string
  aiplex_instance_id?: string
  status: ExecutionRunStatus
  started_at: string
  ended_at?: string
  decisions_count: number
  effects_count: number
  unknown_effects: number
  obligations: number
  policy_violations: number
  budget_usd_charged: number
}

export interface ExecutionEvent {
  run_id: string
  seq: number
  tenant_id: string
  agent_id: string
  plane: string
  actor: string
  subject: string
  aiplex_instance_id?: string
  kind: ExecutionEventKind
  scope?: string
  tool?: string
  timestamp: string
  payload_json?: string
}

export interface RunsListParams {
  tenant_id?: string
  agent_id?: string
  has_unknown_effects?: boolean
  has_obligations?: boolean
  limit?: number
}

export const listRuns = (params: RunsListParams = {}) => {
  const qs = new URLSearchParams()
  if (params.tenant_id) qs.set('tenant_id', params.tenant_id)
  if (params.agent_id) qs.set('agent_id', params.agent_id)
  if (params.has_unknown_effects) qs.set('has_unknown_effects', 'true')
  if (params.has_obligations) qs.set('has_obligations', 'true')
  if (params.limit) qs.set('limit', String(params.limit))
  const suffix = qs.toString() ? `?${qs}` : ''
  return request<{ runs: ExecutionRun[] }>(`/runs${suffix}`)
}

export const getRun = (runID: string) =>
  request<ExecutionRun>(`/runs/${encodeURIComponent(runID)}`)

export const listRunEvents = (runID: string, fromSeq = 0, limit = 1000) =>
  request<{ events: ExecutionEvent[] }>(
    `/runs/${encodeURIComponent(runID)}/events?from_seq=${fromSeq}&limit=${limit}`
  )

export const listRunEffects = (runID: string) =>
  request<{ effects: ExecutionEvent[] }>(`/runs/${encodeURIComponent(runID)}/effects`)

export const listRunObligations = (runID: string) =>
  request<{ obligations: ExecutionEvent[] }>(`/runs/${encodeURIComponent(runID)}/obligations`)

export const listRunBudgets = (runID: string) =>
  request<{ budgets: ExecutionEvent[] }>(`/runs/${encodeURIComponent(runID)}/budgets`)

export const getWhoami = () =>
  request<WhoamiResponse>('/auth/whoami')
