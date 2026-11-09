import { useId, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { accessPointUsageSummary, type APGroupField } from '../api/accessPointUsage'
import { TIME_PRESETS, presetSince, formatCount, type TimePreset } from '../api/usage'
import { ErrorPanel } from './UsageError'
import UsageSummaryTable, { UsageHeadline } from './UsageSummaryTable'

function Section({ accessPointId, groupBy, heading, since, headline }: { accessPointId: string; groupBy: APGroupField; heading: string; since: string | undefined; headline?: boolean }) {
  const query = useQuery({
    queryKey: ['access-point-usage', accessPointId, groupBy, since ?? ''],
    queryFn: () => accessPointUsageSummary(accessPointId, [groupBy], since ? { since } : {}),
  })
  if (query.isPending) return <p role="status" className="text-sm text-gray-500">Loading usage by {heading.toLowerCase()}…</p>
  if (query.isError) return <ErrorPanel error={query.error} what="usage for this access point" onRetry={() => void query.refetch()} />
  const { groups, truncated, records_scanned } = query.data
  return (
    <div className="space-y-2">
      {truncated && <p role="alert" className="rounded border border-amber-400 p-2 text-sm text-amber-800 dark:text-amber-300">Truncated: totals cover only the newest {formatCount(records_scanned)} records scanned. Choose a shorter window for complete totals.</p>}
      {groups.length === 0 ? <p className="text-sm text-gray-500">No {heading.toLowerCase()} usage recorded in this window.</p> : (
        <>
          {headline && <UsageHeadline groups={groups} label="Access point usage totals" />}
          <UsageSummaryTable groups={groups} keyField={groupBy} heading={heading} />
        </>
      )}
    </div>
  )
}

/** Usage of one access point: totals and breakdowns by caller kind, model and tool. */
export default function AccessPointUsagePanel({ accessPointId }: { accessPointId: string }) {
  const uid = useId()
  const [preset, setPreset] = useState<TimePreset>('24h')
  // Resolved when the preset changes so query keys stay stable between renders.
  const [since, setSince] = useState(() => presetSince('24h'))
  return (
    <section aria-label="Usage" className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <h3 className="text-lg font-medium">Usage</h3>
        <div>
          <label htmlFor={`${uid}-range`} className="block text-xs text-gray-500 mb-1">Time range</label>
          <select id={`${uid}-range`} value={preset} onChange={e => { const p = e.target.value as TimePreset; setPreset(p); setSince(presetSince(p)) }}
            className="rounded border border-gray-300 bg-transparent px-2 py-1 text-sm dark:border-gray-700">
            {TIME_PRESETS.map(p => <option key={p.id} value={p.id}>{p.label}</option>)}
          </select>
        </div>
      </div>
      <p className="text-xs text-gray-500">Unknown usage and cost are shown as &quot;unknown&quot;, never as zero. Costs are shown per currency and basis and never added across currencies.</p>
      <h4 className="text-sm font-semibold">By caller kind</h4>
      <Section accessPointId={accessPointId} groupBy="caller_kind" heading="Caller kind" since={since} headline />
      <h4 className="text-sm font-semibold">By model</h4>
      <Section accessPointId={accessPointId} groupBy="model_id" heading="Model" since={since} />
      <h4 className="text-sm font-semibold">By tool</h4>
      <Section accessPointId={accessPointId} groupBy="tool_name" heading="Tool" since={since} />
    </section>
  )
}
