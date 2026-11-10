import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listSubjects, type OverviewSubject, type SubjectKind } from '../api/overview'
import { errorStatus } from '../api/accessPoints'
import SubjectTable, { type SubjectRow } from './SubjectTable'
import { CostText, RequestsText, TokensText, lastSeenText } from './OverviewUsageCells'

const KIND_NOUN: Record<SubjectKind, string> = { agent: 'agents', user: 'users', client: 'clients' }

/** One tab of the home view: the subjects of a kind, with usage, paged. */
export default function SubjectsPanel({ kind, since, onOpen }: { kind: SubjectKind; since: string; onOpen: (s: OverviewSubject) => void }) {
  const [cursors, setCursors] = useState<string[]>([''])
  useEffect(() => setCursors(['']), [kind, since])
  const cursor = cursors[cursors.length - 1]
  const q = useQuery({ queryKey: ['overview-subjects', kind, since, cursor], queryFn: () => listSubjects(kind, since, cursor) })

  if (q.isLoading) return <p role="status">Loading {KIND_NOUN[kind]}…</p>
  if (q.isError) {
    const status = errorStatus(q.error)
    return (
      <div role="alert" className="rounded border border-status-denied-fg p-3 text-sm text-status-denied-fg">
        <p>{status === 403 ? 'The overview is available to administrators and deployers only.' : `Could not load ${KIND_NOUN[kind]}: ${(q.error as Error).message}`}</p>
        {status !== 403 && <button type="button" className="mt-2 underline" onClick={() => void q.refetch()}>Retry</button>}
      </div>
    )
  }
  const page = q.data!
  const byId = new Map(page.subjects.map(s => [s.id, s]))
  const rows: SubjectRow[] = page.subjects.map(s => ({
    kind: s.kind, id: s.id, label: s.display_name,
    detail: <><span className="font-medium">{s.identity.source}</span>{s.identity.detail ? `: ${s.identity.detail}` : ''}</>,
  }))
  return (
    <div className="space-y-3">
      {!!page.limitations?.length && (
        <ul aria-label="Limitations" className="list-disc ml-5 text-xs text-gray-600 dark:text-gray-400">{page.limitations.map(l => <li key={l}>{l}</li>)}</ul>
      )}
      {page.derived_from && page.derived_from.length > 0 && kind === 'user' && <p className="text-xs text-gray-600 dark:text-gray-400">Users are derived from: {page.derived_from.join(', ')}.</p>}
      {page.usage_truncated && (
        <p role="alert" className="rounded border border-status-warning-fg p-2 text-sm text-status-warning-fg">Usage is truncated: totals cover only the newest ledger records scanned. Choose a shorter range for complete numbers.</p>
      )}
      {!page.usage_available && <p role="note" className="text-sm text-status-warning-fg">The usage ledger is not configured, so usage and last seen are unavailable (shown as unknown).</p>}
      <SubjectTable
        rows={rows}
        caption={`${KIND_NOUN[kind]} overview`}
        empty={`No ${KIND_NOUN[kind]} found for this range.`}
        columns={[
          { header: 'Access points', cell: r => (byId.get(r.id)?.access_points ?? []).join(', ') || '—' },
          { header: 'Last seen', cell: r => (page.usage_available ? lastSeenText(byId.get(r.id)?.last_seen) : 'unknown') },
          { header: 'Requests', cell: r => <RequestsText usage={page.usage_available ? byId.get(r.id)?.usage : null} /> },
          { header: 'Tokens', cell: r => <TokensText usage={page.usage_available ? byId.get(r.id)?.usage : null} /> },
          { header: 'Cost', cell: r => <CostText usage={page.usage_available ? byId.get(r.id)?.usage : null} /> },
        ]}
        actions={r => <button type="button" className="text-brand-600 hover:underline" aria-label={`Details for ${r.id}`} onClick={() => onOpen(byId.get(r.id)!)}>Details</button>}
      />
      {rows.length > 0 && (
        <div className="flex items-center gap-3">
          <button type="button" className="px-3 py-1 text-sm rounded border border-gray-300 dark:border-gray-700 disabled:opacity-40" disabled={cursors.length <= 1} onClick={() => setCursors(c => c.slice(0, -1))}>Previous</button>
          <span className="text-xs text-gray-600 dark:text-gray-400">Page {cursors.length}</span>
          <button type="button" className="px-3 py-1 text-sm rounded border border-gray-300 dark:border-gray-700 disabled:opacity-40" disabled={!page.next_cursor} onClick={() => setCursors(c => [...c, page.next_cursor!])}>Next</button>
        </div>
      )}
    </div>
  )
}
