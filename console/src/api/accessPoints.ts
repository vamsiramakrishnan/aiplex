import { authenticatedRequest, listAgents, type Agent } from './client'
import type { ConnectEndpoint, ConnectInfo } from './overview'
import { listToolConnections, type ToolConnectionRecord } from './toolAuth'

// Access points: one stable URL (/ap/{id}/...) that enforces identity, access
// and QoS in front of models, tools and skills. Contract: internal/api/access_point*.go.

export type CallerKind = 'agent' | 'user' | 'client'
export const CALLER_KINDS: readonly CallerKind[] = ['agent', 'user', 'client']

export interface GroupRef { source: string; id: string }
export interface AccessPointSubjects {
  groups?: GroupRef[]
  principals?: string[]
  agents?: string[]
  client_registrations?: string[]
}
export interface AccessPointModel { name: string; surface_ref: string; native_model: string }
export interface AccessPointTool { connection_ref: string; expose?: string[] }
export interface AccessPointSkill { instance_ref: string }
export interface AccessPointLimits { requests_per_minute?: number; tokens_per_minute?: number; concurrency?: number }
/** principal: limits apply to each caller separately; access_point: one pool shared by every caller. */
export type QoSScope = 'principal' | 'access_point'
/** What the limits are shared by. An absent scope is the server's default, which is per principal. */
export const qosScopeLabel = (scope?: QoSScope) => (scope === 'access_point' ? 'shared pool (every caller)' : scope === 'principal' ? 'per caller' : 'per caller (default)')
export interface AccessPointQoS { class: string; weight?: number; scope?: QoSScope; limits?: AccessPointLimits; budget_refs?: string[] }

export interface AccessPointSpec {
  id: string
  display_name?: string
  caller_kinds: CallerKind[]
  subjects: AccessPointSubjects
  models?: AccessPointModel[]
  tools?: AccessPointTool[]
  skills?: AccessPointSkill[]
  qos?: AccessPointQoS
}
export interface AccessPointRecord { id: string; revision: number; tenant_id?: string; kind?: string; spec: AccessPointSpec }

const base = '/api/v1/access-points'
const ifMatch = (revision: number) => ({ 'If-Match': `"${revision}"` })

export async function listAccessPoints(): Promise<AccessPointRecord[]> {
  const records = await authenticatedRequest<AccessPointRecord[] | null>(`${base}/`)
  if (records == null) return []
  if (!Array.isArray(records)) throw new Error('Invalid access point list response')
  // A caller without an operator role gets reduced views (no spec); those are not records.
  return records.filter((r) => !!r && typeof r === 'object' && !!(r as Partial<AccessPointRecord>).spec)
}
/**
 * What `GET /api/v1/access-points` returns to a caller who is not an operator:
 * only the access points that admit them, as `{id, display_name, endpoints}`
 * with server-built endpoint URLs and no configuration.
 */
export interface AccessPointView { id: string; display_name?: string; endpoints: { kind: string; url: string; label?: string }[] }

/** The caller's access points in whichever shape the server returned: full records for operators, views for everyone else. */
export async function listAccessPointViews(): Promise<(AccessPointRecord | AccessPointView)[]> {
  const records = await authenticatedRequest<(AccessPointRecord | AccessPointView)[] | null>(`${base}/`)
  if (records == null) return []
  if (!Array.isArray(records)) throw new Error('Invalid access point list response')
  return records
}
export const isFullAccessPoint = (r: AccessPointRecord | AccessPointView): r is AccessPointRecord => 'spec' in r && !!(r as AccessPointRecord).spec
export const accessPointView = (r: AccessPointRecord | AccessPointView): AccessPointView =>
  isFullAccessPoint(r) ? { id: r.id, display_name: r.spec.display_name, endpoints: [] } : { id: r.id, display_name: r.display_name, endpoints: r.endpoints ?? [] }

export const getAccessPoint = (id: string) => authenticatedRequest<AccessPointRecord>(`${base}/${encodeURIComponent(id)}`)
/** revision 0 creates; any other value must be the current revision (409 otherwise). */
export const putAccessPoint = (spec: AccessPointSpec, revision: number) =>
  authenticatedRequest<AccessPointRecord>(`${base}/${encodeURIComponent(spec.id)}`, {
    method: 'PUT', body: JSON.stringify(spec), headers: revision ? ifMatch(revision) : undefined,
  })
