import { useId, useRef, useState } from 'react'
import { useQueries } from '@tanstack/react-query'
import { SINCE_PRESETS, SUBJECT_KINDS, firstTabWithRows, listSubjects, type SincePreset, type SubjectKind } from '../api/overview'
import AccessPointStrip from '../components/AccessPointStrip'
import SubjectDetailPanel from '../components/SubjectDetailPanel'
import SubjectsPanel from '../components/SubjectsPanel'
import { useAccess } from '../hooks/useAccess'
import Home from './Home'

const TAB_LABEL: Record<SubjectKind, string> = { agent: 'Agents', user: 'Users', client: 'Clients' }

/** Landing page: who is using AIPlex (agents, users, clients) and through which access points. */
export default function Overview() {
  const access = useAccess()
  return access.isOperator ? <OperatorOverview /> : <Home />
}

function OperatorOverview() {
  const uid = useId()
  const [chosen, setChosen] = useState<SubjectKind | null>(null)
  const [since, setSince] = useState<SincePreset>('24h')
  // Open on the first tab that has rows (Users, Agents, Clients). The queries
  // share their cache keys with SubjectsPanel, so the chosen tab renders from
  // cache; until they answer the panel says it is loading rather than flashing
  // an empty tab. A failed query counts as no rows; the tab shows its own error.
  const probes = useQueries({ queries: SUBJECT_KINDS.map(k => ({ queryKey: ['overview-subjects', k, since, ''], queryFn: () => listSubjects(k, since, ''), retry: false })) })
  const counts = Object.fromEntries(SUBJECT_KINDS.map((k, i) => [k, probes[i].isError ? 0 : probes[i].data?.subjects.length])) as Partial<Record<SubjectKind, number | undefined>>
  // Decide once per range: the chosen tab's own query refetching must not flip the answer back to "loading".
  const decided = useRef<{ since: string; kind: SubjectKind } | null>(null)
  const computed = firstTabWithRows(counts)
  if (computed && decided.current?.since !== since) decided.current = { since, kind: computed }
  const kind: SubjectKind | null = chosen ?? (decided.current?.since === since ? decided.current.kind : null)
  const [open, setOpen] = useState<{ kind: SubjectKind; id: string } | null>(null)

  return (
    <div className="space-y-8">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-bold">Overview</h1>
          <p className="text-gray-600 dark:text-gray-400">Who is calling, how they authenticate, what they can reach and what it costs.</p>
        </div>
        <div>
          <label htmlFor={`${uid}-since`} className="block text-xs text-gray-600 dark:text-gray-400 mb-1">Time range</label>
          <select id={`${uid}-since`} value={since} onChange={e => setSince(e.target.value as SincePreset)} className="rounded border border-gray-300 bg-transparent px-2 py-1 text-sm dark:border-gray-700">
            {SINCE_PRESETS.map(p => <option key={p.id} value={p.id}>{p.label}</option>)}
          </select>
        </div>
      </header>

      <section aria-label="Callers" className="space-y-3">
        <div role="tablist" aria-label="Caller kind" className="flex gap-6 border-b border-gray-200 dark:border-gray-800">
          {SUBJECT_KINDS.map(k => (
            <button key={k} role="tab" id={`${uid}-tab-${k}`} aria-selected={kind === k} aria-controls={`${uid}-panel`} type="button"
              onClick={() => { setChosen(k); setOpen(null) }}
              className={`border-b-2 px-1 pb-2 text-sm ${kind === k ? 'border-brand-600 font-medium' : 'border-transparent text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-200'}`}>{TAB_LABEL[k]}</button>
          ))}
        </div>
        <div role="tabpanel" id={`${uid}-panel`} aria-labelledby={kind ? `${uid}-tab-${kind}` : undefined} className="space-y-4">
          {kind === null ? <p role="status">Loading callers…</p> : <SubjectsPanel kind={kind} since={since} onOpen={s => setOpen({ kind: s.kind, id: s.id })} />}
          {open && <SubjectDetailPanel key={`${open.kind}:${open.id}`} kind={open.kind} id={open.id} since={since} onClose={() => setOpen(null)} />}
        </div>
      </section>

      <AccessPointStrip />
    </div>
  )
}
