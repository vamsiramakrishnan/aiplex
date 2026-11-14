import { authenticatedRequest } from './client'

// MCP tool connection auth types and tool policy. The auth-types catalog is
// the source of truth for types and fields; nothing here hard-codes them
// except the credential-mode alignment the gateway requires on save.

const base = '/api/v1/mcp'

export interface ToolAuthField {
  name: string
  type: string
  required?: boolean
  secret_reference?: boolean
  enum?: string[]
  description: string
}

export interface ToolAuthType {
  type: string
  title: string
  description: string
  execution_credential_mode: string
  fields: ToolAuthField[]
}

export interface ToolAuthCatalog {
  types: ToolAuthType[]
  default_type: string
  default_behaviour: string
  secret_references: { pattern: string; namespace?: string; note: string }
  tools_policy: ToolAuthField[]
}

export type ToolAuth = { type: string } & Record<string, unknown>

export interface ToolPolicy {
  allow?: string[]
  deny?: string[]
  read_only?: boolean
  // Policy rules are edited in the rules panel (toolConnections.ts). The auth
  // editor keeps them as they are when it saves the other members.
  rules?: Record<string, unknown>[]
}

export interface ToolConnectionSpec extends Record<string, unknown> {
  id: string
  resource_url?: string
  execution?: { credential_mode?: string; identity_ref?: string } & Record<string, unknown>
  auth?: ToolAuth
  tools?: ToolPolicy
}

export interface ToolConnectionRecord {
  id: string
  revision: number
  spec: ToolConnectionSpec
}

export const CONNECTION_AUTH = 'connection-auth'
export const DELEGATED_OAUTH = 'delegated-oauth'

export async function getToolAuthCatalog(): Promise<ToolAuthCatalog> {
  const catalog = await authenticatedRequest<ToolAuthCatalog>(`${base}/auth-types`)
  if (!catalog || !Array.isArray(catalog.types)) throw new Error('Invalid auth type catalog response')
  return catalog
}

export async function listToolConnections(): Promise<ToolConnectionRecord[]> {
  const records = await authenticatedRequest<ToolConnectionRecord[] | null>(`${base}/connections/`)
  if (records == null) return []
  if (!Array.isArray(records)) throw new Error('Invalid tool connection list response')
  return records
}

export async function getToolConnection(id: string): Promise<ToolConnectionRecord> {
  return authenticatedRequest<ToolConnectionRecord>(`${base}/connections/${encodeURIComponent(id)}`)
}

