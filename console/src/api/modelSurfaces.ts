import { authenticatedRequest } from './client'

// Model surfaces for non-Google providers, their capacity state and the
// batches created through the gateway. Contract:
// pkg/modelapi/providers.go (spec + descriptor), internal/api/model_providers.go
// (capacity), internal/api/model_batches_list.go (batches).
//
// Credentials in a surface are REFERENCES. Nothing in this module reads, holds
// or sends a secret value.

export const PROVIDER_IDS = ['openai', 'anthropic', 'azure-openai', 'bedrock', 'openai-compatible', 'gemini-developer'] as const
export type ProviderId = (typeof PROVIDER_IDS)[number]

export const PROVIDER_LABELS: Record<ProviderId, string> = {
  openai: 'OpenAI', anthropic: 'Anthropic', 'azure-openai': 'Azure OpenAI', bedrock: 'Amazon Bedrock', 'openai-compatible': 'OpenAI-compatible server', 'gemini-developer': 'Gemini Developer API',
}

/** Example origin shown as a placeholder; the origin is always typed by the operator. */
export const ORIGIN_HINTS: Record<ProviderId, string> = {
  openai: 'https://api.openai.com',
  anthropic: 'https://api.anthropic.com',
  'azure-openai': 'https://<resource>.openai.azure.com',
  bedrock: 'https://bedrock-runtime.<region>.amazonaws.com',
  'openai-compatible': 'https://models.internal.example',
  'gemini-developer': 'https://generativelanguage.googleapis.com',
}

export interface ModelOperation {
  id: string
  method: string
  path: string
  transport: string
  action: string
  resource_kind?: string
  stateful?: boolean
  source_url: string
}
export interface ProviderDescriptor {
  id: string
  protocols: string[]
  credential_schemes: string[]
  api_key_header?: string
  operations: ModelOperation[]
  default_stream_usage?: string
}

export interface ProviderBlock {
  id: string
  base_path?: string
  api_version?: string
  region?: string
  organization?: string
  project?: string
  anthropic_version?: string
  anthropic_betas?: string[]
  stream_usage?: string
  limit_header_passthrough?: boolean
  usage_semantics?: string
  [unknown: string]: unknown
}
export interface WebSocketBlock { metering?: 'session' | 'token' | 'seconds'; max_session_seconds?: number; token_allowance?: number; [unknown: string]: unknown }
export interface CapacityPolicyBlock {
  cooldown_enabled?: boolean
  max_cooldown_seconds?: number
  headroom_fraction?: number
  min_weight?: number
  default_weight?: number
  signal_max_age_seconds?: number
  [unknown: string]: unknown
}
/** A credential reference. `secret_ref` names a stored secret; it is never a value. */
export interface AuthBlock { type: string; in?: string; name?: string; prefix?: string; secret_ref?: string; [unknown: string]: unknown }
export interface SurfaceBlock {
  id: string
  protocol: string
  origin: string
  models?: string[]
  operations?: ModelOperation[]
  provider?: ProviderBlock
  websocket?: WebSocketBlock
  [unknown: string]: unknown
}
export interface SurfaceSpec {
  surface: SurfaceBlock
  identity_ref?: string
  network_profile_ref?: string
  auth?: AuthBlock
  capacity_policy?: CapacityPolicyBlock
  [unknown: string]: unknown
}
export interface SurfaceRecord { id: string; revision: number; spec: SurfaceSpec }

const ifMatch = (revision: number) => ({ 'If-Match': `"${revision}"` })
const surfaces = '/api/v1/llm/surfaces'

export const getProviderDescriptor = (provider: string) => authenticatedRequest<ProviderDescriptor>(`/api/v1/llm/operations?provider=${encodeURIComponent(provider)}`)

