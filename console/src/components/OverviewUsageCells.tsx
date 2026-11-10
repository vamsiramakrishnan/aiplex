import { UNKNOWN, formatCount, formatTotal } from '../api/usage'
import type { OverviewUsage, OverviewUsageGroup } from '../api/overview'

const hasRecords = (u: OverviewUsage) => Number(u.records) > 0 || (typeof u.records === 'string' && u.records !== '0')

/** Requests, or "unknown" when the server sent no usage. */
export function RequestsText({ usage }: { usage: OverviewUsage | null | undefined }) {
  return <>{usage ? formatCount(usage.requests) : UNKNOWN}</>
}

function counter(u: OverviewUsage, name: string): string {
  const v = u.tokens?.[name]
  if (v === undefined || v === null) return UNKNOWN // not reported is unknown, never 0
  const missing = u.tokens_unknown?.[name]
  return missing !== undefined && Number(missing) > 0 ? `${formatCount(v)} (+${formatCount(missing)} unreported)` : formatCount(v)
}

/** Input / output tokens; a counter nobody reported reads "unknown". */
export function TokensText({ usage }: { usage: OverviewUsage | null | undefined }) {
  if (!usage) return <>{UNKNOWN}</>
  if (!hasRecords(usage)) return <>—</>
  return <>in {counter(usage, 'input_tokens')} / out {counter(usage, 'output_tokens')}</>
}

/** One line per currency and basis. Currencies are never summed; unpriced records are called out. */
export function CostText({ usage }: { usage: OverviewUsage | null | undefined }) {
  if (!usage) return <>{UNKNOWN}</>
  const costs = usage.cost ?? []
  if (costs.length === 0) return <>{hasRecords(usage) ? UNKNOWN : '—'}</>
  return (
    <>
      {costs.map(c => <div key={`${c.currency}-${c.basis}`}>{formatTotal(c)} <span className="text-xs text-gray-600 dark:text-gray-400">{c.basis}</span></div>)}
      {Number(usage.unknown_cost_records) > 0 && <div className="text-xs text-gray-600 dark:text-gray-400">+{formatCount(usage.unknown_cost_records)} records with unknown cost</div>}
    </>
  )
}

export function lastSeenText(iso: string | null | undefined): string {
  if (!iso) return 'never seen in ledger'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/** Usage grouped by model or tool, with the same unknown-vs-zero rules. */
export function UsageGroupsTable({ groups, field, heading }: { groups: OverviewUsageGroup[] | null; field: string; heading: string }) {
  if (!groups || groups.length === 0) return <p className="text-sm text-gray-600 dark:text-gray-400">No {heading.toLowerCase()} usage in this window.</p>
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">{`Usage by ${heading.toLowerCase()}`}</caption>
        <thead><tr className="text-xs text-gray-600 dark:text-gray-400 border-b border-gray-200 dark:border-gray-800"><th scope="col" className="py-1 pr-4">{heading}</th><th scope="col" className="pr-4">Requests</th><th scope="col" className="pr-4">Tokens</th><th scope="col">Cost</th></tr></thead>
        <tbody>
          {groups.map((g, i) => (
            <tr key={`${g.key[field]}-${i}`} className="border-t border-gray-200 dark:border-gray-800 align-top">
              <th scope="row" className="py-1 pr-4 font-normal font-mono text-xs">{g.key[field] || '(none)'}</th>
              <td className="pr-4"><RequestsText usage={g.usage} /></td>
              <td className="pr-4"><TokensText usage={g.usage} /></td>
              <td><CostText usage={g.usage} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
