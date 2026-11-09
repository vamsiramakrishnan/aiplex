const BASE = '/api/v1'
const TOKEN_KEY = 'aiplex_token'

// Shared transport for API and authentication endpoints. Credentials stay in
// headers; callers get consistent non-2xx handling.
export async function authenticatedRequest<T>(path: string, init?: RequestInit, decode?: (response: Response) => Promise<T>): Promise<T> {
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

  const res = await fetch(path, {
    ...init,
    headers,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ message: res.statusText }))
    if (res.status === 401) window.dispatchEvent(new CustomEvent('aiplex:session-invalid', { detail: { token } }))
    throw new Error(err.message ?? `HTTP ${res.status}`)
  }
  if (res.status === 204) return undefined as T
  return decode ? decode(res) : res.json()
}

function request<T>(path: string, init?: RequestInit): Promise<T> {
  return authenticatedRequest<T>(`${BASE}${path}`, init)
}

// Types matching Go models
export interface ModuleDescriptor {
  name: string
  version: string
  contract_version: string
  capabilities?: string[]
  navigation?: { path: string; title: string }[]
  commands?: { name: string; description: string }[]
}

export const listModules = async (): Promise<ModuleDescriptor[]> => {
  const result = await request<ModuleDescriptor[]>('/modules')
  if (!Array.isArray(result) || result.some((d) => !d || typeof d.name !== 'string' || typeof d.version !== 'string' || typeof d.contract_version !== 'string' ||
    (d.navigation !== undefined && (!Array.isArray(d.navigation) || d.navigation.some((entry) => !entry || typeof entry.path !== 'string' || typeof entry.title !== 'string'))))) {
    throw new Error('Invalid module discovery response')
  }
  return result
}

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
 revision?: number
  client_id: string
  display_name: string
  description?: string
  auth_method: string
  grant_types: string[]
  allowed_scopes: string[]
  status: string
  registered_at: string
  redirect_uris?: string[]
  client_secret?: string
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
  redirect_uris?: string[]
}) => request<Agent>('/agents', { method: 'POST', body: JSON.stringify(body) })
export const deleteAgent = async (id: string, revision?: number) => {
 const current = revision ?? (await getAgent(id)).revision
 if (!current) throw new Error("Reload the agent before deleting it")
 return request<void>(`/agents/${id}`, { method: "DELETE", headers: { "If-Match": String(current) } })
}

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
 revision?: number
  id: string
  model_id: string
  backends: LLMBackend[]
  fallbacks?: string[]
  budget?: UsageBudget
}

export const listLLMRoutes = () => request<LLMRouteConfig[]>('/llm/routes')
export const putLLMRoute = (modelId: string, body: Partial<LLMRouteConfig>) =>
  request<LLMRouteConfig>(`/llm/routes/${modelId}`, {
    method: 'PUT', body: JSON.stringify(body), headers: { 'If-Match': String(body.revision ?? 0) }
  })
export const deleteLLMRoute = async (modelId: string, revision?: number) => {
 const current = revision ?? (await request<LLMRouteConfig>(`/llm/routes/${modelId}`)).revision
 if (!current) throw new Error("Reload the route before deleting it")
 return request<void>(`/llm/routes/${modelId}`, { method: "DELETE", headers: { "If-Match": String(current) } })
}

// Manifest (apply YAML/JSON from UI)
export interface Manifest {
  version: string
  instances?: Array<{ name: string; plane: string; template: string; config?: Record<string, unknown> }>
  agents?: Array<{
    client_id: string; display_name: string; description?: string
    auth_method: string; grant_types: string[]; allowed_scopes: string[]
    redirect_uris?: string[]
  }>
  routes?: Array<{
    revision?: number
    model_id: string; backends: LLMBackend[]; fallbacks?: string[]
    budget?: UsageBudget
  }>
}

