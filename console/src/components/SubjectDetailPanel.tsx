import { useQuery } from '@tanstack/react-query'
import { getSubject, type OverviewAccessItem, type OverviewAccessPointQuota, type SubjectDetail, type SubjectKind } from '../api/overview'
import { errorStatus } from '../api/accessPoints'
import { CostText, RequestsText, TokensText, UsageGroupsTable, lastSeenText } from './OverviewUsageCells'

export interface AccessLabel { text: string; tone: 'granted' | 'unknown' | 'denied' | 'conditional' }

/**
 * How an access item reads. Only a state of "granted" whose reasons are not all
 * conditional reads as Granted; unknown and conditional never do.
 */
export function accessLabel(item: Pick<OverviewAccessItem, 'state' | 'reasons'>): AccessLabel {
  if (item.state === 'not_granted') return { text: 'Not granted', tone: 'denied' }
  if (item.state === 'granted') {
    const reasons = item.reasons ?? []
    if (reasons.length > 0 && reasons.every(r => r.conditional)) return { text: 'Conditional', tone: 'conditional' }
    return { text: 'Granted', tone: 'granted' }
  }
  return { text: 'Unknown', tone: 'unknown' }
}
const TONE = {
  granted: 'text-green-700 dark:text-green-400', denied: 'text-gray-600 dark:text-gray-400',
  unknown: 'text-amber-700 dark:text-amber-400', conditional: 'text-amber-700 dark:text-amber-400',
} as const

