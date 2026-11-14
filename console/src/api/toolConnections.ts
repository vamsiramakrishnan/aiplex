import { authenticatedRequest } from './client'
import {
  CONNECTION_AUTH,
  buildAuthBlock,
  checkSecretReference,
  isUrlField,
  looksLikeSecretValue,
  notValidJson,
  validateServerUrl,
  type AuthValues,
  type ToolAuthCatalog,
  type ToolConnectionRecord,
  type ToolConnectionSpec,
  type ToolPolicy,
} from './toolAuth'

// Creating, deleting and testing MCP tool connections, and their policy rules.
// Shapes mirror sdk/aiplex (tool_connection_auth.go, tool_rules.go, resources.go)
// and cmd/aiplex-cli/cmd_mcp.go. The server validates and evaluates everything
// that matters (rule expressions, secret references, auth alignment): nothing
// here evaluates an expression, and a server message is passed on unchanged.

const base = '/api/v1/mcp/connections'
const path = (id: string) => `${base}/${encodeURIComponent(id)}`

export const errorStatus = (e: unknown): number | undefined => {
  const s = (e as { status?: unknown } | null)?.status
  return typeof s === 'number' ? s : undefined
}
export const errorCode = (e: unknown): string | undefined => {
  const c = (e as { code?: unknown } | null)?.code
  return typeof c === 'string' || typeof c === 'number' ? String(c) : undefined
}
/** The server's message, unchanged. Use errorWithCode where the code belongs beside it. */
export const errorMessage = (e: unknown): string => (e instanceof Error ? e.message : String(e))
export const errorWithCode = (e: unknown): string => { const c = errorCode(e); return c ? `${errorMessage(e)} (code ${c})` : errorMessage(e) }

/** A revision the server can check a write against: positive and finite. Anything else is unknown. */
export const knownRevision = (r: unknown): r is number => typeof r === 'number' && Number.isFinite(r) && r > 0

// Validation ---------------------------------------------------------------

