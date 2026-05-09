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

// --- Capability primitive ---

export interface Cap {
  uri: string                              // cap://kind/name@version
  actions?: string[]
  constraints?: Record<string, unknown>
  nbf?: number
  exp?: number
}

export interface Capability {
  uri: string
  kind: string
  name: string
  version: string
  provider?: string
  actions?: string[]
  description?: string
  tags?: string[]
  repository?: string
  image?: string
}

// --- Resources ---

export interface Instance {
  id: string
  kind: string
  template_id: string
  owner: string
  namespace: string
  spiffe_id?: string
  capabilities: Cap[]
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
  kind: string
  name: string
  description: string
  image?: string
  version?: string
  capabilities?: Capability[]
  model_id?: string
  provider?: string
  model_tags?: string[]
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
  allowed_caps: Cap[]
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
export const getCatalog = (kind?: string, page = 0) =>
  request<CatalogPage>(`/catalog?kind=${kind ?? ''}&page=${page}`)

export const getTemplate = (id: string) =>
  request<Template>(`/catalog/${id}`)

// Instances
export const listInstances = (kind?: string) =>
  request<Instance[]>(`/instances?kind=${kind ?? ''}`)

export const getInstance = (id: string) =>
  request<Instance>(`/instances/${id}`)

export const deployInstance = (body: {
  kind: string
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
  allowed_caps: Cap[]
}) => request<Agent>('/agents', { method: 'POST', body: JSON.stringify(body) })
export const deleteAgent = (id: string) =>
  request<void>(`/agents/${id}`, { method: 'DELETE' })

// LLM Routes (kind=model administrative endpoints)
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
  instances?: Array<{ name: string; kind: string; template: string; config?: Record<string, unknown> }>
  agents?: Array<{
    client_id: string; display_name: string; description?: string
    auth_method: string; grant_types: string[]; allowed_caps: Cap[]
  }>
  routes?: Array<{
    model_id: string; backends: LLMBackend[]; fallbacks?: string[]
    budget?: UsageBudget
  }>
}

export async function applyManifest(manifest: Manifest): Promise<{
  applied: number; failed: string[]
}> {
  const failed: string[] = []
  let applied = 0

  for (const inst of manifest.instances ?? []) {
    try {
      await deployInstance({ kind: inst.kind, template_id: inst.template, display_name: inst.name, config: inst.config })
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
  running_instances: number
  registered_agents: number
  active_kinds: number
  instances_by_kind: Record<string, number>
  total_tool_calls: number
  total_a2a_delegations: number
  total_llm_requests: number
  policy_denials: number
  daily_cost_usd: number
  daily_tokens: number
  daily_requests: number
}

export const getDashboardStats = () =>
  request<DashboardStats>('/dashboard/stats')

// Role bindings
export interface RoleBinding {
  id: string
  group: string
  role: 'admin' | 'deployer' | 'viewer' | 'agent'
  caps: Cap[]
  description?: string
  created_at: string
  created_by: string
}

export const listRoleBindings = () =>
  request<RoleBinding[]>('/iam/role-bindings')

// Whoami
export interface WhoamiResponse {
  identity: {
    subject: string
    email: string
    display_name: string
    groups: string[]
  }
  roles: string[]
  caps: Cap[]
}

export const getWhoami = () =>
  request<WhoamiResponse>('/iam/whoami')