export const deleteAccessPoint = (id: string, revision: number) =>
  authenticatedRequest<void>(`${base}/${encodeURIComponent(id)}`, { method: 'DELETE', headers: ifMatch(revision) })

// ---- quota plan ----------------------------------------------------------

export interface QuotaPlan {
  access_point_id: string
  tenant_id: string
  applied: boolean
  fragment: { meters?: Record<string, unknown>; rates?: unknown[]; concurrency?: unknown[]; budget_refs?: unknown[] }
  scheduler?: { class: string; flow_id: string; weight: number; routes: unknown[] }
  policy?: unknown
  validation: { valid: boolean; validator: string; reference?: unknown; error?: string }
  warnings: string[]
}
/** Renders a QuotaPlex policy fragment for review. Applies nothing. */
export const getQuotaPlan = (id: string) =>
  authenticatedRequest<QuotaPlan>(`${base}/${encodeURIComponent(id)}/quota-plan`, { method: 'POST' })

// ---- routing state (operators) and cache purge (admins) -----------------

/** One recent upstream outcome of a backend (routing.RecentOutcome). */
export interface RoutingOutcome { at?: string; outcome?: string; upstream_status?: number }
/** A backend's circuit breaker in the answering API process (routing.BreakerState). */
export interface RoutingBreaker { state?: string; consecutive_failures?: number; opened_at?: string; half_open_at?: string; recent?: RoutingOutcome[] }
/** One backend of a routed model, in routing order (RoutedBackendView). */
export interface RoutedBackend { surface_ref?: string; native_model?: string; weight?: number; priority?: number; breaker?: RoutingBreaker }
/** One routed model of an access point (RoutedModelView). */
export interface RoutedModel { name?: string; strategy?: string; backends?: RoutedBackend[] }
export interface ResponseCacheStats { entries?: number; bytes?: number; max_bytes?: number }
/** `GET /access-points/{id}/routing` (AccessPointRoutingView). State is per API process. */
export interface AccessPointRouting { access_point_id?: string; process_local?: boolean; models?: RoutedModel[]; cache_store?: ResponseCacheStats }
/** `POST /access-points/{id}/cache/purge` (AccessPointCachePurge handler). */
export interface AccessPointCachePurge { access_point_id?: string; model?: string; purged?: number; process_local?: boolean }

export const getAccessPointRouting = (id: string) => authenticatedRequest<AccessPointRouting>(`${base}/${encodeURIComponent(id)}/routing`)
/** Removes this API process's cached answers of the access point (admins). Nothing is removed on other replicas. */
export const purgeAccessPointCache = (id: string) =>
  authenticatedRequest<AccessPointCachePurge>(`${base}/${encodeURIComponent(id)}/cache/purge`, { method: 'POST' })

// ---- reference pickers ---------------------------------------------------

export interface ModelSurfaceRecord {
  id: string
  revision: number
  spec: { surface: { id: string; protocol: string; models?: string[]; publisher?: string; location?: string; provider?: { id: string; [k: string]: unknown } } }
}
export interface RegistrationRecord { id: string; revision: number; spec: { id: string; profile_id?: string; mode?: string; protocol?: string } }

async function listArray<T>(path: string): Promise<T[]> {
  const result = await authenticatedRequest<T[] | null>(path)
  if (result == null) return []
  if (!Array.isArray(result)) throw new Error(`Invalid list response from ${path}`)
  return result
}
export const listModelSurfaces = () => listArray<ModelSurfaceRecord>('/api/v1/llm/surfaces/')
export const listClientRegistrations = () => listArray<RegistrationRecord>('/api/v1/clients/registrations')
export { listToolConnections, listAgents }
export type { ToolConnectionRecord, Agent }

// ---- helpers (pure) ------------------------------------------------------

export function emptyAccessPoint(): AccessPointSpec {
  return { id: '', caller_kinds: ['user'], subjects: {}, models: [], tools: [], skills: [] }
}

export function isRevisionConflict(error: unknown): boolean {
  const e = error as { status?: number; code?: string } | null
  return !!e && (e.status === 409 || e.code === 'REVISION_CONFLICT')
}
export function errorStatus(error: unknown): number | undefined {
  const s = (error as { status?: unknown } | null)?.status
  return typeof s === 'number' ? s : undefined
}