/** Mirrors what the SDK refuses before sending (aiplex.ValidateToolConnectionID): a safe, trimmed, non-empty path segment. */
export function validateConnectionId(id: string): string | undefined {
  if (id === '') return 'Required'
  if (id.trim() !== id) return 'Must not start or end with whitespace'
  if (id.length > 512) return 'At most 512 characters'
  if (id === '.' || id === '..' || /[/\\?#%\u0000-\u001f]/.test(id)) return "Must not contain '/', '\\', '?', '#', '%', control characters, or be '.' or '..'"
  return undefined
}

export { validateServerUrl }

// Spec building --------------------------------------------------------------

export const TRANSPORT_MODES = { 'streamable-http': 'stateless-streamable-http', json: 'stateless-json' } as const
export type TransportMode = keyof typeof TRANSPORT_MODES

/** The auth types the guided form offers (those `aiplex mcp connections add` offers). Others are pasted as a full specification. */
export const FORM_AUTH_TYPES = ['none', 'api_key', 'bearer', 'basic', 'oauth_client_credentials']

export interface NewConnectionInput {
  id: string
  url: string
  mode: TransportMode
  protocolVersion: string
  authType: string
  values: AuthValues
  tools: string[]
}

/** The spec `aiplex mcp connections add` writes for the same flags. */
export function buildConnectionSpec(catalog: ToolAuthCatalog, input: NewConnectionInput): ToolConnectionSpec {
  const type = catalog.types.find((t) => t.type === input.authType)
  if (!type) throw new Error(`Unknown auth type ${input.authType}`)
  const url = input.url.trim()
  return {
    id: input.id,
    resource_url: url,
    execution: {
      id: input.id, endpoint: url, credential_mode: CONNECTION_AUTH, allowed_tools: input.tools,
      protocol_version: input.protocolVersion.trim(), mode: TRANSPORT_MODES[input.mode],
    },
    auth: buildAuthBlock(type, input.values),
  }
}

// Keys that name a credential value rather than a reference to one.
const CREDENTIAL_KEY = /^(secret|password|passwd|token|access_token|refresh_token|id_token|api_key|apikey|client_secret|bearer|authorization|private_key|credential|credentials)$/i

function scanForValues(node: unknown, where: string, out: string[]): void {
  if (Array.isArray(node)) { node.forEach((v, i) => scanForValues(v, `${where}[${i}]`, out)); return }
  if (node && typeof node === 'object') {
    for (const [k, v] of Object.entries(node as Record<string, unknown>)) {
      // A name with the shape of a credential is never echoed back.
      if (looksLikeSecretValue(k)) { out.push(`${where}: a member name has the shape of a credential. Credentials are secret references, never values`); continue }
      if (CREDENTIAL_KEY.test(k)) out.push(`${where}.${k}: credentials are secret references (a field ending in _ref), never values`)
      scanForValues(v, `${where}.${k}`, out)
    }
    return
  }
  if (typeof node === 'string') {
    if (looksLikeSecretValue(node)) out.push(`${where}: this value has the shape of a credential. Credentials are secret references, never values`)
    else if (isUrlField('', node)) { const p = validateServerUrl(node); if (p) out.push(`${where}: ${p}`) }
  }
}

export type SpecParse = { ok: true; spec: ToolConnectionSpec } | { ok: false; errors: string[] }

/**
 * Parses a pasted full specification and refuses anything that could carry a
 * secret value: unknown members of the auth block, a reference field that is not
 * shaped like a reference, credential-named members, token-shaped strings, and a
 * URL with credentials. The server still validates the rest.
 */
export function parsePastedSpec(text: string, catalog: ToolAuthCatalog): SpecParse {
  let spec: unknown
  try { spec = JSON.parse(text) } catch { return { ok: false, errors: [notValidJson(text)] } }
  if (!spec || typeof spec !== 'object' || Array.isArray(spec)) return { ok: false, errors: ['The specification must be a JSON object'] }
  const s = spec as ToolConnectionSpec
  const errors: string[] = []
  const idProblem = typeof s.id === 'string' ? validateConnectionId(s.id) : 'Required'
  if (idProblem) errors.push(`id: ${idProblem}`)
  for (const [where, value] of [['resource_url', s.resource_url], ['execution.endpoint', (s.execution as { endpoint?: unknown } | undefined)?.endpoint]] as const) {
    if (typeof value === 'string' && value !== '') { const p = validateServerUrl(value); if (p) errors.push(`${where}: ${p}`) }
  }
  if (s.auth !== undefined) {
    const auth = s.auth as Record<string, unknown>
    const type = auth && typeof auth === 'object' && !Array.isArray(auth) ? catalog.types.find((t) => t.type === auth.type) : undefined
    if (!type) errors.push('auth.type: not one of the auth types the gateway offers')
    else {
      const known = new Map(type.fields.map((f) => [f.name, f]))
      for (const [k, v] of Object.entries(auth)) {
        if (k === 'type') continue
        const field = known.get(k)
        if (!field) { errors.push(looksLikeSecretValue(k) ? 'auth: a member name has the shape of a credential' : `auth.${k}: not a field of ${type.type}`); continue }
        if (!field.secret_reference && typeof v === 'string' && isUrlField(k, v)) { const p = validateServerUrl(v); if (p) errors.push(`auth.${k}: ${p}`) }
        if (field.secret_reference) {
          const refusal = typeof v === 'string' ? checkSecretReference(catalog, v) : 'must be a string'
          if (refusal) errors.push(`auth.${k}: ${refusal}`)
        }
      }
    }
  }
  scanForValues(s, 'spec', errors)
  return errors.length ? { ok: false, errors: [...new Set(errors)] } : { ok: true, spec: s }
}

// Calls ----------------------------------------------------------------------

/**
 * Creates a connection: refuses an id that exists (a replace is a different
 * act, done by the auth editor with the revision it read), then writes with
 * If-Match 0, which the server accepts only for a new id.
 */
export async function createToolConnection(id: string, spec: ToolConnectionSpec): Promise<ToolConnectionRecord> {
  if (spec.id !== id) throw new Error('The specification id does not match the connection id')
  const problem = validateConnectionId(id)
  if (problem) throw new Error(`Invalid connection id: ${problem}`)
  try {
    const existing = await authenticatedRequest<ToolConnectionRecord>(path(id))
    throw Object.assign(new Error(`Tool connection ${id} already exists (revision ${existing?.revision ?? 'unknown'}). Edit it instead.`), { status: 409, code: 'ALREADY_EXISTS' })
  } catch (e) {
    if (errorStatus(e) !== 404) throw e
  }
  return authenticatedRequest<ToolConnectionRecord>(path(id), { method: 'PUT', headers: { 'If-Match': '0' }, body: JSON.stringify(spec) })
}

export function deleteToolConnection(id: string, revision: number): Promise<void> {
  if (!knownRevision(revision)) throw new Error('A positive revision is required to delete a connection')
  return authenticatedRequest<void>(path(id), { method: 'DELETE', headers: { 'If-Match': String(revision) } })
}

export function isAlreadyExists(e: unknown): boolean {
  return (e as { code?: string } | null)?.code === 'ALREADY_EXISTS'
}

export interface ConnectionTool {
  name: string
  title?: string
  description?: string
  annotations?: { readOnlyHint?: boolean } & Record<string, unknown>
}

export interface ToolListing {
  tools: ConnectionTool[]
  /** True when the server still had more pages after the page limit: the list is not complete. */
  truncated: boolean
}

/** A JSON-RPC error from the tool server (or the gateway on its behalf) answering tools/list. */
export class ToolListFailure extends Error {
  code?: number
  constructor(message: string, code?: number) { super(message); this.name = 'ToolListFailure'; this.code = code }
}

const MAX_PAGES = 20

/** tools/list through the gateway (POST .../invoke), the call `aiplex mcp connections test` makes, following cursors. */
export async function listConnectionTools(id: string): Promise<ToolListing> {
  const tools: ConnectionTool[] = []
  let cursor = ''
  for (let page = 0; page < MAX_PAGES; page++) {
    const request: Record<string, unknown> = { jsonrpc: '2.0', id: page + 1, method: 'tools/list' }
    if (cursor) request.params = { cursor }
    const out = await authenticatedRequest<{ result?: { tools?: unknown; nextCursor?: unknown }; error?: { message?: string; code?: number } } | null>(
      `${path(id)}/invoke`, { method: 'POST', body: JSON.stringify(request), plainTextErrors: true })
    if (out?.error) throw new ToolListFailure(typeof out.error.message === 'string' && out.error.message ? out.error.message : 'The tool server returned an error without a message', out.error.code)
    if (!out?.result) throw new Error('tools/list returned no result')
    const list = out.result.tools
    if (!Array.isArray(list)) throw new Error("The server's answer had no tool list")
    for (const t of list as ConnectionTool[]) {
      if (!t || typeof t.name !== 'string') throw new Error('tools/list returned a tool without a name')
      tools.push(t)
    }
    cursor = typeof out.result.nextCursor === 'string' ? out.result.nextCursor : ''
    if (!cursor) return { tools, truncated: false }
  }
  return { tools, truncated: true }
}

// Policy rules -----------------------------------------------------------------

export interface ToolRule extends Record<string, unknown> {
  id: string
  when: string
  effect: string
  message?: string
  description?: string
}

/** The rules as stored. A missing list and a null one are both "no rules". */
export function rulesOf(spec: ToolConnectionSpec): ToolRule[] {
  const rules = (spec.tools as ToolPolicy | null | undefined)?.rules
  return Array.isArray(rules) ? (rules as ToolRule[]) : []
}

/**
 * The spec with its rules replaced and everything else kept, including members
 * of `tools` this client does not know. An empty list removes `rules`, and an
 * empty `tools` block is removed (as the CLI does).
 */
export function withRules(spec: ToolConnectionSpec, rules: Record<string, unknown>[]): ToolConnectionSpec {
  const next: ToolConnectionSpec = { ...spec }
  const tools: Record<string, unknown> = { ...((spec.tools as Record<string, unknown> | null | undefined) ?? {}) }
  if (rules.length) tools.rules = rules
  else delete tools.rules
  if (Object.keys(tools).length) next.tools = tools as ToolPolicy
  else delete next.tools
  return next
}

/** Writes new rules with the revision the page read. The server compiles every expression and refuses a bad one; its message is the error. */
export function putRules(record: ToolConnectionRecord, rules: Record<string, unknown>[]): Promise<ToolConnectionRecord> {
  return authenticatedRequest<ToolConnectionRecord>(path(record.id), {
    method: 'PUT', headers: { 'If-Match': String(record.revision) }, body: JSON.stringify(withRules(record.spec, rules)),
  })
}

/** The rules from a pasted list, or an object with a "rules" list (as `rules set -f`). Only the shape is checked here. */
export function parseRulesInput(text: string): { ok: true; rules: Record<string, unknown>[] } | { ok: false; error: string } {
  let v: unknown
  try { v = JSON.parse(text) } catch { return { ok: false, error: notValidJson(text) } }
  const list = Array.isArray(v) ? v : v && typeof v === 'object' && Array.isArray((v as { rules?: unknown }).rules) ? (v as { rules: unknown[] }).rules : null
  if (!list) return { ok: false, error: 'Expected a list of rules, or an object with a "rules" list' }
  const bad = list.findIndex((r) => !r || typeof r !== 'object' || Array.isArray(r))
  if (bad >= 0) return { ok: false, error: `Rule ${bad + 1} is not an object` }
  return { ok: true, rules: list as Record<string, unknown>[] }
}

// Dry run ------------------------------------------------------------------------

export interface RulesTestSample {
  tool: string
  args?: unknown
  subject: { kind?: string; principal?: string; groups?: string[]; groups_known?: boolean; agent?: string; client?: string }
  access_point?: string
  annotations?: { read_only?: boolean; destructive?: boolean; idempotent?: boolean; open_world?: boolean }
  /** Replaces the saved rules for this test only. */
  rules?: Record<string, unknown>[]
  time?: string
}

export interface RulesTrace { set: string; id: string; effect: string; result: string; error?: string }
export interface RulesTestResult {
  connection: string
  tool: string
  decision: string
  error_class?: string
  reason?: string
  rule?: string
  message?: string
  trace?: RulesTrace[] | null
  list_hidden?: boolean
  note?: string
}

/** POST .../rules/test: operators only. The server evaluates; nothing is stored. */
export async function testToolRules(id: string, sample: RulesTestSample): Promise<RulesTestResult> {
  const out = await authenticatedRequest<RulesTestResult>(`${path(id)}/rules/test`, { method: 'POST', body: JSON.stringify(sample) })
  if (!out || typeof out.decision !== 'string') throw new Error('Invalid rules test response')
  return out
}
