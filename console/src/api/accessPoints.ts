import { authenticatedRequest, listAgents, type Agent } from './client'
import type { ConnectInfo } from './overview'
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
export interface AccessPointQoS { class: string; weight?: number; limits?: AccessPointLimits; budget_refs?: string[] }

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
  return records
}
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
const wireUrls = (w: Wire, root: string) => {
  const ws = (u: string) => (w.websocket ? u.replace(/^http/, 'ws') : u)
  return [{ label: 'Endpoint', url: ws(root + w.path) }, ...(w.extra ?? []).map(e => ({ label: e.label, url: ws(root + e.path) }))]
}

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

/**
 * Endpoint URLs for an access point, grouped by protocol. Model protocols are
 * limited to those a configured model's surface speaks natively (there is no
 * cross-protocol translation). When a model's surface protocol is not known
 * (surface list unavailable or surface missing), every model protocol is
 * shown with a note.
 */
export function endpointGroups(spec: AccessPointSpec, origin: string, surfaces: ModelSurfaceRecord[] | undefined): EndpointGroup[] {
  const root = `${origin.replace(/\/$/, '')}/ap/${spec.id}`
  const groups: EndpointGroup[] = []
  const models = spec.models ?? []
  if (models.length) {
    const protocols = surfaces ? models.map(m => surfaces.find(s => s.id === m.surface_ref)?.spec.surface.protocol) : undefined
    const unknown = !protocols || protocols.some(p => !p)
    const served = new Set(protocols)
    const wires = unknown ? WIRES : WIRES.filter(w => w.surfaceProtocols.some(p => served.has(p)))
    for (const w of wires) {
      groups.push({
        id: w.id, title: w.label,
        note: unknown ? 'Could not determine which models speak this protocol. A request through the wrong protocol returns an error naming the protocols that serve the model.' : undefined,
        urls: wireUrls(w, root),
      })
    }
    if (wires.some(w => !w.websocket && !w.id.startsWith('bedrock'))) groups.push({ id: 'models', title: 'Model listing', urls: [{ label: 'List models', url: `${root}/v1/models` }] })
  }
  if ((spec.tools ?? []).length) {
    groups.push({
      id: 'mcp', title: 'MCP tools (Streamable HTTP)',
      urls: [
        { label: 'All tools, named {connection}__{tool}', url: `${root}/mcp` },
        ...(spec.tools ?? []).map(t => ({ label: `Connection ${t.connection_ref}`, url: `${root}/mcp/${t.connection_ref}` })),
        { label: 'MCP registry catalog', url: `${root}/v0.1/servers` },
      ],
    })
  }
  if ((spec.skills ?? []).length) groups.push({ id: 'skills', title: 'Skills', urls: [{ label: 'Skills manifest', url: `${root}/skills` }] })
  return groups
}

const FIELD_HINTS: [RegExp, string][] = [
  [/at least one model, tool connection or skill/, 'resources'],
  [/caller_kinds/, 'caller_kinds'],
  [/subjects\.groups/, 'subjects.groups'],
  [/subjects\.principals/, 'subjects.principals'],
  [/subjects\.agents/, 'subjects.agents'],
  [/client registration|subjects\.client_registrations/, 'subjects.client_registrations'],
  [/display_name/, 'display_name'],
  [/access point id/, 'id'],
  [/model surface|model name|native model|models entries|models is bounded|\bmodel "/, 'models'],
  [/tools\[|tool connection|tools is bounded|cannot be namespaced/, 'tools'],
  [/skill/, 'skills'],
  [/qos\.budget_refs/, 'qos.budget_refs'],
  [/qos\.limits/, 'qos.limits'],
  [/qos\.weight/, 'qos.weight'],
  [/qos\.class/, 'qos.class'],
]
/**
 * Maps a server validation message to the form field it names. The server
 * returns one message string, not a field map, so this keys off the field
 * names the messages quote; anything unrecognised stays a form-level error.
 */
export function fieldForError(message: string): string | undefined {
  for (const [re, field] of FIELD_HINTS) if (re.test(message)) return field
  return undefined
}

export function parseList(text: string): string[] {
  return text.split(/[\n,]/).map(s => s.trim()).filter(Boolean)
}

/**
 * Endpoint groups from the server-built connect response (built from the
 * configured public URL, not from this browser's address).
 */
export function endpointGroupsFromConnect(spec: AccessPointSpec, c: ConnectInfo): EndpointGroup[] {
  const groups: EndpointGroup[] = []
  const reach = new Set((c.models ?? []).flatMap(m => m.protocols ?? []))
  // The connect response gives base URLs for the OpenAI, Anthropic and GenAI
  // families, and lists Bedrock and WebSocket endpoints in `endpoints`. Only
  // when a server predates `endpoints` are they built here from the server's
  // public URL (taken from the access point root it returned).
  const serverEndpoints = c.endpoints ?? undefined
  const root =[c.base_urls?.genai, c.base_urls?.anthropic, c.base_urls?.openai?.replace(/\/v1$/, ''), c.mcp_url?.replace(/\/mcp$/, '')].find(Boolean)
  for (const w of WIRES.filter(w => reach.has(w.id))) {
    const family = w.id.startsWith('openai-') && !w.websocket ? 'openai' : w.id === 'anthropic-messages' ? 'anthropic' : w.id === 'google-genai' ? 'genai' : ''
    if (family) {
      const baseUrl = c.base_urls?.[family]
      if (!baseUrl) continue
      const at = (path: string) => baseUrl + (family === 'openai' ? path.replace(/^\/v1/, '') : path)
      groups.push({ id: w.id, title: w.label, urls: [{ label: 'Endpoint', url: at(w.path) }, ...(w.extra ?? []).map(e => ({ label: e.label, url: at(e.path) }))] })
    } else if (serverEndpoints) {
      const mine = serverEndpoints.filter(e => e.protocol === w.id)
      if (mine.length) {
        groups.push({
          id: w.id, title: w.label,
          urls: mine.map(e => ({
            label: e.label ?? (e.transport === 'websocket' ? 'Endpoint' : e.transport),
            // A WebSocket connection needs its query parameters (the model selector).
            url: e.url + (e.query?.length ? '?' + e.query.map(q => `${q}={${q}}`).join('&') : ''),
          })),
        })
      }
    } else if (root) {
      groups.push({ id: w.id, title: w.label, note: 'Built by this console from the server\'s public URL; the connect response does not list this endpoint.', urls: wireUrls(w, root) })
    }
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
