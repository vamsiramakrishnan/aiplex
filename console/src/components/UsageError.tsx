import * as api from '../api/usage'

export function ErrorPanel({ error, what, onRetry }: { error: unknown; what: string; onRetry?: () => void }) {
  const status = api.errorStatus(error)
  const message = status === 403 ? `You do not have access to ${what}.`
    : status === 503 ? 'The usage ledger is not configured on this server.'
    : error instanceof Error ? error.message : String(error)
  return <div role="alert" className="rounded border border-red-300 p-3 text-sm text-red-700 dark:border-red-800 dark:text-red-400">
    <p>{message}</p>
    {status !== 403 && onRetry && <button type="button" className="mt-2 underline" onClick={onRetry}>Retry</button>}
  </div>
}