export interface Wire {
  id: string; label: string; path: string
  /** Model-surface protocols that serve this wire natively. */
  surfaceProtocols: readonly string[]
  /** Further endpoints of the same protocol (streaming variants). */
  extra?: { label: string; path: string }[]
  /** WebSocket session endpoint: metered as one request, usage advisory. */
  websocket?: boolean
}
/** The images and audio wires are served by any of these surface protocols (accessPointMediaWires). */
const MEDIA_SURFACE_PROTOCOLS = ['openai-chat', 'openai-responses', 'openai-images', 'openai-audio'] as const
const GEMINI_LIVE_PATH = '/ws/google.cloud.aiplatform.v1.LlmBidiService/BidiGenerateContent'
/**
 * Client wire protocols and the model-surface protocol that serves each
 * natively. Mirrors accessPointWires in internal/api/access_point_models.go (plus accessPointMediaWires
 * in client_gateway_media.go and the utility wires in client_gateway_utility.go);
 * a surface protocol listed here is one the server accepts at save time.
 */
export const WIRES: readonly Wire[] = [
  { id: 'openai-chat-completions', label: 'OpenAI Chat Completions', path: '/v1/chat/completions', surfaceProtocols: ['openai-chat'] },
  { id: 'openai-responses', label: 'OpenAI Responses', path: '/v1/responses', surfaceProtocols: ['openai-responses'] },
  { id: 'anthropic-messages', label: 'Anthropic Messages', path: '/v1/messages', surfaceProtocols: ['anthropic-messages'], extra: [{ label: 'Count tokens', path: '/v1/messages/count_tokens' }] },
  { id: 'google-genai', label: 'Google GenAI', path: '/v1beta/models/{model}:{op}', surfaceProtocols: ['google-genai'], extra: [{ label: 'Cached contents (Developer API)', path: '/v1beta/cachedContents' }, { label: 'Interactions (Developer API)', path: '/v1beta/interactions' }] },
  { id: 'bedrock-converse', label: 'Amazon Bedrock Converse', path: '/model/{model}/converse', surfaceProtocols: ['bedrock-converse'], extra: [{ label: 'Streaming', path: '/model/{model}/converse-stream' }] },
  { id: 'bedrock-invoke', label: 'Amazon Bedrock InvokeModel', path: '/model/{model}/invoke', surfaceProtocols: ['bedrock-invoke'], extra: [{ label: 'Streaming', path: '/model/{model}/invoke-with-response-stream' }] },
  { id: 'openai-realtime', label: 'OpenAI Realtime (WebSocket)', path: '/v1/realtime?model={model}', surfaceProtocols: ['openai-realtime'], websocket: true },
  { id: 'google-live', label: 'Gemini Live (WebSocket)', path: `${GEMINI_LIVE_PATH}?model={model}`, surfaceProtocols: ['google-genai'], websocket: true },
  { id: 'openai-embeddings', label: 'OpenAI Embeddings', path: '/v1/embeddings', surfaceProtocols: ['openai-embeddings'] },
  { id: 'openai-moderations', label: 'OpenAI Moderations', path: '/v1/moderations', surfaceProtocols: ['openai-moderations'] },
  { id: 'openai-images', label: 'OpenAI Images', path: '/v1/images/generations', surfaceProtocols: MEDIA_SURFACE_PROTOCOLS, extra: [{ label: 'Image edits', path: '/v1/images/edits' }] },
  { id: 'openai-audio', label: 'OpenAI Audio', path: '/v1/audio/transcriptions', surfaceProtocols: MEDIA_SURFACE_PROTOCOLS, extra: [{ label: 'Translations', path: '/v1/audio/translations' }, { label: 'Speech', path: '/v1/audio/speech' }] },
]
/** Client wires that reach a surface speaking this protocol. */
export const wiresForProtocol = (protocol: string) => WIRES.filter(w => w.surfaceProtocols.includes(protocol))

type SurfaceInfo = ModelSurfaceRecord['spec']['surface']
/** Provider name shown for a surface: its explicit provider, else Google Cloud. */
export const surfaceProviderName = (s: SurfaceInfo) => s.provider?.id ?? 'google-cloud'
/** Why an access point cannot route this surface, or undefined when it can. */
export function surfaceUnroutableReason(s: SurfaceInfo): string | undefined {
  if (WIRES.some(w => w.surfaceProtocols.includes(s.protocol))) return undefined
  return `access points have no client protocol for ${s.protocol}`
}
export const surfaceOptionLabel = (s: SurfaceInfo) => {
  const reason = surfaceUnroutableReason(s)
  return `${s.id} · ${surfaceProviderName(s)} · ${s.protocol}${reason ? ` (unavailable: ${reason})` : ''}`
}

