import { authenticatedRequest } from './client'

// IAM role bindings: which IdP group holds which AIPlex role. Admin only on the
// server (GET included). Shapes and revision handling mirror internal/api/iam.go.
const base = '/api/v1/iam/role-bindings'

export type IAMRole = 'admin' | 'deployer' | 'viewer' | 'agent'
export const ROLES: readonly IAMRole[] = ['admin', 'deployer', 'viewer', 'agent']

export interface RoleBinding {
  id: string
  group: string
  role: IAMRole
  provider?: string
  pool_id?: string
  /** null when the store has no scopes for the binding: unknown, which is not the same as none. */
  scopes: string[] | null
  description?: string
  created_at?: string
  created_by?: string
  /** Absent means unknown. Never treated as 0. */
  revision?: number
}

export interface RoleInfo { role: IAMRole; default_scopes: string[] }

/** Fields the caller states. `scopes` omitted on create means "the role's defaults"; [] means none. */
export interface RoleBindingInput {
  group: string
  role: IAMRole
  provider?: string
  pool_id?: string
  description?: string
  scopes?: string[]
}

const ifMatch = (revision: number) => ({ 'If-Match': String(revision) })

export async function listRoleBindings(): Promise<RoleBinding[]> {
  const rows = await authenticatedRequest<RoleBinding[] | null>(base)
  if (rows == null) return []
  if (!Array.isArray(rows)) throw new Error('Invalid role binding list response')
  return rows
}

export async function listRoles(): Promise<RoleInfo[]> {
  const rows = await authenticatedRequest<RoleInfo[] | null>('/api/v1/iam/roles')
  if (!Array.isArray(rows)) throw new Error('Invalid role list response')
  return rows
}

/** Create. The server derives the id from provider, pool, group and role; scopes omitted gives the role's defaults. */
export const createRoleBinding = (input: RoleBindingInput) =>
  authenticatedRequest<RoleBinding>(base, { method: 'POST', body: JSON.stringify(input) })

/**
 * Replace. The server keeps the stored scopes when `scopes` is omitted (and
 * resets them to the role's defaults when the role changes), so the update always
 * states scopes explicitly: what the form shows is what is stored. `revision` is
 * the one the page read; the server refuses a stale one with REVISION_CONFLICT.
 */
export const updateRoleBinding = (id: string, revision: number, input: RoleBindingInput & { scopes: string[] }) =>
  authenticatedRequest<RoleBinding>(`${base}/${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: ifMatch(revision),
    body: JSON.stringify({
      group: input.group, role: input.role, provider: input.provider ?? '', pool_id: input.pool_id ?? '',
      description: input.description ?? '', scopes: input.scopes,
    }),
  })

export const deleteRoleBinding = (id: string, revision: number) =>
  authenticatedRequest<void>(`${base}/${encodeURIComponent(id)}`, { method: 'DELETE', headers: ifMatch(revision) })

export function isRevisionConflict(error: unknown): boolean {
  const e = error as { status?: number; code?: string } | null
  return !!e && (e.code === 'REVISION_CONFLICT' || e.status === 412)
}
export function errorStatus(error: unknown): number | undefined {
  const s = (error as { status?: unknown } | null)?.status
  return typeof s === 'number' ? s : undefined
}
