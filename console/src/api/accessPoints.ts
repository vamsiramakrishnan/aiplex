import { authenticatedRequest, listAgents, type Agent } from './client'
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
  spec: { surface: { id: string; protocol: string; models?: string[]; publisher?: string; location?: string } }
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

export interface Wire { id: string; label: string; path: string; surfaceProtocol: string }
/** Client wire protocols and the model-surface protocol that serves each natively. */
export const WIRES: readonly Wire[] = [
  { id: 'openai-chat-completions', label: 'OpenAI Chat Completions', path: '/v1/chat/completions', surfaceProtocol: 'openai-chat' },
  { id: 'openai-responses', label: 'OpenAI Responses', path: '/v1/responses', surfaceProtocol: 'openai-responses' },
  { id: 'anthropic-messages', label: 'Anthropic Messages', path: '/v1/messages', surfaceProtocol: 'anthropic-messages' },
  { id: 'google-genai', label: 'Google GenAI', path: '/v1beta/models/{model}:{op}', surfaceProtocol: 'google-genai' },
]

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
    const wires = unknown ? WIRES : WIRES.filter(w => served.has(w.surfaceProtocol))
    for (const w of wires) {
      groups.push({
        id: w.id, title: w.label,
        note: unknown ? 'Could not determine which models speak this protocol. A request through the wrong protocol returns an error naming the protocols that serve the model.' : undefined,
        urls: [{ label: 'Endpoint', url: root + w.path }],
      })
    }
    if (wires.length) groups.push({ id: 'models', title: 'Model listing', urls: [{ label: 'List models', url: `${root}/v1/models` }] })
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