export interface EndpointGroup { id: string; title: string; note?: string; urls: { label: string; url: string }[] }

/** A save error: the server's message and, when it names one, the field path from `details.field`. */
export interface FieldIssue { message: string; field?: string }

/** Reads `details.field` (a slash-separated path such as `models/1/surface_ref`) from a failed save. */
export function fieldIssue(error: unknown): FieldIssue {
  const e = error as { message?: unknown; details?: { field?: unknown } } | null
  const field = e?.details?.field
  return { message: error instanceof Error ? error.message : typeof e?.message === 'string' ? e.message : 'Could not save the access point.', ...(typeof field === 'string' && field ? { field } : {}) }
}

/** `models/1/surface_ref` becomes section "models", index 1, leaf "surface_ref". */
export function parseFieldPath(field: string | undefined): { section: string; index?: number; leaf?: string } | undefined {
  if (!field) return undefined
  const [section, ...rest] = field.split('/')
  if (!section) return undefined
  const index = rest.length && /^\d+$/.test(rest[0]) ? Number(rest[0]) : undefined
  const tail = index === undefined ? rest : rest.slice(1)
  return { section, ...(index !== undefined ? { index } : {}), ...(tail.length ? { leaf: tail.join('/') } : {}) }
}

export function parseList(text: string): string[] {
  return text.split(/[\n,]/).map(s => s.trim()).filter(Boolean)
}

/**
 * Endpoint groups from the server-built connect response. The server lists
 * only the wires the access point really serves: the OpenAI, Anthropic and
 * GenAI families come as a base URL (a wire's own path is appended to it) and
 * every other endpoint (Bedrock, WebSocket sessions, Azure OpenAI shapes, ...)
 * comes in `endpoints` and is shown exactly as given. Nothing is derived from
 * this browser's address.
 */
export function endpointGroupsFromConnect(spec: AccessPointSpec, c: ConnectInfo): EndpointGroup[] {
  const groups: EndpointGroup[] = []
  const reach = new Set((c.models ?? []).flatMap(m => m.protocols ?? []))
  for (const w of WIRES.filter(w => reach.has(w.id))) {
    const family = w.id.startsWith('openai-') && !w.websocket ? 'openai' : w.id === 'anthropic-messages' ? 'anthropic' : w.id === 'google-genai' ? 'genai' : ''
    const baseUrl = family ? c.base_urls?.[family] : undefined
    if (!baseUrl) continue
    const at = (path: string) => baseUrl + (family === 'openai' ? path.replace(/^\/v1/, '') : path)
    groups.push({ id: w.id, title: w.label, urls: [{ label: 'Endpoint', url: at(w.path) }, ...(w.extra ?? []).map(e => ({ label: e.label, url: at(e.path) }))] })
  }
  const byProtocol = new Map<string, ConnectEndpoint[]>()
  for (const e of c.endpoints ?? []) byProtocol.set(e.protocol, [...(byProtocol.get(e.protocol) ?? []), e])
  for (const [protocol, list] of byProtocol) {
    groups.push({
      id: protocol, title: WIRES.find(w => w.id === protocol)?.label ?? list[0].label ?? protocol.replace(/[-_]/g, ' '),
      urls: list.map(e => ({
        label: e.label ?? (e.transport === 'websocket' ? 'Endpoint' : e.transport),
        // A WebSocket connection needs its query parameters (the model selector).
        url: e.url + (e.query?.length ? '?' + e.query.map(q => `${q}={${q}}`).join('&') : ''),
      })),
    })
  }
  if (c.base_urls?.openai && (reach.has('openai-chat-completions') || reach.has('openai-responses'))) {
    groups.push({ id: 'models', title: 'Model listing', urls: [{ label: 'List models', url: `${c.base_urls.openai}/models` }] })
  }
  if (c.mcp_url) {
    groups.push({
      id: 'mcp', title: 'MCP tools (Streamable HTTP)',
      urls: [
        { label: 'All tools, named {connection}__{tool}', url: c.mcp_url },
        ...(spec.tools ?? []).map(t => ({ label: `Connection ${t.connection_ref}`, url: `${c.mcp_url}/${t.connection_ref}` })),
        ...(c.mcp_catalog_url ? [{ label: 'MCP registry catalog', url: c.mcp_catalog_url }] : []),
      ],
    })
  }
  if (c.skills_url) groups.push({ id: 'skills', title: 'Skills', urls: [{ label: 'Skills manifest', url: c.skills_url }] })
  return groups
}