export async function listSurfaces(): Promise<SurfaceRecord[]> {
  const records = await authenticatedRequest<SurfaceRecord[] | null>(`${surfaces}/`)
  if (records == null) return []
  if (!Array.isArray(records)) throw new Error('Invalid model surface list response')
  return records
}
export const getSurface = (id: string) => authenticatedRequest<SurfaceRecord>(`${surfaces}/${encodeURIComponent(id)}`)
/** Revision 0 creates; any other value must be the current revision (409 otherwise). */
export const putSurface = (spec: SurfaceSpec, revision: number) =>
  authenticatedRequest<SurfaceRecord>(`${surfaces}/${encodeURIComponent(spec.surface.id)}`, { method: 'PUT', body: JSON.stringify(spec), headers: revision ? ifMatch(revision) : { 'If-Match': '"0"' } })
export const deleteSurface = (id: string, revision: number) =>
  authenticatedRequest<void>(`${surfaces}/${encodeURIComponent(id)}`, { method: 'DELETE', headers: ifMatch(revision) })

// ---- capacity ------------------------------------------------------------

/** A limit window. A member that is absent was not reported by the provider: unknown, not zero. */
export interface LimitWindow { limit?: number; remaining?: number; reset_at?: string }
export interface ProviderLimits {
  provider: string
  observed_at: string
  http_status?: number
  classification?: string
  retry_after_ms?: number
  requests?: LimitWindow
  tokens?: LimitWindow
  input_tokens?: LimitWindow
  output_tokens?: LimitWindow
}
export type CooldownPhase = 'none' | 'cooling' | 'half_open' | 'probing'
export interface SurfaceCapacity {
  surface_id: string
  observed: boolean
  scope: string
  limits?: ProviderLimits
  capacity_policy: {
    enabled: boolean
    policy?: CapacityPolicyBlock
    cooldown?: { phase: CooldownPhase | string; until?: string; retry_after_ms?: number }
  }
}
export const getSurfaceCapacity = (id: string) => authenticatedRequest<SurfaceCapacity>(`${surfaces}/${encodeURIComponent(id)}/capacity`)

// ---- batches -------------------------------------------------------------

export interface ModelBatch {
  id: string
  surface_id: string
  provider: string
  batch_id: string
  attempt_id?: string
  model?: string
  create_operation: string
  state: 'open' | 'settled'
  provider_status?: string
  ended: boolean
  /** The provider's item count; null while unknown (never zero). */
  expected: number | null
  /** When the exchange that created the batch finished; absent on records that predate it. */
  created_at?: string
  /** When the results were fetched and the batch settled; absent while open. */
  settled_at?: string
}
export interface BatchPage { batches: ModelBatch[]; next_cursor?: string }
export function listBatches(params: { state?: 'open' | 'settled'; surface_id?: string; cursor?: string; limit?: number; order?: 'id' | 'newest'; older_than?: string } = {}) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') q.set(k, String(v))
  const qs = q.toString()
  return authenticatedRequest<BatchPage>(`/api/v1/llm/batches${qs ? `?${qs}` : ''}`)
}

// ---- pure form logic -----------------------------------------------------

/** Live voice is billed by session seconds; the server rejects token metering for it. */
export const LIVE_VOICE_OPERATION = 'openai.v1.live'
export const WS_LIMITS = { sessionSeconds: [60, 86400], tokenAllowance: [1024, 4194304], cooldownSeconds: [1, 3600], signalAgeSeconds: [1, 3600], weight: 1000 } as const

/** Which provider-block fields apply to which provider (mirrors Surface.validateProvider). */
export const PROVIDER_FIELDS: Record<ProviderId, readonly string[]> = {
  openai: ['organization', 'project', 'stream_usage'],
  anthropic: ['anthropic_version', 'anthropic_betas'],
  'azure-openai': ['api_version', 'stream_usage'],
  bedrock: ['region'],
  'openai-compatible': ['base_path', 'stream_usage', 'usage_semantics'],
  'gemini-developer': [],
}

