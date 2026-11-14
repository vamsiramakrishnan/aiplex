import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { errorStatus, getAccessPointRouting, purgeAccessPointCache, type AccessPointRouting, type RoutedBackend, type RoutingOutcome } from '../api/accessPoints'
import { UNKNOWN } from '../api/usage'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800'

/** A value the API did not send is unknown: never 0, never blank, never "healthy" or "closed". */
const text = (v: unknown): string => (typeof v === 'string' && v !== '' ? v : UNKNOWN)
const num = (v: unknown): string => (typeof v === 'number' && Number.isFinite(v) ? String(v) : UNKNOWN)
const when = (v: string): string => {
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? UNKNOWN : d.toLocaleTimeString()
}
const messageOf = (e: unknown) => (e instanceof Error ? e.message : String(e))

/**
 * The breaker state, then when it opened and when it may retry, each only when the API sent it
 * (the API omits both for a breaker that has not opened). Nothing is printed for an absent time.
 */
const breakerText = (b: RoutedBackend) => text(b.breaker?.state)
  + (b.breaker?.opened_at ? `, opened at ${when(b.breaker.opened_at)}` : '')
  + (b.breaker?.half_open_at ? `, retry at ${when(b.breaker.half_open_at)}` : '')

/** Recent outcomes in order. The API omits upstream_status when there was none, so no suffix is shown then. */
function recentText(recent: RoutingOutcome[] | undefined): string {
  if (!Array.isArray(recent)) return UNKNOWN
  if (recent.length === 0) return 'none recorded'
  return recent.map(o => text(o.outcome) + (typeof o.upstream_status === 'number' && o.upstream_status !== 0 ? `(${o.upstream_status})` : '')).join(', ')
}

/**
 * Routing state of one access point in the API process that answered: each
 * routed model's backends in routing order with their breaker, and the cache.
 * Operators read it; purging the response cache is offered to admins only and
 * asks first. A failed refresh keeps the last good figures, labelled as stale.
 */
export default function AccessPointRoutingPanel({ accessPointId, isAdmin }: { accessPointId: string; isAdmin: boolean }) {
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState(false)
  const routing = useQuery({ queryKey: ['ap-routing', accessPointId], queryFn: () => getAccessPointRouting(accessPointId) })
  const purge = useMutation({
    mutationFn: () => purgeAccessPointCache(accessPointId),
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: ['ap-routing', accessPointId] }) },
    onSettled: () => setConfirming(false),
  })

  const purgedCount = purge.data?.purged
  const purgeScope = purge.data?.process_local === true ? ' (this API process)' : ''
  return (
    <section aria-label="Routing" className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-lg font-medium">Routing</h3>
        <div className="flex flex-wrap gap-2">
          <button type="button" className={btn} onClick={() => void routing.refetch()} disabled={routing.isFetching}>Refresh routing state</button>
          {isAdmin && !confirming && <button type="button" className={btn} onClick={() => setConfirming(true)} disabled={purge.isPending}>Purge cache</button>}
        </div>
      </div>

      {isAdmin && confirming && (
        <span role="group" aria-label="Confirm purge" className="flex flex-wrap items-center gap-2 text-sm">
          <span>
            Remove the cached answers of {accessPointId}?
            {routing.data?.process_local === true && ' This clears only the API process that answers; other replicas keep their own cache.'}
          </span>
          <button type="button" onClick={() => purge.mutate()} disabled={purge.isPending} className="px-3 py-1.5 text-sm bg-status-denied-fg text-status-denied-bg rounded disabled:opacity-50">
            {purge.isPending ? 'Purging…' : 'Confirm purge'}
          </button>
          <button type="button" className={btn} onClick={() => setConfirming(false)} disabled={purge.isPending}>Cancel</button>
        </span>
      )}
      {purge.isError && (
        <p role="alert" className="text-sm text-status-denied-fg">
          {errorStatus(purge.error) === 403 ? 'Purging the cache requires the admin role.' : `Could not purge the cache: ${messageOf(purge.error)}`}
        </p>
      )}
      {purge.isSuccess && (
        <p role="status" className="text-sm">
          {typeof purgedCount === 'number'
            ? `Removed ${purgedCount} cached answers of access point ${accessPointId}${purgeScope}.`
            : `The API did not say how many cached answers were removed${purgeScope}.`}
        </p>
      )}

      {routing.isPending && <p role="status" className="text-sm text-gray-600 dark:text-gray-400">Loading routing state…</p>}
      {routing.isError && (
        <div role="alert" className="flex items-center gap-3 rounded border border-status-denied-fg p-3 text-sm text-status-denied-fg">
          <span>
            {routing.data
              ? `The latest refresh of routing state failed (${messageOf(routing.error)}). The figures shown are from the last successful refresh at ${new Date(routing.dataUpdatedAt).toLocaleTimeString()}.`
              : `Could not load routing state: ${messageOf(routing.error)}.`}
          </span>
          <button type="button" className={btn} onClick={() => void routing.refetch()}>Retry</button>
        </div>
      )}
      {routing.data && <RoutingView view={routing.data} />}
    </section>
  )
}

