import { useQuery } from '@tanstack/react-query'
import * as api from '../api/modelSurfaces'

const muted = 'text-xs text-gray-600 dark:text-gray-400'

/**
 * Last provider limits a server replica observed for a surface, and the
 * capacity policy's cooldown phase. Limits the provider did not report are
 * "unknown", never zero.
 */
export default function SurfaceCapacityPanel({ surfaceId }: { surfaceId: string }) {
  const q = useQuery({ queryKey: ['surface-capacity', surfaceId], queryFn: () => api.getSurfaceCapacity(surfaceId), retry: false })
  if (q.isPending) return <p role="status" className="text-sm text-gray-600 dark:text-gray-400">Loading capacity…</p>
  if (q.isError) {
    const status = (q.error as { status?: number }).status
    return <p role="alert" className="text-sm text-status-denied-fg">{status === 403 ? 'Capacity details need an administrator.' : `Could not load capacity: ${(q.error as Error).message}`}</p>
  }
  const c = q.data
  const l = c.limits
  const policy = c.capacity_policy
  return <section aria-label={`Capacity of ${surfaceId}`} className="space-y-3 rounded border border-gray-200 p-4 dark:border-gray-800">
    <div className="flex items-center justify-between gap-3"><h4 className="font-medium">Capacity of {surfaceId}</h4><button type="button" className="text-sm underline" onClick={() => void q.refetch()}>Refresh</button></div>
    <p className={muted}>{c.scope}. Each server replica keeps its own last observation, so another replica may show a different one.</p>

    <div role="group" aria-label="Last observed limits">
      <h5 className="text-sm font-medium">Last observed provider limits</h5>
      {!c.observed || !l ? <p className="text-sm">No provider limit has been observed on this replica yet.</p> : <dl className="mt-1 grid grid-cols-[10rem_1fr] gap-x-3 gap-y-1 text-sm">
        <dt className="text-gray-600 dark:text-gray-400">Observed</dt><dd>{l.observed_at} ({l.provider})</dd>
        <dt className="text-gray-600 dark:text-gray-400">Classification</dt><dd>{l.classification || 'none'}</dd>
        {l.http_status !== undefined && <><dt className="text-gray-600 dark:text-gray-400">HTTP status</dt><dd>{l.http_status}</dd></>}
        <dt className="text-gray-600 dark:text-gray-400">Retry after</dt><dd>{l.retry_after_ms === undefined ? api.UNKNOWN : `${l.retry_after_ms.toLocaleString('en-US')} ms`}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Requests</dt><dd>{api.describeWindow(l.requests)}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Tokens</dt><dd>{api.describeWindow(l.tokens)}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Input tokens</dt><dd>{api.describeWindow(l.input_tokens)}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Output tokens</dt><dd>{api.describeWindow(l.output_tokens)}</dd>
      </dl>}
    </div>

    <div role="group" aria-label="Capacity policy state">
      <h5 className="text-sm font-medium">Capacity policy</h5>
      {!policy.enabled || !policy.policy ? <p className="text-sm">Off. Limits are observed but nothing is enforced.</p> : <>
        <p className="text-sm">Cooldown phase: <strong>{policy.cooldown?.phase ?? 'none'}</strong>{policy.cooldown?.until ? ` until ${policy.cooldown.until}` : ''}{policy.cooldown?.retry_after_ms ? ` (retry after ${policy.cooldown.retry_after_ms.toLocaleString('en-US')} ms)` : ''}</p>
        <p className={muted}>{api.PHASE_TEXT[policy.cooldown?.phase ?? 'none'] ?? ''}</p>
        <p className={`${muted} mt-1`}>Cooldown {policy.policy.cooldown_enabled ? `on (up to ${policy.policy.max_cooldown_seconds}s)` : 'off'} · headroom {policy.policy.headroom_fraction ? `${policy.policy.headroom_fraction} (minimum weight ${policy.policy.min_weight}, default weight ${policy.policy.default_weight})` : 'off'} · signals used for up to {policy.policy.signal_max_age_seconds}s</p>
      </>}
    </div>
  </section>
}
