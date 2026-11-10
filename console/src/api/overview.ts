import { authenticatedRequest } from './client'
import { parseUsageJSON, type Count } from './usage'

// Single-pane overview API. Contract: internal/api/overview*.go.
// Counters decode exactly (huge int64 values arrive as strings).

export type SubjectKind = 'agent' | 'user' | 'client'
export const SUBJECT_KINDS: readonly SubjectKind[] = ['agent', 'user', 'client']

export interface OverviewCost { currency: string; basis: 'settled' | 'estimated' | string; amount: string; scale: number }
/** Absent token counters are unknown, not zero; tokens_unknown counts records lacking them. */
export interface OverviewUsage {
  requests: Count
  records: Count
  errors: Count
  denied: Count
  tokens: Record<string, Count> | null
  tokens_unknown: Record<string, Count> | null
  cost: OverviewCost[] | null
  unknown_cost_records: Count
  /** Records whose usage_bound is set; counted in `tokens` but not final. Absent when zero. */
  bounded_usage_records?: Count
  lower_bound_records?: Count
  ambiguous_usage_records?: Count
  partial_usage_records?: Count
}
export interface OverviewUsageGroup { key: Record<string, string>; last_seen?: string | null; usage: OverviewUsage | null }

export interface OverviewSubject {
  kind: SubjectKind
  id: string
  display_name?: string
  identity: { source: string; detail: string }
  access_points: string[] | null
  last_seen: string | null
  usage: OverviewUsage | null
  known_from: string[] | null
}
export interface SubjectPage {
  kind: SubjectKind
  since: string
  subjects: OverviewSubject[]
  next_cursor?: string
  usage_available: boolean
  usage_truncated: boolean
  derived_from: string[] | null
  limitations: string[] | null
}

export type AccessState = 'granted' | 'not_granted' | 'unknown'
export interface OverviewReason { type: string; ref?: string; detail?: string; conditional?: boolean }
export interface OverviewAccessItem {
  kind: 'model' | 'tool' | 'skill'
  name: string
  access_point_id: string
  resource?: string
  required_scope?: string
  state: AccessState
  reasons: OverviewReason[] | null
}
export interface QuotaCoverage { state: 'not_required' | 'covered' | 'uncovered' | 'unknown'; detail: string }
export interface OverviewAccessPointQuota {
  access_point_id: string
  qos_class?: string
  coverage: QuotaCoverage
  allocations: { budgets: { id?: string }[] | null; rates: { id?: string }[] | null; concurrency: { id?: string }[] | null } | null
}
export interface OverviewBalance { available: boolean; reason?: string }
export interface OverviewAdmission { id: string; display_name?: string; admits: 'yes' | 'unknown' | 'no'; reason: string }

export interface SubjectDetail {
  kind: SubjectKind
  id: string
  display_name?: string
  identity: { source: string; detail: string }
  aliases?: string[]
  identity_bindings?: { id: string; preset: string; issuer: string; subject: string; subject_claim: string }[]
  user_identity_providers?: { id: string; display_name?: string; issuer: string; disabled: boolean }[]
  access_points: OverviewAdmission[] | null
  access: { models: OverviewAccessItem[] | null; tools: OverviewAccessItem[] | null; skills: OverviewAccessItem[] | null; scopes_known: boolean; scopes_note: string; truncated: boolean }
  quota: { access_points: OverviewAccessPointQuota[] | null; balance: OverviewBalance }
  since: string
  last_seen: string | null
  usage: OverviewUsage | null
  usage_by_model: OverviewUsageGroup[] | null
  usage_by_tool: OverviewUsageGroup[] | null
  notes: string[] | null
  known_from: string[] | null
}

export interface OverviewResourceModel { name: string; native_model: string; model_id?: string; surface_ref: string; publisher?: string; surface_protocol?: string; wire_protocols: string[] | null; resolved: boolean }
export interface AccessPointOverview {
  access_point: { id: string; display_name?: string; caller_kinds: string[]; [k: string]: unknown }
  revision: number
  since: string
  callers: { caller_kinds: string[]; open_to_all_of_kind: string[] | null; note?: string; users: OverviewCaller[] | null; agents: OverviewCaller[] | null; clients: OverviewCaller[] | null; groups: { source: string; id: string }[] | null }
  resources: { models: OverviewResourceModel[] | null; tools: { connection_ref: string; tools: string[] | null; resolved: boolean }[] | null; skills: { instance_ref: string; skills: string[] | null; resolved: boolean }[] | null }
  qos: unknown
  quota: OverviewAccessPointQuota
  balance: OverviewBalance
  usage: { available: boolean; truncated: boolean; total: OverviewUsage | null; by_caller_kind: OverviewUsageGroup[] | null; by_model: OverviewUsageGroup[] | null; by_tool: OverviewUsageGroup[] | null }
  notes: string[] | null
}
export interface OverviewCaller { id: string; display_name?: string; declared: boolean; observed: boolean; last_seen: string | null; usage: OverviewUsage | null }