export type AuthMode = 'secret' | 'identity' | 'none'
/** Credential choices per provider. Identity references name an operator identity profile. */
export function authModes(provider: ProviderId, descriptor: ProviderDescriptor | undefined): AuthMode[] {
  const modes: AuthMode[] = ['secret']
  if (provider === 'openai' || provider === 'openai-compatible' || provider === 'azure-openai' || provider === 'bedrock') modes.push('identity')
  if (descriptor?.credential_schemes.includes('none')) modes.push('none')
  return modes
}
export const IDENTITY_HINT: Partial<Record<ProviderId, string>> = {
  'azure-openai': 'an Entra identity profile (type azure-default)',
  bedrock: 'an AWS identity profile (type aws-default), signed with SigV4',
  openai: 'an operator bearer identity profile (type bearer-env)',
  'openai-compatible': 'an operator bearer identity profile (type bearer-env)',
}

export interface SurfaceDraft {
  id: string
  provider: ProviderId
  protocol: string
  origin: string
  models: string
  operationIds: string[]
  networkProfileRef: string
  base_path: string
  api_version: string
  region: string
  organization: string
  project: string
  anthropic_version: string
  anthropic_betas: string
  stream_usage: string
  limit_header_passthrough: boolean
  usage_semantics: string
  authMode: AuthMode
  secretRef: string
  identityRef: string
  wsMetering: '' | 'session' | 'token' | 'seconds'
  wsMaxSessionSeconds: string
  wsTokenAllowance: string
  capacityEnabled: boolean
  cooldownEnabled: boolean
  maxCooldownSeconds: string
  headroomFraction: string
  minWeight: string
  defaultWeight: string
  signalMaxAgeSeconds: string
}

export function blankDraft(provider: ProviderId = 'openai'): SurfaceDraft {
  return {
    id: '', provider, protocol: '', origin: '', models: '', operationIds: [], networkProfileRef: '',
    base_path: '', api_version: '', region: '', organization: '', project: '', anthropic_version: provider === 'anthropic' ? '2023-06-01' : '', anthropic_betas: '', stream_usage: '', limit_header_passthrough: false, usage_semantics: '',
    authMode: 'secret', secretRef: '', identityRef: '',
    wsMetering: '', wsMaxSessionSeconds: '', wsTokenAllowance: '',
    capacityEnabled: false, cooldownEnabled: false, maxCooldownSeconds: '', headroomFraction: '', minWeight: '', defaultWeight: '', signalMaxAgeSeconds: '',
  }
}

const text = (v: unknown) => (v === undefined || v === null ? '' : String(v))

export function draftFromSpec(spec: SurfaceSpec): SurfaceDraft {
  const s = spec.surface
  const provider = (PROVIDER_IDS as readonly string[]).includes(s.provider?.id ?? '') ? (s.provider!.id as ProviderId) : 'openai'
  const p = s.provider ?? { id: provider }
  const ws = s.websocket, cp = spec.capacity_policy
  return {
    ...blankDraft(provider),
    id: s.id, protocol: s.protocol, origin: s.origin, models: (s.models ?? []).join('\n'), operationIds: (s.operations ?? []).map(o => o.id), networkProfileRef: spec.network_profile_ref ?? '',
    base_path: text(p.base_path), api_version: text(p.api_version), region: text(p.region), organization: text(p.organization), project: text(p.project), anthropic_version: text(p.anthropic_version), anthropic_betas: (p.anthropic_betas ?? []).join('\n'),
    stream_usage: text(p.stream_usage), limit_header_passthrough: p.limit_header_passthrough === true, usage_semantics: text(p.usage_semantics),
    authMode: spec.identity_ref ? 'identity' : spec.auth?.type === 'none' ? 'none' : 'secret', secretRef: text(spec.auth?.secret_ref), identityRef: text(spec.identity_ref),
    wsMetering: ws?.metering ?? '', wsMaxSessionSeconds: text(ws?.max_session_seconds), wsTokenAllowance: text(ws?.token_allowance),
    capacityEnabled: !!cp, cooldownEnabled: cp?.cooldown_enabled === true, maxCooldownSeconds: text(cp?.max_cooldown_seconds), headroomFraction: text(cp?.headroom_fraction), minWeight: text(cp?.min_weight), defaultWeight: text(cp?.default_weight), signalMaxAgeSeconds: text(cp?.signal_max_age_seconds),
  }
}