function RoutingView({ view }: { view: AccessPointRouting }) {
  const models = view.models
  return (
    <div className="space-y-4">
      {!Array.isArray(models) && <p className="text-sm">Routed models: {UNKNOWN}</p>}
      {Array.isArray(models) && models.length === 0 && (
        <p className="text-sm text-gray-600 dark:text-gray-400">No routed models: this access point has no backends, routing or cache.</p>
      )}
      {Array.isArray(models) && models.map((m, i) => (
        <div key={`${m.name ?? 'model'}-${i}`} className="space-y-1">
          <h4 className="font-mono text-sm">{text(m.name)}</h4>
          <p className="text-sm">Strategy: <span className="font-semibold">{text(m.strategy)}</span></p>
          {!Array.isArray(m.backends) ? <p className="text-sm">Backends: {UNKNOWN}</p> : m.backends.length === 0 ? <p className="text-sm text-gray-600 dark:text-gray-400">No backends.</p> : (
            <table className="w-full text-left text-sm">
              <caption className="sr-only">{`Backends of ${text(m.name)}`}</caption>
              <thead>
                <tr className="text-xs text-gray-600 dark:text-gray-400">
                  <th scope="col" className="py-1 pr-4">Order</th>
                  <th scope="col" className="pr-4">Backend</th>
                  <th scope="col" className="pr-4">Weight</th>
                  <th scope="col" className="pr-4">Priority</th>
                  <th scope="col" className="pr-4">Breaker</th>
                  <th scope="col" className="pr-4">Consecutive failures</th>
                  <th scope="col">Recent outcomes</th>
                </tr>
              </thead>
              <tbody>
                {m.backends.map((b, j) => (
                  <tr key={j} className="border-t border-gray-200 dark:border-gray-800">
                    <td className="py-1 pr-4">{j + 1}</td>
                    <td className="pr-4 font-mono text-xs">{`${text(b.surface_ref)}/${text(b.native_model)}`}</td>
                    <td className="pr-4">{num(b.weight)}</td>
                    <td className="pr-4">{num(b.priority)}</td>
                    <td className="pr-4">{breakerText(b)}</td>
                    <td className="pr-4">{num(b.breaker?.consecutive_failures)}</td>
                    <td className="text-xs">{recentText(b.breaker?.recent)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      ))}
      {view.cache_store && (
        <p className="text-sm">Response cache (this process): {num(view.cache_store.entries)} entries, {num(view.cache_store.bytes)} of {num(view.cache_store.max_bytes)} bytes</p>
      )}
      {view.process_local === true && <p className="text-xs text-gray-600 dark:text-gray-400">State is per API process and in memory: other replicas keep their own.</p>}
      {typeof view.process_local !== 'boolean' && <p className="text-xs text-gray-600 dark:text-gray-400">Scope of this state: unknown.</p>}
    </div>
  )
}