/** A Bedrock or WebSocket endpoint; "{model}" in `url` is a model name. WebSocket urls use ws/wss and need the `query` parameters. */
export interface ConnectEndpoint { protocol: string; transport: 'http' | 'http-stream' | 'websocket' | string; url: string; label?: string; query?: string[]; models: string[] | null }
export interface ConnectSnippet { title: string; language: string; content: string }
export interface ConnectInfo {
  access_point_id: string
  client: string
  base_urls: Record<string, string> | null
  /** Absent on servers that predate it. */
  endpoints?: ConnectEndpoint[] | null
  models: { name: string; protocols: string[] | null }[] | null
  mcp_url?: string
  mcp_catalog_url?: string
  skills_url?: string
  auth: { scheme: string; header: string; token_env: string; caller_kinds: string[] | null; authorization_servers?: string[]; resource_metadata_url?: string; steps: string[] | null }
  snippets: ConnectSnippet[] | null
  notes: string[] | null
}
export interface ClientProfile { id: string; name: string; surface: string; modes: string[]; protocols: string[]; sources?: string[] }

const base = '/api/v1/overview'
const get = <T>(path: string) => authenticatedRequest<T>(`${base}${path}`, undefined, async (r) => parseUsageJSON(await r.text()) as T)

export const SINCE_PRESETS = [
  { id: '24h', label: 'Last 24 hours' },
  { id: '7d', label: 'Last 7 days' },
  { id: '30d', label: 'Last 30 days' },
] as const
export type SincePreset = (typeof SINCE_PRESETS)[number]['id']

export function listSubjects(kind: SubjectKind, since: string, cursor = '', limit = 50) {
  const q = new URLSearchParams({ kind, since, limit: String(limit) })
  if (cursor) q.set('cursor', cursor)
  return get<SubjectPage>(`/subjects?${q}`)
}
/** Always uses ?id= so ids containing "/" work. */
export const getSubject = (kind: SubjectKind, id: string, since: string) =>
  get<SubjectDetail>(`/subjects/${kind}?${new URLSearchParams({ id, since })}`)
export interface AccessPointListRow {
  id: string
  display_name?: string
  caller_kinds: string[] | null
  qos_class?: string
  counts: { models: number; tools: number; skills: number }
  quota_coverage: QuotaCoverage
}
export interface AccessPointListPage { access_points: AccessPointListRow[] | null; next_cursor?: string; skipped_invalid?: number }
/** One page of the cheap access point list (no ledger scan). */
export function listOverviewAccessPoints(cursor = '', limit = 200) {
  const q = new URLSearchParams({ limit: String(limit) })
  if (cursor) q.set('cursor', cursor)
  return get<AccessPointListPage>(`/access-points?${q}`)
}
/** Every row of the list, following next_cursor (bounded to 10 pages). */
export async function listAllOverviewAccessPoints(): Promise<AccessPointListRow[]> {
  const rows: AccessPointListRow[] = []
  let cursor = ''
  for (let page = 0; page < 10; page++) {
    const p = await listOverviewAccessPoints(cursor)
    rows.push(...(p.access_points ?? []))
    if (!p.next_cursor) break
    cursor = p.next_cursor
  }
  return rows
}
export const getAccessPointOverview = (id: string, since: string) =>
  get<AccessPointOverview>(`/access-points/${encodeURIComponent(id)}?${new URLSearchParams({ since })}`)
export const getConnect = (id: string, client: string) =>
  get<ConnectInfo>(`/access-points/${encodeURIComponent(id)}/connect?${new URLSearchParams({ client })}`)
export async function listClientProfiles(): Promise<ClientProfile[]> {
  return (await authenticatedRequest<ClientProfile[] | null>('/api/v1/clients/profiles')) ?? []
}

export function connectErrorMessage(error: unknown): string {
  const e = error as { status?: number; code?: string; message?: string } | null
  const msg = e?.message ?? 'Unexpected error'
  switch (e?.status) {
    case 422: return `This client cannot be connected to this access point: ${msg}`
    case 404: return `Not found: ${msg}`
    case 409: return e.code === 'PUBLIC_URL_REQUIRED' ? `The server has no public URL configured, so connection details cannot be built: ${msg}` : msg
    case 403: return 'You do not have access to connection details for this access point.'
    default: return msg
  }
}

/** Order in which the Overview looks for the first caller kind that has rows. */
export const DEFAULT_TAB_ORDER: readonly SubjectKind[] = ['user', 'agent', 'client']

/**
 * The tab the Overview opens on: the first of Users, Agents, Clients with at
 * least one row. `counts` holds the row count per kind, or undefined while that
 * kind is still loading. Returns null while the answer could still change (a
 * higher-priority kind is loading and nothing before it has rows), so the page
 * can wait instead of flashing an empty tab. When every kind has loaded and
 * none has rows, Users.
 */
export function firstTabWithRows(counts: Partial<Record<SubjectKind, number | undefined>>): SubjectKind | null {
  for (const kind of DEFAULT_TAB_ORDER) {
    const n = counts[kind]
    if (n === undefined) return null
    if (n > 0) return kind
  }
  return 'user'
}