export const splitList = (value: string) => value.split(/[\n,]/).map(v => v.trim()).filter(Boolean)
/** Operations of a selection that are WebSocket sessions. */
export const websocketOperations = (descriptor: ProviderDescriptor | undefined, ids: readonly string[]) =>
  (descriptor?.operations ?? []).filter(o => o.transport === 'websocket' && ids.includes(o.id))
/** Whether the WebSocket block applies to the selected operations. */
export const hasWebSocketOperation = (descriptor: ProviderDescriptor | undefined, ids: readonly string[]) => websocketOperations(descriptor, ids).length > 0
/** Token metering is rejected by the server for Live voice. Other cases are left to the server's validation error. */
export const tokenMeteringBlocked = (ids: readonly string[]) => ids.includes(LIVE_VOICE_OPERATION)
/** Seconds metering reserves and settles a Live voice session's length; the server accepts it only on a surface with that operation. */
export const secondsMeteringAvailable = (ids: readonly string[]) => ids.includes(LIVE_VOICE_OPERATION)
export const azureDeploymentSelected = (ids: readonly string[]) => ids.some(id => id.startsWith('azure.deployments.'))

const whole = (v: string) => /^\d+$/.test(v.trim()) && Number.isSafeInteger(Number(v))
const inRange = (v: string, [lo, hi]: readonly [number, number] | readonly number[]) => whole(v) && Number(v) >= lo && Number(v) <= hi

/**
 * Builds the spec to save. `base` is the saved spec being edited: its fields
 * the form does not model (including unknown ones) are carried over unchanged.
 * Returns every problem found; the server remains the authority and its error
 * is shown for anything this cannot decide.
 */
