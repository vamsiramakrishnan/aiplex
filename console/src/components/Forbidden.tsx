import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useAccess, ADMIN_REASON, OPERATOR_REASON } from '../hooks/useAccess'

export type Need = 'admin' | 'operator'

export const isForbidden = (error: unknown): boolean => (error as { status?: unknown } | null)?.status === 403

const roleText = (need: Need) => (need === 'admin' ? 'the admin role' : 'the admin or deployer role')

/** The state shown when the caller's roles do not allow a screen or action. Never a spinner, never a raw error. */
export function ForbiddenNotice({ need = 'admin', what }: { need?: Need; what?: string }) {
  const access = useAccess()
  return (
    <section role="alert" aria-label="Access denied" className="max-w-xl rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-6 space-y-2">
      <h1 className="text-xl font-semibold">You need {roleText(need)} for this</h1>
      <p className="text-sm text-gray-600 dark:text-gray-400">
        {what ? `${what} is ` : 'This is '}limited to people with {roleText(need)}.
        {access.roles.length ? ` You have: ${access.roles.join(', ')}.` : ' You are signed in without a role.'} Ask an administrator to grant it, then sign in again.
      </p>
      <p><Link to="/overview" className="text-sm text-brand-600 hover:underline">Back to your home</Link></p>
    </section>
  )
}

/** Route guard: renders its children only for callers with the role. */
export function RequireRole({ need, what, children }: { need: Need; what?: string; children: ReactNode }) {
  const access = useAccess()
  const allowed = need === 'admin' ? access.isAdmin : access.isOperator
  return allowed ? <>{children}</> : <ForbiddenNotice need={need} what={what} />
}

/**
 * An error from a query: a 403 reads as a missing role, everything else as the
 * server's message with an optional retry.
 */
export function ErrorNotice({ error, need = 'operator', what, onRetry }: { error: unknown; need?: Need; what?: string; onRetry?: () => void }) {
  if (isForbidden(error)) {
    return (
      <p role="alert" className="text-sm text-gray-700 dark:text-gray-300">
        {need === 'admin' ? ADMIN_REASON : OPERATOR_REASON}{what ? ` (${what})` : ''}
      </p>
    )
  }
  return (
    <p role="alert" className="text-sm text-status-error">
      {error instanceof Error ? error.message : 'Request failed.'}
      {onRetry && <button type="button" onClick={onRetry} className="ml-2 underline">Retry</button>}
    </p>
  )
}
