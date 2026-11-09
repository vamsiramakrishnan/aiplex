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

function secretPattern(catalog: ToolAuthCatalog): RegExp | null {
  try { return new RegExp(catalog.secret_references.pattern) } catch { return null }
}

// Returns field name -> message. Server-side validation remains authoritative.
export function validateAuthValues(catalog: ToolAuthCatalog, type: ToolAuthType, values: AuthValues): Record<string, string> {
  const errors: Record<string, string> = {}
  const pattern = secretPattern(catalog)
  for (const field of type.fields) {
    const raw = (values[field.name] ?? '').trim()
    if (!raw) {
      if (field.required) errors[field.name] = 'Required'
      continue
    }
    if (field.secret_reference && pattern && !pattern.test(raw)) {
      errors[field.name] = 'Enter the name of a stored secret (a reference), not the secret value'
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
    if (next.execution) {
      next.execution = {
        ...next.execution,
        credential_mode: type.type === 'oauth_user' ? DELEGATED_OAUTH : CONNECTION_AUTH,
        identity_ref: '',
      }
    }
  }
  const policy: ToolPolicy = {}
  const allow = splitList(selection.allow, /[\n,]+/)
  const deny = splitList(selection.deny, /[\n,]+/)
  if (allow.length) policy.allow = allow
  if (deny.length) policy.deny = deny
  if (selection.readOnly) policy.read_only = true
  if (Object.keys(policy).length) next.tools = policy
  else delete next.tools
  return next
}