export function specFromDraft(d: SurfaceDraft, descriptor: ProviderDescriptor, base?: SurfaceSpec): { spec?: SurfaceSpec; errors: string[] } {
  const errors: string[] = []
  const fields = PROVIDER_FIELDS[d.provider]
  const models = splitList(d.models)
  if (!d.id.trim()) errors.push('Surface ID is required.')
  if (!descriptor.protocols.includes(d.protocol)) errors.push('Choose a protocol served by this provider.')
  if (!d.origin.trim()) errors.push('Origin is required (scheme and host only, for example ' + ORIGIN_HINTS[d.provider] + ').')
  if (models.length === 0) errors.push('List at least one allowed model.')
  if (d.operationIds.length === 0) errors.push('Select at least one operation.')
  if (d.provider === 'anthropic' && !/^\d{4}-\d{2}-\d{2}$/.test(d.anthropic_version.trim())) errors.push('Anthropic requires a pinned version such as 2023-06-01.')
  if (d.provider === 'bedrock' && !d.region.trim()) errors.push('Bedrock requires the signing region, for example us-east-1.')
  if (d.provider === 'azure-openai' && azureDeploymentSelected(d.operationIds) && !d.api_version.trim()) errors.push('Deployment-scoped Azure operations require a pinned API version.')
  if (d.authMode === 'secret' && !d.secretRef.trim()) errors.push('Enter the name of the secret reference.')
  if (d.authMode === 'identity' && !d.identityRef.trim()) errors.push('Enter the name of the identity reference.')

  const ws = hasWebSocketOperation(descriptor, d.operationIds)
  if (ws) {
    if (d.wsMetering === 'token' && tokenMeteringBlocked(d.operationIds)) errors.push('Token metering is not available for Live voice sessions (billed by session seconds); use session or seconds metering.')
    if (d.wsMetering === 'seconds' && !secondsMeteringAvailable(d.operationIds)) errors.push('Seconds metering is for Live voice sessions only: select the Live voice operation or choose another metering.')
    if (d.wsMetering === 'seconds' && !d.wsMaxSessionSeconds.trim()) errors.push('Seconds metering needs a maximum session length: it is what each session reserves when it starts.')
    if (d.wsMaxSessionSeconds.trim() && !inRange(d.wsMaxSessionSeconds, WS_LIMITS.sessionSeconds)) errors.push(`Maximum session length must be ${WS_LIMITS.sessionSeconds[0]} to ${WS_LIMITS.sessionSeconds[1]} seconds.`)
    if (d.wsTokenAllowance.trim() && d.wsMetering !== 'token') errors.push('A token allowance requires token metering.')
    if (d.wsTokenAllowance.trim() && !inRange(d.wsTokenAllowance, WS_LIMITS.tokenAllowance)) errors.push(`Token allowance must be ${WS_LIMITS.tokenAllowance[0]} to ${WS_LIMITS.tokenAllowance[1]}.`)
  }

  let capacity: CapacityPolicyBlock | undefined
  if (d.capacityEnabled) {
    capacity = { ...(base?.capacity_policy ?? {}) }
    const headroom = d.headroomFraction.trim()
    const h = headroom === '' ? 0 : Number(headroom)
    if (headroom !== '' && !(Number.isFinite(h) && h >= 0 && h <= 1)) errors.push('Headroom fraction must be between 0 and 1.')
    if (!d.cooldownEnabled && !(h > 0)) errors.push('Turn on cooldown or set a headroom fraction; a policy with neither does nothing.')
    if (d.maxCooldownSeconds.trim() && !d.cooldownEnabled) errors.push('Maximum cooldown requires cooldown to be on.')
    if (d.maxCooldownSeconds.trim() && !inRange(d.maxCooldownSeconds, WS_LIMITS.cooldownSeconds)) errors.push('Maximum cooldown must be 1 to 3600 seconds.')
    if (d.signalMaxAgeSeconds.trim() && !inRange(d.signalMaxAgeSeconds, WS_LIMITS.signalAgeSeconds)) errors.push('Signal maximum age must be 1 to 3600 seconds.')
    if (h > 0 && !(whole(d.minWeight) && Number(d.minWeight) >= 1)) errors.push('Headroom needs a minimum weight of at least 1.')
    if (!(h > 0) && (d.minWeight.trim() || d.defaultWeight.trim())) errors.push('Minimum and default weight apply only with a headroom fraction.')
    for (const [label, v] of [['Minimum weight', d.minWeight], ['Default weight', d.defaultWeight]] as const) if (v.trim() && !(whole(v) && Number(v) <= WS_LIMITS.weight)) errors.push(`${label} must be a whole number up to ${WS_LIMITS.weight}.`)
    set(capacity, 'cooldown_enabled', d.cooldownEnabled || undefined)
    set(capacity, 'max_cooldown_seconds', d.cooldownEnabled && d.maxCooldownSeconds.trim() ? Number(d.maxCooldownSeconds) : undefined)
    set(capacity, 'headroom_fraction', h > 0 ? h : undefined)
    set(capacity, 'min_weight', h > 0 && d.minWeight.trim() ? Number(d.minWeight) : undefined)
    set(capacity, 'default_weight', h > 0 && d.defaultWeight.trim() ? Number(d.defaultWeight) : undefined)
    set(capacity, 'signal_max_age_seconds', d.signalMaxAgeSeconds.trim() ? Number(d.signalMaxAgeSeconds) : undefined)
  }
  if (errors.length) return { errors }

  // Operations are copied from the provider's catalog exactly: the server only accepts catalog operations.
  const catalog = new Map(descriptor.operations.map(o => [o.id, o]))
  const saved = new Map((base?.surface.operations ?? []).map(o => [o.id, o]))
  const operations = d.operationIds.map(id => catalog.get(id) ?? saved.get(id)).filter((o): o is ModelOperation => !!o)

  const provider: ProviderBlock = { ...(base?.surface.provider ?? {}), id: d.provider }
  const fieldValue: Record<string, unknown> = {
    base_path: d.base_path.trim() || undefined, api_version: d.api_version.trim() || undefined, region: d.region.trim() || undefined,
    organization: d.organization.trim() || undefined, project: d.project.trim() || undefined, anthropic_version: d.anthropic_version.trim() || undefined,
    anthropic_betas: splitList(d.anthropic_betas).length ? splitList(d.anthropic_betas) : undefined, stream_usage: d.stream_usage || undefined, usage_semantics: d.usage_semantics || undefined,
  }
  for (const name of ['base_path', 'api_version', 'region', 'organization', 'project', 'anthropic_version', 'anthropic_betas', 'stream_usage', 'usage_semantics']) {
    // The pinned API version applies only to deployment-scoped Azure operations.
    const applies = fields.includes(name) && (name !== 'api_version' || azureDeploymentSelected(d.operationIds))
    set(provider, name, applies ? fieldValue[name] : undefined)
  }
  set(provider, 'limit_header_passthrough', d.limit_header_passthrough || undefined)

  const surface: SurfaceBlock = { ...(base?.surface ?? {}), id: d.id.trim(), protocol: d.protocol, origin: d.origin.trim(), models, operations, provider }
  let websocket: WebSocketBlock | undefined
  if (ws && (d.wsMetering || d.wsMaxSessionSeconds.trim() || d.wsTokenAllowance.trim())) {
    websocket = { ...(base?.surface.websocket ?? {}) }
    set(websocket, 'metering', d.wsMetering || undefined)
    set(websocket, 'max_session_seconds', d.wsMaxSessionSeconds.trim() ? Number(d.wsMaxSessionSeconds) : undefined)
    set(websocket, 'token_allowance', d.wsMetering === 'token' && d.wsTokenAllowance.trim() ? Number(d.wsTokenAllowance) : undefined)
  }
  set(surface, 'websocket', websocket)

  const spec: SurfaceSpec = { ...(base ?? {}), surface }
  set(spec, 'network_profile_ref', d.networkProfileRef.trim() || undefined)
  set(spec, 'capacity_policy', capacity)
  // Exactly one credential reference. Switching away from a mode drops the other mode's reference.
  if (d.authMode === 'identity') { spec.identity_ref = d.identityRef.trim(); delete spec.auth }
  else {
    delete spec.identity_ref
    if (d.authMode === 'none') spec.auth = { ...(base?.auth ?? {}), type: 'none' }
    else {
      const header = descriptor.credential_schemes.includes('api-key') && descriptor.api_key_header
      const prior = { ...(base?.auth ?? {}) }
      spec.auth = header ? { ...prior, type: 'api_key', in: 'header', name: descriptor.api_key_header, secret_ref: d.secretRef.trim() } : { ...prior, type: 'bearer', secret_ref: d.secretRef.trim() }
      if (!header) { delete spec.auth.in; delete spec.auth.name; delete spec.auth.prefix }
    }
  }
  return { spec, errors }
}

function set(target: object, key: string, value: unknown) {
  const t = target as Record<string, unknown>
  if (value === undefined) delete t[key]
  else t[key] = value
}

// ---- capacity / limits formatting ---------------------------------------

export const UNKNOWN = 'unknown'
/** An absent value is unknown, never zero. */
export const limitNumber = (v: number | undefined) => (v === undefined || v === null ? UNKNOWN : v.toLocaleString('en-US'))
export function describeWindow(w: LimitWindow | undefined): string {
  if (!w || (w.limit === undefined && w.remaining === undefined && !w.reset_at)) return 'not reported'
  return `${limitNumber(w.remaining)} remaining of ${limitNumber(w.limit)}${w.reset_at ? `, resets ${w.reset_at}` : ''}`
}
export const PHASE_TEXT: Record<string, string> = {
  none: 'No cooldown: requests are being sent to the provider.',
  cooling: 'Cooling down: new requests are rejected until the provider says it can take traffic.',
  half_open: 'Cooldown elapsed: the next request is a probe.',
  probing: 'Probing: one request is in flight to check whether the provider has recovered.',
}
