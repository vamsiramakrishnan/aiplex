import * as usage from '../api/usage'
import type { APSummaryGroup } from '../api/accessPointUsage'

const muted = 'text-xs text-gray-500 dark:text-gray-400'
const UNKNOWN = usage.UNKNOWN

function tokenCell(g: APSummaryGroup, name: usage.TokenCounter): string {
  const sum = g.tokens[name]
  if (sum === undefined) return UNKNOWN
  const missing = g.tokens_unknown[name]
  return missing !== undefined && usage.toBig(missing) > 0n ? `${usage.formatCount(sum)} (+${usage.formatCount(missing)} unknown)` : usage.formatCount(sum)
}

/** Headline totals over groups that partition the records. Unknown stays unknown; currencies are never merged. */
export function UsageHeadline({ groups, label }: { groups: APSummaryGroup[]; label: string }) {
  const costs = usage.sumCosts(groups)
  const input = usage.tokenHeadline(groups, 'input_tokens')
  const output = usage.tokenHeadline(groups, 'output_tokens')
  const requests = usage.sumCounts(groups.map(g => g.requests))
  const unknownCost = usage.sumCounts(groups.map(g => g.unknown_cost_records))
  const tokenLine = (name: string, t: usage.TokenHeadline) => (
    <p>{name} <strong>{t.reported ? usage.formatCount(t.total.toString()) : UNKNOWN}</strong>
      {t.reported && t.unknownRecords > 0n && <span className={muted}> (+{usage.formatCount(t.unknownRecords.toString())} records unreported)</span>}</p>
  )
  return (
    <div aria-label={label} className="grid gap-4 md:grid-cols-3">
      <div role="group" aria-label="Requests" className="rounded border border-gray-200 dark:border-gray-800 p-3">
        <p className={muted}>Requests</p><p className="text-2xl font-semibold">{usage.formatCount(requests.toString())}</p>
      </div>
      <div role="group" aria-label="Tokens" className="rounded border border-gray-200 dark:border-gray-800 p-3">
        <p className={muted}>Tokens</p>{tokenLine('Input', input)}{tokenLine('Output', output)}
      </div>
      <div role="group" aria-label="Cost" className="rounded border border-gray-200 dark:border-gray-800 p-3">
        <p className={muted}>Cost (per currency and basis)</p>
        {costs.length === 0 ? <p>{UNKNOWN}</p> : (
          <ul>{costs.map(c => <li key={`${c.currency}-${c.basis}`}><strong>{usage.formatTotal(c)}</strong> <span className={muted}>{c.basis}</span></li>)}</ul>
        )}
        {unknownCost > 0n && <p className={muted}>{usage.formatCount(unknownCost.toString())} records have unknown cost and are not counted above.</p>}
      </div>
    </div>
  )
}

/** One row per group: requests, tokens, cost (with currency and basis), unknown counts. */
export default function UsageSummaryTable({ groups, keyField, heading }: {
  groups: APSummaryGroup[]
  keyField: 'caller_kind' | 'model_id' | 'tool_name'
  heading: string
}) {
  const max = groups.reduce((m, g) => Math.max(m, Number(g.requests)), 0)
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">{`Usage by ${heading}`}</caption>
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs text-gray-500">
            <th scope="col" className="py-1 pr-4">{heading}</th>
            <th scope="col" className="pr-4">Requests</th>
            <th scope="col" className="pr-4">Input tokens</th>
            <th scope="col" className="pr-4">Output tokens</th>
            <th scope="col" className="pr-4">Cost</th>
            <th scope="col">Unknown</th>
          </tr>
        </thead>
        <tbody>
          {groups.map((g, i) => {
            const width = max > 0 ? Math.max(2, Math.round((Number(g.requests) / max) * 100)) : 0
            const unknownUsage = usage.toBig(g.unknown_usage_records), unknownCost = usage.toBig(g.unknown_cost_records)
            return (
              <tr key={`${g.key[keyField] ?? ''}-${i}`} className="border-t border-gray-200 dark:border-gray-800 align-top">
                <th scope="row" className="py-1 pr-4 font-normal">{g.key[keyField] || '(none)'}</th>
                <td className="pr-4">
                  <div>{usage.formatCount(g.requests)}</div>
                  <div aria-hidden="true" className="mt-1 h-1.5 w-28 rounded bg-gray-100 dark:bg-gray-800"><div className="h-1.5 rounded bg-brand-500" style={{ width: `${width}%` }} /></div>
                </td>
                <td className="pr-4">{tokenCell(g, 'input_tokens')}</td>
                <td className="pr-4">{tokenCell(g, 'output_tokens')}</td>
                <td className="pr-4">{g.cost.length === 0 ? UNKNOWN : g.cost.map(c => <div key={`${c.currency}-${c.basis}`}>{usage.formatTotal(c)} <span className={muted}>{c.basis}</span></div>)}</td>
                <td>{unknownUsage === 0n && unknownCost === 0n ? '—' : <span>usage: {usage.formatCount(g.unknown_usage_records)}<br />cost: {usage.formatCount(g.unknown_cost_records)}</span>}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
