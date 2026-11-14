import { authenticatedRequest } from './client'

// Budgets: team and project limits above per-caller quota, enforced by QuotaPlex.
// Shapes mirror pkg/budgets (the specification), internal/api/budgets.go (the
// status and preview answers) and sdk/aiplex/budgets.go. The server holds at most 24 budgets per tenant (pkg/budgets/budgets.go:34),
// and the page asks for one status per listed budget. Operators read; admins write.
const base = '/api/v1/budgets'

export interface GroupRef { source: string; id: string }

export interface BudgetMembers {
  groups?: GroupRef[] | null
  principals?: string[] | null
  agents?: string[] | null
  client_registrations?: string[] | null
  access_points?: string[] | null
  projects?: string[] | null
  [extra: string]: unknown
}

/**
 * The saved specification. The server refuses fields it does not know, but a newer
 * server may add some: the index signatures keep whatever the page did not model,
 * so an edit sends it back unchanged.
 */
export interface BudgetSpec {
  id: string
  display_name?: string
  parent?: string
  limit: { unit: string; currency?: string; amount: string; [extra: string]: unknown }
  window: { kind: string; unit?: string; time_zone?: string; hours?: number; [extra: string]: unknown }
  members?: BudgetMembers | null
  on_unknown_cost?: string
  include_tool_calls?: boolean
  thresholds?: number[] | null
  [extra: string]: unknown
}

export interface BudgetRecord {
  id: string
  tenant_id?: string
  kind?: string
  /** Absent means unknown. Never treated as 0: a write needs a revision the server sent. */
  revision?: number
  spec: BudgetSpec
}

export interface BudgetStatus {
  id: string
  display_name?: string
  parent?: string
  unit?: string
  currency?: string
  limit?: string
  spent?: string
  reserved?: string
  remaining?: string
  over_limit?: boolean
  window?: { start?: string; end?: string }
  include_tool_calls?: boolean
  /** Cost budgets only, operators only. Absent is unknown, 0 is a real zero. */
  unknown_cost_requests?: number
  on_unknown_cost?: string
  thresholds?: number[] | null
  threshold_events?: { percent: number; at?: string; consumed?: string }[] | null
  children?: { id: string; display_name?: string; unit?: string; currency?: string; limit?: string; spent?: string; reserved?: string; remaining?: string }[] | null
  top_spenders?: { principal_id: string; display?: string; requests?: number; unknown_cost_records?: number; tokens?: Record<string, number> | null; cost?: { currency?: string; basis?: string; amount?: string; amount_decimal?: string; records?: number }[] | null }[] | null
  notes?: string[] | null
}

export interface PreviewSubject { principal?: string; groups?: GroupRef[]; agent?: string; client?: string }
export interface PreviewRequest { subject: PreviewSubject; access_point?: string; plane?: string; draft?: BudgetSpec }
export interface PreviewDraw {
  id: string; display_name?: string; parent?: string; direct?: boolean; reasons?: string[] | null; via_child?: string
  unit?: string; currency?: string; limit?: string; window?: BudgetSpec['window']; draft?: boolean; include_tool_calls?: boolean
}
export interface BudgetPreview {
  hypothetical?: boolean
  plane?: string
  access_point?: string
  project?: string
  draws?: PreviewDraw[] | null
  skipped?: { id: string; reason: string }[] | null
  draft?: { valid: boolean; field?: string; error?: string }
  notes?: string[] | null
}

export const PLANES = ['llmplex', 'mcplex', 'a2aplex'] as const

const ifMatch = (revision: number) => ({ 'If-Match': String(revision) })
const path = (id: string) => `${base}/${encodeURIComponent(id)}`

export async function listBudgets(): Promise<BudgetRecord[]> {
  const rows = await authenticatedRequest<BudgetRecord[] | null>(base)
  if (rows == null) return []
  if (!Array.isArray(rows) || rows.some((r) => !r || typeof r.id !== 'string' || !r.spec || typeof r.spec !== 'object')) throw new Error('Invalid budget list response')
  return rows
}

export async function getBudgetStatus(id: string): Promise<BudgetStatus> {
  const s = await authenticatedRequest<BudgetStatus>(`${path(id)}/status`)
  if (!s || typeof s !== 'object') throw new Error('Invalid budget status response')
  return s
}

/** Create (expectedRevision 0) or replace (the revision the page read). The server refuses a stale one with REVISION_CONFLICT. */
export const putBudget = (spec: BudgetSpec, expectedRevision: number) =>
  authenticatedRequest<BudgetRecord>(path(spec.id), { method: 'PUT', headers: ifMatch(expectedRevision), body: JSON.stringify(spec) })

export const deleteBudget = (id: string, revision: number) =>
  authenticatedRequest<void>(path(id), { method: 'DELETE', headers: ifMatch(revision) })

export const previewBudgets = (req: PreviewRequest) =>
  authenticatedRequest<BudgetPreview>(`${base}/preview`, { method: 'POST', body: JSON.stringify(req) })

export function errorStatus(error: unknown): number | undefined {
  const s = (error as { status?: unknown } | null)?.status
  return typeof s === 'number' ? s : undefined
}
export function errorCode(error: unknown): string | undefined {
  const c = (error as { code?: unknown } | null)?.code
  return typeof c === 'string' ? c : undefined
}
/** Budgets are enforced by QuotaPlex; without it the API answers 409 QUOTAPLEX_REQUIRED. */
export const isQuotaplexRequired = (error: unknown) => errorStatus(error) === 409 && errorCode(error) === 'QUOTAPLEX_REQUIRED'
/**
 * What the budget handlers send for a stale If-Match, on PUT and on DELETE alike: 409 REVISION_CONFLICT
 * (internal/api/budgets.go budgetStoreError, line 65). They never send 412; a missing revision on delete is 428 REVISION_REQUIRED.
 */
export const isRevisionConflict = (error: unknown) => errorStatus(error) === 409 && errorCode(error) === 'REVISION_CONFLICT'
/** The field a rejected specification names, when the server said which. */
export function invalidField(error: unknown): string | undefined {
  const d = (error as { details?: unknown } | null)?.details as { field?: unknown } | undefined
  return d && typeof d.field === 'string' ? d.field : undefined
}
