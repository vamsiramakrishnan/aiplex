import * as api from '../api/usage'

export function ErrorPanel({ error, what, onRetry }: { error: unknown; what: string; onRetry?: () => void }) {
  const status = api.errorStatus(error)
  const message = status === 403 ? `You do not have access to ${what}.`
    : status === 503 ? 'The usage ledger is not configured on this server.'
    : error instanceof Error ? error.message : String(error)
  return <div role="alert" className="rounded border border-status-denied-fg p-3 text-sm text-status-denied-fg">
    <p>{message}</p>
    {status !== 403 && onRetry && <button type="button" className="mt-2 underline" onClick={onRetry}>Retry</button>}
  </div>
}
