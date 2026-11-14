import { authenticatedRequest } from './client'

// Request-hook policies (docs/request-hooks.md). Operators only on the server, reads
// included. A record is { id, revision, spec } as stored; the spec is the policy document
// the server validates, so the Console treats every field of it as possibly absent.
const base = '/api/v1/hooks/policies'

export interface HookPolicyRecord {
  id: string
  /** Absent means unknown. Never treated as 0. */
  revision?: number
  spec?: unknown
}

export type HookPhase = 'request' | 'response'
export type HookPlane = 'llmplex' | 'mcplex'

export interface HookRuleCount { id: string; count?: number }
export interface HookResult { id?: string; type?: string; phase?: string; decision?: string; rules?: HookRuleCount[] | null; duration_ms?: number; fail_open_used?: boolean }

/** What the dry run decided. `texts` is the output after transforms and is absent when refused. */
export interface HookTestResult {
  policy?: string
  phase?: string
  decision?: string
  code?: string
  message?: string
  texts?: string[] | null
  results?: HookResult[] | null
  uninspected_parts?: number
}

const ifMatch = (revision: number) => ({ 'If-Match': String(revision) })

export async function listHookPolicies(): Promise<HookPolicyRecord[]> {
  const rows = await authenticatedRequest<HookPolicyRecord[] | null>(`${base}/`)
  // The server always sends an array, empty when there are no policies (pkg/resources/store.go List,
  // internal/registry/module_resources.go List). null or a missing list is not an answer, so it is an error.
  if (!Array.isArray(rows)) throw new Error('The server sent no list of hook policies')
  return rows
}

/**
 * Create (revision 0) or replace (the revision the page read). `document` is the policy JSON
 * exactly as the person wrote it, so the server sees unknown fields and refuses them.
 */
export const putHookPolicy = (id: string, revision: number, document: string) =>
  authenticatedRequest<HookPolicyRecord>(`${base}/${encodeURIComponent(id)}`, { method: 'PUT', headers: ifMatch(revision), body: document })

export const deleteHookPolicy = (id: string, revision: number) =>
  authenticatedRequest<void>(`${base}/${encodeURIComponent(id)}`, { method: 'DELETE', headers: ifMatch(revision) })

/**
 * Dry run. The sample goes only to this route; the server stores and logs nothing. Callers must
 * not keep it: the screen holds it in component state and the mutation's own state only.
 */
export const testHookPolicy = (id: string, sample: { texts: string[]; phase: HookPhase; plane: HookPlane }) =>
  authenticatedRequest<HookTestResult>(`${base}/${encodeURIComponent(id)}/test`, { method: 'POST', body: JSON.stringify(sample) })

/** The server's own words for a refused document, with the field it names when it names one. */
export function validationMessage(error: unknown): string {
  const e = error as { message?: unknown; details?: { field?: unknown } } | null
  const message = typeof e?.message === 'string' && e.message ? e.message : 'The server refused the document.'
  const field = e?.details && typeof e.details.field === 'string' ? e.details.field : ''
  return field ? `${message} (field: ${field})` : message
}