// Writes with If-Match so a concurrent change is rejected (409) instead of
// overwritten. Callers never retry a conflict with a newer revision on their
// own; the user reloads and reviews first.
export function putToolConnection(id: string, spec: ToolConnectionSpec, revision: number): Promise<ToolConnectionRecord> {
  return authenticatedRequest<ToolConnectionRecord>(`${base}/connections/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'If-Match': String(revision) },
    body: JSON.stringify(spec),
  })
}

export function isRevisionConflict(error: unknown): boolean {
  const e = error as { status?: number; code?: string } | null
  return !!e && (e.status === 409 || e.code === 'REVISION_CONFLICT')
}

export function hasManagedExecution(spec: ToolConnectionSpec): boolean {
  return !!spec.execution && Object.keys(spec.execution).length > 0
}

// Display helpers -----------------------------------------------------------

export function describeAuth(spec: ToolConnectionSpec, catalog?: ToolAuthCatalog): string {
  if (spec.auth?.type) return catalog?.types.find((t) => t.type === spec.auth?.type)?.title ?? spec.auth.type
  switch (spec.execution?.credential_mode) {
    case DELEGATED_OAUTH: return 'Default (delegated user OAuth)'
    case 'operator-bearer': return 'Default (operator identity)'
    default: return 'Default'
  }
}

export function describePolicy(policy?: ToolPolicy): string {
  const parts: string[] = []
  if (policy?.allow?.length) parts.push(`allow ${policy.allow.length}`)
  if (policy?.deny?.length) parts.push(`deny ${policy.deny.length}`)
  if (policy?.read_only) parts.push('read-only')
  return parts.length ? parts.join(' · ') : 'No restrictions'
}

// Form model ---------------------------------------------------------------

export type AuthValues = Record<string, string>

export function valuesFromAuth(type: ToolAuthType | undefined, auth: ToolAuth | undefined): AuthValues {
  const values: AuthValues = {}
  if (!type || !auth || auth.type !== type.type) return values
  for (const field of type.fields) {
    const raw = auth[field.name]
    if (Array.isArray(raw)) values[field.name] = raw.join(', ')
    else if (typeof raw === 'string') values[field.name] = raw
  }
  return values
}

export function splitList(text: string, separators: RegExp = /[\s,]+/): string[] {
  return text.split(separators).map((s) => s.trim()).filter(Boolean)
}

export function buildAuthBlock(type: ToolAuthType, values: AuthValues): ToolAuth {
  const auth: ToolAuth = { type: type.type }
  for (const field of type.fields) {
    const raw = (values[field.name] ?? '').trim()
    if (!raw) continue
    auth[field.name] = field.type.endsWith('[]') ? splitList(raw) : raw
  }
  return auth
}

// The reference shape the CLI checks (aiplex.ValidToolSecretRef): a name, never a
// value. The catalog's pattern is authoritative; this is its fallback.
const REFERENCE_SHAPE = /^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$/
export const NOT_A_REFERENCE = 'Enter the name of a stored secret (a reference), not the secret value'

function secretPattern(catalog: ToolAuthCatalog): RegExp {
  // An empty, missing or invalid catalog pattern falls back to the built-in shape: it never matches everything.
  const source = catalog.secret_references?.pattern
  if (typeof source !== 'string' || source.trim() === '') return REFERENCE_SHAPE
  try { return new RegExp(source) } catch { return REFERENCE_SHAPE }
}

// The one list of credential shapes, used for every reference field and for every
// key and string value of a pasted specification. Only high-precision shapes: a
// known token prefix or a JWT. A long name that fits the reference pattern is a
// legitimate reference and is never refused for its length or its characters.
const TOKEN_SHAPES: RegExp[] = [
  /^(sk|pk|rk)-[A-Za-z0-9_-]{20,}$/i,
  /^(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}/,
  /^github_pat_/,
  /^xox[abprs]-/,
  /^AKIA[0-9A-Z]{12,}/,
  /^AIza[0-9A-Za-z_-]{20,}/,
  /^ya29\./,
  /^eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+/,
]

// The word "bearer", white space, then at least 16 characters of the token alphabet, anywhere in the text.
const BEARER_TOKEN = /\bbearer\s+[A-Za-z0-9._~+/-]{16,}=*/i

/** True when text, or any word of it, has the shape of a known credential. */
export function looksLikeSecretValue(text: string): boolean {
  const t = text.trim()
  if (BEARER_TOKEN.test(t)) return true
  if (TOKEN_SHAPES.some((re) => re.test(t))) return true
  return t.split(/[\s"'=,;:]+/).some((w) => w !== '' && TOKEN_SHAPES.some((re) => re.test(w)))
}

export const LOOKS_LIKE_CREDENTIAL = 'This has the shape of a credential, not a reference name. Enter the name of a stored secret, not the secret value.'

/** Undefined when value is acceptable as a secret reference; otherwise the reason it is refused. Nothing is sent before this passes. */
export function checkSecretReference(catalog: ToolAuthCatalog, value: string): string | undefined {
  const v = value.trim()
  if (looksLikeSecretValue(v)) return LOOKS_LIKE_CREDENTIAL
  if (!secretPattern(catalog).test(v)) return NOT_A_REFERENCE
  return undefined
}

const URL_SECRET_KEY = /^(.*[-_])?(token|key|apikey|secret|password|passwd|sig|signature|auth|authorization|credential|credentials)$/i

/** An http(s) URL with no credentials in it. Credentials are secret references in the auth settings, never part of the URL. */
export function validateServerUrl(text: string): string | undefined {
  const raw = text.trim()
  if (!raw) return 'Required'
  let u: URL
  try { u = new URL(raw) } catch { return 'Not a URL' }
  if ((u.protocol !== 'https:' && u.protocol !== 'http:') || !u.hostname) return 'Must be an http or https URL'
  if (u.username || u.password) return 'A URL with a user name or password is refused: credentials are secret references in the auth settings'
  for (const key of u.searchParams.keys()) {
    if (URL_SECRET_KEY.test(key) || looksLikeSecretValue(key)) return 'A query parameter looks like a credential. Credentials are secret references in the auth settings, never part of the URL'
  }
  for (const value of u.searchParams.values()) {
    if (looksLikeSecretValue(value)) return 'A query value has the shape of a credential. Credentials are secret references, never part of the URL'
  }
  return undefined
}

/** A catalog field that holds a URL: by name, or because the value is written as an http(s) URL. */
export function isUrlField(name: string, value: string): boolean {
  return /(^|_)(url|uri|endpoint)$/.test(name) || /^https?:\/\//i.test(value.trim())
}

// Returns field name -> message. Server-side validation remains authoritative.
export function validateAuthValues(catalog: ToolAuthCatalog, type: ToolAuthType, values: AuthValues): Record<string, string> {
  const errors: Record<string, string> = {}
  for (const field of type.fields) {
    const raw = (values[field.name] ?? '').trim()
    if (!raw) {
      if (field.required) errors[field.name] = 'Required'
      continue
    }
    if (field.secret_reference) {
      const refusal = checkSecretReference(catalog, raw)
      if (refusal) errors[field.name] = refusal
    }
    if (!field.secret_reference && isUrlField(field.name, raw)) {
      const refusal = validateServerUrl(raw)
      if (refusal) errors[field.name] = refusal
    }
    if (field.enum && !field.enum.includes(raw)) errors[field.name] = `Must be one of: ${field.enum.join(', ')}`
  }
  return errors
}

export interface EditorSelection {
  // '' keeps the connection without an auth block.
  authType: string
  values: AuthValues
  allow: string
  deny: string
  readOnly: boolean
}

// Produces the full spec to PUT: everything the editor does not own is kept.
// Setting an auth block aligns execution.credential_mode the way the gateway
// requires (oauth_user -> delegated-oauth, every other type ->
// connection-auth with an empty identity_ref).
export function applySelection(spec: ToolConnectionSpec, catalog: ToolAuthCatalog, selection: EditorSelection): ToolConnectionSpec {
  const next: ToolConnectionSpec = { ...spec }
  if (selection.authType === '') {
    delete next.auth
  } else {
    const type = catalog.types.find((t) => t.type === selection.authType)
    if (!type) throw new Error(`Unknown auth type ${selection.authType}`)
    next.auth = buildAuthBlock(type, selection.values)
    // Members of the stored auth block this client does not know are kept while the type stays the same.
    if (spec.auth?.type === type.type) {
      const known = new Set(['type', ...type.fields.map((f) => f.name)])
      for (const [k, v] of Object.entries(spec.auth)) if (!known.has(k)) next.auth[k] = v
    }
    if (next.execution) {
      next.execution = {
        ...next.execution,
        credential_mode: type.type === 'oauth_user' ? DELEGATED_OAUTH : CONNECTION_AUTH,
        identity_ref: '',
      }
    }
  }
  // Start from the stored block so members this client does not know (and the rules) survive the edit.
  const policy: ToolPolicy = { ...((spec.tools as ToolPolicy | null | undefined) ?? {}) }
  delete policy.allow
  delete policy.deny
  delete policy.read_only
  const allow = splitList(selection.allow, /[\n,]+/)
  const deny = splitList(selection.deny, /[\n,]+/)
  if (allow.length) policy.allow = allow
  if (deny.length) policy.deny = deny
  if (selection.readOnly) policy.read_only = true
  if (!policy.rules?.length) delete policy.rules
  if (Object.keys(policy).length) next.tools = policy
  else delete next.tools
  return next
}

/** A fixed message for text that is not JSON, with line and column when the parser reports a position. It never quotes the input. */
export function notValidJson(text: string): string {
  try { JSON.parse(text); return 'Not valid JSON' } catch (e) {
    const m = /position (\d+)/.exec(e instanceof Error ? e.message : '')
    if (!m) return 'Not valid JSON'
    const upTo = text.slice(0, Number(m[1]))
    const line = upTo.split('\n').length
    const col = upTo.length - upTo.lastIndexOf('\n')
    return `Not valid JSON (line ${line}, column ${col})`
  }
}