function AccessTable({ title, items }: { title: string; items: OverviewAccessItem[] | null }) {
  if (!items || items.length === 0) return <p className="text-sm text-gray-500">No {title.toLowerCase()} are reachable through the access points considered.</p>
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">{title}</caption>
        <thead><tr className="text-xs text-gray-500 border-b border-gray-200 dark:border-gray-800"><th scope="col" className="py-1 pr-4">Name</th><th scope="col" className="pr-4">Access point</th><th scope="col" className="pr-4">Access</th><th scope="col">Why</th></tr></thead>
        <tbody>
          {items.map(it => {
            const label = accessLabel(it)
            return (
              <tr key={`${it.access_point_id}:${it.kind}:${it.name}`} className="border-t border-gray-200 dark:border-gray-800 align-top">
                <th scope="row" className="py-1 pr-4 font-normal font-mono text-xs">{it.name}</th>
                <td className="pr-4 text-xs">{it.access_point_id}</td>
                <td className={`pr-4 font-medium ${TONE[label.tone]}`}>{label.text}</td>
                <td className="text-xs">
                  <ul className="space-y-0.5">
                    {(it.reasons ?? []).map((r, i) => (
                      <li key={i}>
                        <span className="text-gray-500">{r.type}{r.ref ? ` ${r.ref}` : ''}</span>
                        {r.conditional && <span className="ml-1 rounded bg-amber-100 text-amber-900 dark:bg-amber-900 dark:text-amber-100 px-1">conditional</span>}
                        {r.detail && <span>: {r.detail}</span>}
                      </li>
                    ))}
                    {(it.reasons ?? []).length === 0 && <li className="text-gray-500">No explanation provided.</li>}
                  </ul>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export function CoverageBadge({ coverage }: { coverage: OverviewAccessPointQuota['coverage'] }) {
  const map = {
    covered: ['Quota covered', 'bg-green-100 text-green-900 dark:bg-green-900 dark:text-green-100'],
    not_required: ['No quota required', 'bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200'],
    uncovered: ['Quota UNCOVERED: requests are rejected', 'bg-red-600 text-white font-semibold'],
    unknown: ['Quota coverage unknown', 'bg-amber-100 text-amber-900 dark:bg-amber-900 dark:text-amber-100'],
  } as const
  const [text, cls] = map[coverage.state] ?? [`Quota ${coverage.state}`, map.unknown[1]]
  return <span className={`inline-block rounded px-2 py-0.5 text-xs ${cls}`}>{text}</span>
}

/** Everything the overview knows about one subject: identity, access with reasons, quota, usage. */
export default function SubjectDetailPanel({ kind, id, since, onClose }: { kind: SubjectKind; id: string; since: string; onClose: () => void }) {
  const q = useQuery({ queryKey: ['overview-subject', kind, id, since], queryFn: () => getSubject(kind, id, since) })
  return (
    <aside aria-label={`Details for ${id}`} className="rounded border border-gray-200 dark:border-gray-800 p-4 space-y-4 bg-white dark:bg-gray-900">
      <div className="flex justify-between">
        <h3 className="text-lg font-medium">{q.data?.display_name || id}</h3>
        <button type="button" className="text-sm text-gray-500" onClick={onClose}>Close</button>
      </div>
      {q.isLoading && <p role="status">Loading details…</p>}
      {q.isError && <p role="alert" className="text-sm text-red-600">{errorStatus(q.error) === 403 ? 'You do not have access to subject details.' : `Could not load details: ${(q.error as Error).message}`}</p>}
      {q.data && <Body d={q.data} />}
    </aside>
  )
}

function Body({ d }: { d: SubjectDetail }) {
  return (
    <div className="space-y-5">
      <section aria-label="Identity">
        <h4 className="text-sm font-semibold">Identity</h4>
        <p className="text-sm"><strong>{d.identity.source}</strong>{d.identity.detail ? `: ${d.identity.detail}` : ''}</p>
        <p className="text-xs font-mono break-all text-gray-500">{d.id}</p>
        {d.aliases?.length ? <p className="text-xs text-gray-500">Also known as: {d.aliases.join(', ')}</p> : null}
        {d.identity_bindings?.map(b => <p key={b.id} className="text-xs">Identity binding <code>{b.id}</code> ({b.preset}): issuer <code>{b.issuer}</code>, subject <code>{b.subject}</code></p>)}
        {d.user_identity_providers?.map(p => <p key={p.id} className="text-xs">User identity provider <code>{p.id}</code>{p.disabled ? ' (disabled)' : ''}: issuer <code>{p.issuer}</code></p>)}
        <p className="text-xs text-gray-500">Last seen: {lastSeenText(d.last_seen)}</p>
      </section>

      <section aria-label="Access points">
        <h4 className="text-sm font-semibold">Access points</h4>
        {(d.access_points ?? []).length === 0 ? <p className="text-sm text-gray-500">No access points were evaluated for this subject.</p> : (
          <ul className="text-sm space-y-1">
            {d.access_points!.map(a => (
              <li key={a.id}><code>{a.id}</code>: <strong className={a.admits === 'yes' ? TONE.granted : a.admits === 'no' ? TONE.denied : TONE.unknown}>{a.admits === 'yes' ? 'Admitted' : a.admits === 'no' ? 'Not admitted' : 'Unknown'}</strong> <span className="text-xs text-gray-600 dark:text-gray-400">{a.reason}</span></li>
            ))}
          </ul>
        )}
      </section>

      <section aria-label="Access" className="space-y-2">
        <h4 className="text-sm font-semibold">What it can reach</h4>
        <p className="text-xs text-gray-600 dark:text-gray-400">{d.access.scopes_note}{!d.access.scopes_known && ' Items that depend on scopes are shown as unknown, not as granted.'}</p>
        {d.access.truncated && <p role="alert" className="text-xs text-amber-800 dark:text-amber-300">The access list was truncated.</p>}
        <h5 className="text-xs font-semibold uppercase text-gray-500">Models</h5><AccessTable title="Models" items={d.access.models} />
        <h5 className="text-xs font-semibold uppercase text-gray-500">Tools</h5><AccessTable title="Tools" items={d.access.tools} />
        <h5 className="text-xs font-semibold uppercase text-gray-500">Skills</h5><AccessTable title="Skills" items={d.access.skills} />
      </section>

      <section aria-label="Quota" className="space-y-1">
        <h4 className="text-sm font-semibold">Quota</h4>
        {(d.quota.access_points ?? []).map(a => (
          <div key={a.access_point_id} className="text-sm">
            <code>{a.access_point_id}</code>{a.qos_class ? ` (${a.qos_class})` : ''}: <CoverageBadge coverage={a.coverage} /> <span className="text-xs text-gray-600 dark:text-gray-400">{a.coverage.detail}</span>
            <div className="text-xs text-gray-500">Allocations: {a.allocations?.budgets?.length ?? 0} budgets, {a.allocations?.rates?.length ?? 0} rate limits, {a.allocations?.concurrency?.length ?? 0} concurrency limits</div>
          </div>
        ))}
        <p className="text-sm">{d.quota.balance.available ? 'Balance is available.' : <>Balance unavailable: {d.quota.balance.reason || 'no reason given'}</>}</p>
      </section>

      <section aria-label="Usage" className="space-y-2">
        <h4 className="text-sm font-semibold">Usage</h4>
        <p className="text-sm">Requests <strong><RequestsText usage={d.usage} /></strong> · Tokens <TokensText usage={d.usage} /> · Cost <CostText usage={d.usage} /></p>
        <h5 className="text-xs font-semibold uppercase text-gray-500">By model</h5><UsageGroupsTable groups={d.usage_by_model} field="model_id" heading="Model" />
        <h5 className="text-xs font-semibold uppercase text-gray-500">By tool</h5><UsageGroupsTable groups={d.usage_by_tool} field="tool_name" heading="Tool" />
      </section>

      {!!d.notes?.length && <ul aria-label="Notes" className="list-disc ml-5 text-xs text-gray-600 dark:text-gray-400">{d.notes.map(n => <li key={n}>{n}</li>)}</ul>}
    </div>
  )
}
