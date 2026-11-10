import { useOptionalAuth } from './useAuth'

/** What the caller may do, from the server-verified `GET /api/v1/iam/whoami` (read once at sign-in). */
export interface Access {
  roles: string[]
  scopes: string[]
  isAdmin: boolean
  /** admin or deployer: may read configuration. */
  isOperator: boolean
  /** Why an action is unavailable, for a disabled control's title. Undefined when allowed. */
  reason: (need: 'admin' | 'operator') => string | undefined
}

export const ADMIN_REASON = 'You need the admin role for this.'
export const OPERATOR_REASON = 'You need the admin or deployer role for this.'

export function accessFrom(roles: readonly string[] = [], scopes: readonly string[] = []): Access {
  const isAdmin = roles.includes('admin')
  const isOperator = isAdmin || roles.includes('deployer')
  return {
    roles: [...roles], scopes: [...scopes], isAdmin, isOperator,
    reason: (need) => (need === 'admin' ? (isAdmin ? undefined : ADMIN_REASON) : isOperator ? undefined : OPERATOR_REASON),
  }
}

export function useAccess(): Access {
  const auth = useOptionalAuth()
  // Without a session provider (a screen rendered alone in a test) nothing is gated:
  // the server remains the authority and answers 403 where it must.
  if (!auth) return accessFrom(['admin'], [])
  return accessFrom(auth.user?.roles, auth.user?.scopes)
}
