import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { presetSince, usageSummary } from '../api/usage'
import { ErrorNotice } from './Forbidden'
import UsageSummaryTable, { UsageHeadline } from './UsageSummaryTable'

/** Model usage and cost of the last 24 hours, from the same usage ledger `aiplex usage summary` reads. */
export default function LedgerCosts() {
  const since = useMemo(() => presetSince('24h'), [])
  const q = useQuery({ queryKey: ['llm-ledger-costs', since], queryFn: () => usageSummary({ since, plane: 'llmplex' }, ['model_id']) })
  return (
    <section aria-label="Cost tracking" className="space-y-3">
      <h3 className="font-semibold text-lg">Cost tracking (last 24 hours)</h3>
      <p className="text-xs text-gray-600 dark:text-gray-400">Unknown usage and cost are shown as &quot;unknown&quot;, never as zero. <Link to="/usage" className="text-brand-600 hover:underline">Open the full usage ledger</Link>.</p>
      {q.isLoading && <p role="status">Loading usage…</p>}
      {q.isError && <ErrorNotice error={q.error} need="operator" what="usage" onRetry={() => void q.refetch()} />}
      {q.data && (q.data.groups.length === 0
        ? <p className="text-gray-600 dark:text-gray-400 text-sm">No model usage in the last 24 hours.</p>
        : <><UsageHeadline groups={q.data.groups} label="Model usage totals" /><UsageSummaryTable groups={q.data.groups} keyField="model_id" heading="Model" /></>)}
    </section>
  )
}
