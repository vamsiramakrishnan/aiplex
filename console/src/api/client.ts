const BASE = '/api/v1'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
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
