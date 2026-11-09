import { authenticatedRequest } from './client'

const base = '/api/v1/quota'
export interface QuotaPolicy { policy: { tenant_id: string; id: string; revision: number; budgets?: unknown[]; rates?: unknown[]; concurrency?: unknown[]; sharing?: { id: string }[] }; reference: { digest: string }; active: { digest: string }; as_of: string }
export interface Inventory { kind: string; records: { id: string; kind: string; value: unknown }[]; next_after_id?: string; done: boolean; as_of: string }
export interface Reconciliation { id: string; digest: string; attempt_id: string; reason: string; evidence_ref: string; independent_approval: boolean; expires_at: string; settlement: unknown }
export interface Inspection { plan: Reconciliation; receipt?: unknown }
function quotaRequest<T>(path: string, init?: RequestInit): Promise<T> { return authenticatedRequest<T>(path, init, async response => safeQuotaJSON(await response.text()) as T) }
export const quotaPolicy = () => quotaRequest<QuotaPolicy>(`${base}/policy`)
export const quotaInventory = (kind: string, after = '') => quotaRequest<Inventory>(`${base}/inventory?${new URLSearchParams({ kind, after, limit: '20' })}`)
export const quotaAttempt = (id: string) => quotaRequest<unknown>(`${base}/attempts/${encodeURIComponent(id)}`)
export const quotaReconciliation = (id: string) => quotaRequest<Inspection>(`${base}/reconciliations/${encodeURIComponent(id)}`)
export function quotaMutation<T>(path: string, body: unknown, key: string): Promise<T> {
  return quotaRequest<T>(`${base}${path}`, { method: 'POST', headers: { 'Idempotency-Key': key }, body: JSON.stringify(body) })
}
export const validateQuotaPolicy = (policy: unknown, expected_digest: string) => quotaMutation<unknown>('/policy/validate', { policy, expected_digest }, crypto.randomUUID())
// Persist only a hash and opaque operation ID. Reloading after a lost response
// must not create another loan or reconciliation from the same reviewed form.
export async function quotaOperationKey(scope: string, action: string, payload: unknown): Promise<string> {
  const bytes = new TextEncoder().encode(JSON.stringify([scope, action, payload]))
  const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), n => n.toString(16).padStart(2, '0')).join('')
  const name = `aiplex-quota-operation:${hash}`
  const prior = sessionStorage.getItem(name)
  if (prior && /^[0-9a-f-]{36}$/.test(prior)) return prior
  const id = crypto.randomUUID()
  sessionStorage.setItem(name, id)
  return id
}
export function safeQuotaJSON(text: string): unknown {
  for (const token of text.matchAll(/"(?:\\.|[^"\\])*"|(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/g)) {
    if (token[1] === undefined) continue
    if (!/^-?(?:0|[1-9]\d*)$/.test(token[1]) || BigInt(token[1]) > BigInt(Number.MAX_SAFE_INTEGER) || BigInt(token[1]) < -BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Quota values must be exact browser-safe decimal integers. Use the CLI for larger values.')
  }
  const parsed: unknown = JSON.parse(text)
  const visit = (value: unknown) => {
    if (typeof value === 'number' && (!Number.isFinite(value) || (Number.isInteger(value) && !Number.isSafeInteger(value)))) throw new Error('This value exceeds exact browser integer precision. Use the CLI for this operation.')
    if (Array.isArray(value)) value.forEach(visit)
    else if (value && typeof value === 'object') Object.values(value).forEach(visit)
  }
  visit(parsed)
  return parsed
}
