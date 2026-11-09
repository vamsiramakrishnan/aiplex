import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQueries, useQuery } from '@tanstack/react-query'
import { listAccessPoints } from '../api/accessPoints'
import { getAccessPointOverview } from '../api/overview'
import AccessPointSummaryCard from './AccessPointSummaryCard'
import ConnectWizard from './ConnectWizard'
import { CoverageBadge } from './SubjectDetailPanel'

/** Access points with their quota coverage. Uncovered ones reject requests and are flagged first. */
export default function AccessPointStrip({ since }: { since: string }) {
  const list = useQuery({ queryKey: ['access-points'], queryFn: listAccessPoints })
  const aps = list.data ?? []
  const overviews = useQueries({
    queries: aps.map(r => ({ queryKey: ['overview-access-point', r.id, since], queryFn: () => getAccessPointOverview(r.id, since) })),
  })
  const [connecting, setConnecting] = useState<string | null>(null)

  if (list.isLoading) return <p role="status">Loading access points…</p>
  if (list.isError) return <p role="alert" className="text-sm text-red-600">Could not load access points: {(list.error as Error).message}</p>

  const covState = (i: number) => overviews[i]?.data?.quota.coverage
  const uncovered = aps.filter((_, i) => covState(i)?.state === 'uncovered')
  return (
    <section aria-label="Access points" className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-medium">Access points</h3>
        <Link to="/access-points" className="text-sm text-brand-600 hover:underline">Manage access points</Link>
      </div>
      {uncovered.length > 0 && (
        <p role="alert" className="rounded border-2 border-red-600 bg-red-50 dark:bg-red-950 p-3 text-sm text-red-900 dark:text-red-100">
          <strong>{uncovered.length === 1 ? '1 access point rejects' : `${uncovered.length} access points reject`} requests</strong> because no active quota binding covers {uncovered.length === 1 ? 'it' : 'them'}: {uncovered.map(r => r.id).join(', ')}.
        </p>
      )}
      {aps.length === 0 && <p className="text-sm text-gray-500">No access points yet.</p>}
      <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {aps.map((r, i) => {
          const o = overviews[i]
          const cov = o?.data?.quota.coverage
          return (
            <li key={r.id}>
              <AccessPointSummaryCard spec={r.spec} revision={r.revision} footer={
                <div className="space-y-2">
                  {o?.isLoading && <p role="status" className="text-xs text-gray-500">Checking quota coverage…</p>}
                  {o?.isError && <p className="text-xs text-amber-700">Quota coverage unknown: {(o.error as Error).message}</p>}
                  {cov && <div aria-label={`Quota coverage for ${r.id}`}><CoverageBadge coverage={cov} /><p className="mt-1 text-xs text-gray-600 dark:text-gray-400">{cov.detail}</p></div>}
                  <div className="flex gap-3 text-sm">
                    <Link to={`/access-points/${encodeURIComponent(r.id)}`} className="text-brand-600 hover:underline" aria-label={`Open ${r.spec.display_name || r.id}`}>Open</Link>
                    <button type="button" className="text-brand-600 hover:underline" aria-label={`Connect a client to ${r.id}`} onClick={() => setConnecting(r.id)}>Connect</button>
                  </div>
                </div>
              } />
            </li>
          )
        })}
      </ul>
      {connecting && <ConnectWizard accessPointId={connecting} onClose={() => setConnecting(null)} />}
    </section>
  )
}