// Apply manifest (applies each resource sequentially)
export async function applyManifest(manifest: Manifest): Promise<{
  applied: number; failed: string[]
}> {
  if (!manifest || typeof manifest !== 'object' || !['v1', '1'].includes(manifest.version)) throw new Error('Manifest version must be v1.')
  for (const key of ['instances', 'agents', 'routes'] as const) if (manifest[key] !== undefined && !Array.isArray(manifest[key])) throw new Error(`Manifest ${key} must be an array.`)
  if (!(manifest.instances?.length || manifest.agents?.length || manifest.routes?.length)) throw new Error('Add at least one resource before applying a manifest.')
  if (manifest.instances?.some((item) => !item || typeof item.name !== 'string' || !item.name || typeof item.template !== 'string' || !item.template || !['mcplex', 'a2aplex', 'llmplex', 'skillsplex'].includes(item.plane))) throw new Error('Every instance requires a name, supported plane and template ID.')
  if (manifest.agents?.some((item) => !item || !item.client_id || !item.display_name || !Array.isArray(item.allowed_scopes) || !Array.isArray(item.grant_types))) throw new Error('Every agent requires its identity, auth method, grant types and scope ceiling.')
  if (manifest.routes?.some((item) => !item || !item.model_id || !Array.isArray(item.backends) || item.backends.length === 0)) throw new Error('Every route requires a model ID and at least one backend.')
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
 revision?: number
  id: string
  group: string
  role: 'admin' | 'deployer' | 'viewer' | 'agent'
  provider?: string
  pool_id?: string
  description?: string
  scopes: string[]
  created_at: string
  created_by: string
}

export const listRoleBindings = () =>
  request<RoleBinding[]>('/iam/role-bindings')

// Whoami
export type WhoamiResponse = ResolvedAccess

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
  | 'run.started' | 'run.completed' | 'run.failed' | 'run.compacted'
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
  compacted?: boolean
  compacted_at?: string
  retained_until?: string
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

// ── Operator audit + actions (PR 11 items 8, 10) ──────────────────────────

export interface OperatorAudit {
  id: string
  run_id: string
  action: 'redrive' | 'reconcile' | 'cancel' | 'signal' | 'compensate'
  actor: string
  at: string
  reason?: string
  gate_name?: string
  resolution?: string
  status: 'accepted' | 'failed'
  error?: string
}

export const listOperatorAudit = (runID: string) =>
  request<{ audit: OperatorAudit[] }>(`/runs/${encodeURIComponent(runID)}/operator-audit`)

// Operator actions return 202 Accepted with { accepted, action, run_id }.
// Wires the Idempotency-Key header so the middleware dedupes double-clicks.

function withIdempotencyKey(runID: string, action: string, body?: object): RequestInit {
  return {
    method: 'POST',
    headers: {
      'Idempotency-Key': `${runID}|${action}|${Date.now()}`,
    },
    body: body ? JSON.stringify(body) : undefined,
  }
}

export const runRedrive = (runID: string) =>
  request<{ accepted: boolean; action: string; run_id: string }>(
    `/runs/${encodeURIComponent(runID)}/redrive`,
    withIdempotencyKey(runID, 'redrive'),
  )

export const runReconcile = (runID: string) =>
  request<{ accepted: boolean; action: string; run_id: string }>(
    `/runs/${encodeURIComponent(runID)}/reconcile`,
    withIdempotencyKey(runID, 'reconcile'),
  )

export const runCancel = (runID: string, reason: string) =>
  request<{ accepted: boolean; action: string; run_id: string }>(
    `/runs/${encodeURIComponent(runID)}/cancel`,
    withIdempotencyKey(runID, 'cancel', { reason }),
  )

export const runSignal = (runID: string, gateName: string, resolutionJson: string) =>
  request<{ accepted: boolean; action: string; run_id: string }>(
    `/runs/${encodeURIComponent(runID)}/signal`,
    withIdempotencyKey(runID, 'signal', { gate_name: gateName, resolution_json: resolutionJson }),
  )

export const runCompensate = (runID: string) =>
  request<{ accepted: boolean; action: string; run_id: string }>(
    `/runs/${encodeURIComponent(runID)}/compensate`,
    withIdempotencyKey(runID, 'compensate'),
  )

// Runs health — drives the empty-state checklist (PR 11 item 13).

export interface RunsHealth {
  last_ingest_at: string
  has_runs: boolean
  tape_instances_count: number
  now: string
}

export const getRunsHealth = () => request<RunsHealth>('/runs/_health')

export interface ResolvedAccess {
  identity: { principal_id: string; subject: string; email: string; actor_id?: string; authorized_party?: string }
  roles: string[]
  scopes: string[]
  group_scopes?: string[]
  manual_scopes?: string[]
}

export const getWhoami = () => request<ResolvedAccess>('/iam/whoami')
