import { useState } from 'react'
import { useRunTimelineSSE } from '../hooks/useRunTimelineSSE'
import { useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import {
  ExecutionEventKind,
  ExecutionRun,
  ExecutionRunStatus,
  OperatorAudit,
  getRunsHealth,
  listOperatorAudit,
  listRunBudgets,
  listRunEffects,
  listRunObligations,
  listRuns,
  runCancel,
  runCompensate,
  runReconcile,
  runRedrive,
  runSignal,
} from '../api/client'
import { UNKNOWN } from '../api/usage'

// Visual priority for the run-timeline view. Each entry from Tape's
// journal gets a colour + glyph so a Console reader can scan a wall of
// rows and spot the interesting things (UNKNOWNs, denials, gates) at a
// glance. Stable across SDK versions because the kinds are typed in
// the wire contract.
const KIND_STYLE: Record<ExecutionEventKind, { colour: string; glyph: string; label: string }> = {
  'run.started':         { colour: 'bg-gray-100   text-gray-700 dark:bg-gray-800 dark:text-gray-300',   glyph: '▶', label: 'run started' },
  'run.completed':       { colour: 'bg-status-ok-bg  text-status-ok-fg',  glyph: '✓', label: 'completed' },
  'run.failed':          { colour: 'bg-status-denied-bg  text-status-denied-fg',    glyph: '✗', label: 'failed' },
  'decision.recorded':   { colour: 'bg-brand-100  text-brand-700',   glyph: '◆', label: 'decision' },
  'effect.begin':        { colour: 'bg-status-warning-bg  text-status-warning-fg',  glyph: '⟶', label: 'effect begin' },
  'effect.confirmed':    { colour: 'bg-status-ok-bg  text-status-ok-fg',  glyph: '✓', label: 'effect confirmed' },
  'effect.failed':       { colour: 'bg-status-denied-bg  text-status-denied-fg',    glyph: '✗', label: 'effect failed' },
  'effect.unknown':      { colour: 'bg-status-warning-bg text-status-warning-fg', glyph: '?', label: 'UNKNOWN' },
  'effect.duplicate':    { colour: 'bg-status-warning-bg text-status-warning-fg', glyph: '⚠', label: 'duplicate' },
  'obligation.created':  { colour: 'bg-brand-100 text-brand-800', glyph: '↺', label: 'obligation' },
  'gate.waiting':        { colour: 'bg-brand-100 text-brand-700', glyph: '⏸', label: 'waiting on gate' },
  'timer.scheduled':     { colour: 'bg-gray-100   text-gray-600 dark:bg-gray-800 dark:text-gray-400',   glyph: '⏲', label: 'timer' },
  'budget.charged':      { colour: 'bg-brand-50  text-brand-600',   glyph: '$', label: 'budget' },
  'policy.violation':    { colour: 'bg-status-denied-bg  text-status-denied-fg',    glyph: '✦', label: 'POLICY' },
  'run.compacted':       { colour: 'bg-gray-100   text-gray-700 dark:bg-gray-800 dark:text-gray-300',   glyph: '▣', label: 'archived' },
}

const STATUS_STYLE: Record<ExecutionRunStatus, string> = {
  runnable:     'bg-gray-100    text-gray-600 dark:bg-gray-800 dark:text-gray-400',
  running:      'bg-brand-100  text-brand-700',
  waiting:      'bg-brand-100  text-brand-700',
  terminal:     'bg-status-ok-bg  text-status-ok-fg',
  failed:       'bg-status-denied-bg  text-status-denied-fg',
  compensating: 'bg-brand-100  text-brand-700',
  stuck:        'bg-status-denied-bg  text-status-denied-fg',
  cancelled:    'bg-gray-200    text-gray-700 dark:bg-gray-700 dark:text-gray-300',
}

interface Filters {
  tenant_id: string
  agent_id: string
  has_unknown_effects: boolean
  has_obligations: boolean
}

export default function Runs() {
  const [filters, setFilters] = useState<Filters>({
    tenant_id: '',
    agent_id: '',
    has_unknown_effects: false,
    has_obligations: false,
  })
  const [selected, setSelected] = useState<string | null>(null)

  const runs = useQuery({
    queryKey: ['runs', filters],
    queryFn: () => listRuns({
      tenant_id: filters.tenant_id || undefined,
      agent_id: filters.agent_id || undefined,
      has_unknown_effects: filters.has_unknown_effects || undefined,
      has_obligations: filters.has_obligations || undefined,
    }),
    refetchInterval: 5_000,
  })

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h1 className="text-2xl font-bold">Runs</h1>
      </div>
      <p className="text-gray-600 dark:text-gray-400 mb-6">
        Durable agent runs (Tape-backed). What the agent decided, attempted,
        confirmed, replayed, compensated, or left UNKNOWN. See the{' '}
        <a className="underline" href="https://github.com/vamsiramakrishnan/aiplex/blob/main/docs-site/docs/guides/tape-runtime.md">
          Tape runtime guide
        </a>{' '}for the wire contract.
      </p>

      <FiltersBar filters={filters} onChange={setFilters} />

      {runs.isLoading && <div className="text-gray-600 dark:text-gray-400 py-8">Loading runs…</div>}
      {runs.isError && (
        <EmptyState
          title="Couldn't load runs"
          body={String((runs.error as Error)?.message || 'Unknown error')}
        />
      )}
      {runs.data && runs.data.runs.length === 0 && <EmptyRunsChecklist />}

      {runs.data && runs.data.runs.length > 0 && (
        <div className="grid grid-cols-12 gap-4">
          <div className="col-span-7">
            <RunList runs={runs.data.runs} selected={selected} onSelect={setSelected} />
          </div>
          <div className="col-span-5">
            {selected ? (
              <RunDetail
                runID={selected}
                run={runs.data.runs.find(r => r.run_id === selected) ?? null}
              />
            ) : (
              <div className="text-gray-600 dark:text-gray-400 italic p-6 border rounded">
                Select a run to see its timeline.
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function FiltersBar({ filters, onChange }: { filters: Filters; onChange: (f: Filters) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-3 mb-4 p-3 bg-gray-50 rounded dark:bg-gray-900">
      <input
        type="text"
        aria-label="Tenant ID" placeholder="tenant_id"
        value={filters.tenant_id}
        onChange={e => onChange({ ...filters, tenant_id: e.target.value })}
        className="px-3 py-1.5 border rounded text-sm"
      />
      <input
        type="text"
        aria-label="Agent ID" placeholder="agent_id"
        value={filters.agent_id}
        onChange={e => onChange({ ...filters, agent_id: e.target.value })}
        className="px-3 py-1.5 border rounded text-sm"
      />
      <label className="flex items-center gap-1 text-sm">
        <input
          type="checkbox"
          checked={filters.has_unknown_effects}
          onChange={e => onChange({ ...filters, has_unknown_effects: e.target.checked })}
        />
        only runs with UNKNOWN effects
      </label>
      <label className="flex items-center gap-1 text-sm">
        <input
          type="checkbox"
          checked={filters.has_obligations}
          onChange={e => onChange({ ...filters, has_obligations: e.target.checked })}
        />
        only runs with obligations
      </label>
    </div>
  )
}

function RunList({
  runs, selected, onSelect,
}: { runs: ExecutionRun[]; selected: string | null; onSelect: (id: string) => void }) {
  return (
    <div className="space-y-2">
      {runs.map(r => (
        <button
          key={r.run_id}
          onClick={() => onSelect(r.run_id)}
          className={`block w-full text-left p-3 border rounded hover:bg-gray-50 ${
            selected === r.run_id ? 'border-brand-600 bg-brand-50' : ''
          }`}
        >
          <div className="flex items-center justify-between gap-2 mb-1">
            <code className="text-xs font-mono text-gray-600 truncate dark:text-gray-400">{r.run_id}</code>
            <div className="flex items-center gap-1">
              {r.compacted && (
                <span
                  className="px-2 py-0.5 text-xs rounded bg-gray-200 text-gray-700 dark:bg-gray-700 dark:text-gray-300"
                  title={r.compacted_at ? `Compacted ${r.compacted_at}` : 'Compacted'}
                >
                  archived
                </span>
              )}
              <span className={`px-2 py-0.5 text-xs rounded ${STATUS_STYLE[r.status]}`}>
                {r.status}
              </span>
            </div>
          </div>
          <div className="text-sm text-gray-700 dark:text-gray-300">
            <span className="font-medium">{r.agent_id || '(no agent)'}</span>
            {' · '}
            <span className="text-gray-600 dark:text-gray-400">{r.tenant_id || 'no tenant'}</span>
            {' · '}
            <span className="text-gray-600 dark:text-gray-400">{r.subject || '(no subject)'}</span>
          </div>
          <div className="flex items-center gap-3 mt-1 text-xs text-gray-600 dark:text-gray-400">
            <span>{r.decisions_count} decisions</span>
            <span>{r.effects_count} effects</span>
            {r.unknown_effects > 0 && (
              <span className="text-status-warning-fg">{r.unknown_effects} UNKNOWN</span>
            )}
            {r.obligations > 0 && (
              <span className="text-brand-700">{r.obligations} obligations</span>
            )}
            {r.policy_violations > 0 && (
              <span className="text-status-denied-fg">{r.policy_violations} denials</span>
            )}
            {r.budget_usd_charged > 0 && (
              <span>${r.budget_usd_charged.toFixed(2)}</span>
            )}
          </div>
        </button>
      ))}
    </div>
  )
}

function RunDetail({ runID, run }: { runID: string; run: ExecutionRun | null }) {
  const queryClient = useQueryClient()
  const compacted = run?.compacted ?? false
  // Compacted runs are read-only: Tape has zeroed the bulky payloads,
  // and operator actions like Redrive / Compensate cannot meaningfully
  // execute without the original decision/effect bodies. We also skip
  // the live timeline subscription — the events row is gone.
  const { events, live } = useRunTimelineSSE(compacted ? '' : runID)

  const auditQ = useQuery({
    queryKey: ['run-audit', runID],
    queryFn: () => listOperatorAudit(runID),
    refetchInterval: 5_000,
    enabled: !compacted,
  })

  // Effects, obligations and budget charges are separate reads: each section loads and fails on its own.
  // A compacted run's payloads are zeroed, so these routes are not called for it (see archived* below).
  const detailPolling = detailRefetchInterval(run, compacted)
  const effectsQ = useQuery({ queryKey: ['run-effects', runID], queryFn: () => listRunEffects(runID), refetchInterval: detailPolling, enabled: !compacted })
  const obligationsQ = useQuery({ queryKey: ['run-obligations', runID], queryFn: () => listRunObligations(runID), refetchInterval: detailPolling, enabled: !compacted })
  const budgetsQ = useQuery({ queryKey: ['run-budgets', runID], queryFn: () => listRunBudgets(runID), refetchInterval: detailPolling, enabled: !compacted })
  const archivedEffects = compacted ? `Effect detail was archived when this run was compacted. The run summary counts ${run?.effects_count ?? 0} effect(s), ${run?.unknown_effects ?? 0} with unknown outcome.` : null
  const archivedObligations = compacted ? `Obligation detail was archived when this run was compacted. The run summary counts ${run?.obligations ?? 0} obligation(s).` : null
  const archivedBudgets = compacted ? 'Charge detail was archived when this run was compacted. The run summary does not count charges.' : null

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['run-audit', runID] })
    queryClient.invalidateQueries({ queryKey: ['run-effects', runID] })
    queryClient.invalidateQueries({ queryKey: ['run-obligations', runID] })
    queryClient.invalidateQueries({ queryKey: ['run-budgets', runID] })
    queryClient.invalidateQueries({ queryKey: ['runs'] })
  }

  return (
    <div className="border rounded p-3 sticky top-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="font-semibold">Timeline {live && <span className="text-xs text-status-ok-fg ml-2">● live</span>}</h3>
        <code className="text-xs text-gray-600 dark:text-gray-400 font-mono truncate max-w-[18ch]">{runID}</code>
      </div>

      {compacted ? (
        <div className="text-xs p-3 mb-3 bg-gray-100 text-gray-700 rounded dark:bg-gray-800 dark:text-gray-300">
          <div className="font-medium mb-1">Run archived</div>
          <div>
            Details were compacted on{' '}
            <code>{run?.compacted_at ?? 'an earlier date'}</code>. The audit
            envelope (status, counts, decisions/effects metadata) is preserved,
            but request and response payloads have been zeroed. Operator
            actions are disabled.
          </div>
        </div>
      ) : (
        <OperatorToolbar runID={runID} onActionTaken={invalidate} />
      )}

      {events.length === 0 && !compacted && (
        <div className="text-gray-600 dark:text-gray-400 italic text-sm">No events yet.</div>
      )}
      <ol className="space-y-1 max-h-[28rem] overflow-y-auto mt-3">
        {events.map(ev => {
          const s = KIND_STYLE[ev.kind] ?? { colour: 'bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300', glyph: '·', label: ev.kind }
          return (
            <li key={ev.seq} className={`flex items-start gap-2 text-xs p-2 rounded ${s.colour}`}>
              <span className="font-mono flex-shrink-0 w-5 text-center">{s.glyph}</span>
              <span className="font-mono flex-shrink-0 w-6 text-right">{ev.seq}</span>
              <div className="min-w-0 flex-1">
                <div className="font-medium">{s.label}{ev.tool ? ` · ${ev.tool}` : ''}{ev.scope ? ` · ${ev.scope}` : ''}</div>
                {ev.payload_json && (
                  <div className="font-mono text-[10px] text-gray-600 truncate dark:text-gray-400">
                    {ev.payload_json}
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ol>

      <RunSection title="Effects" label="effects" query={effectsQ} archived={archivedEffects} isEmpty={d => d.effects.length === 0} emptyText="No effects recorded for this run.">
        {d => {
          const unknown = d.effects.filter(e => effectOutcome(e.kind) === UNKNOWN).length
          return <>
            <ol className="space-y-1">
              {d.effects.map(e => (
                <li key={e.seq} className="flex items-baseline gap-2 text-xs">
                  <span className="font-mono w-6 text-right">{e.seq}</span>
                  <span className={effectOutcome(e.kind) === UNKNOWN ? 'font-medium text-status-warning-fg' : ''}>{effectOutcome(e.kind)}</span>
                  <span className="font-mono truncate">{e.tool || '-'}</span>
                  <span className="text-gray-600 dark:text-gray-400 truncate">{e.scope || '-'}</span>
                </li>
              ))}
            </ol>
            <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">
              {d.effects.length} effect(s){unknown > 0 ? `, ${unknown} with unknown outcome` : ''}
            </p>
          </>
        }}
      </RunSection>

      <RunSection title="Obligations" label="obligations" query={obligationsQ} archived={archivedObligations} isEmpty={d => d.obligations.length === 0} emptyText="No obligations recorded for this run.">
        {d => <>
          <ol className="space-y-1">
            {d.obligations.map(e => (
              <li key={e.seq} className="flex items-baseline gap-2 text-xs">
                <span className="font-mono w-6 text-right">{e.seq}</span>
                <span className="font-mono truncate">{e.tool || '-'}</span>
                <span className="text-gray-600 dark:text-gray-400 truncate">{e.scope || '-'}</span>
                {e.payload_json && <span className="font-mono text-[10px] text-gray-600 dark:text-gray-400 truncate">{e.payload_json}</span>}
              </li>
            ))}
          </ol>
          <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">{d.obligations.length} obligation(s)</p>
        </>}
      </RunSection>

      <RunSection title="Budget charges" label="budget charges" query={budgetsQ} archived={archivedBudgets} isEmpty={d => d.budgets.length === 0} emptyText="No budget charges recorded on this run; no total.">
        {d => {
          const charges = d.budgets.map(e => ({ seq: e.seq, usd: chargedUSD(e.payload_json) }))
          const known = charges.flatMap(c => (c.usd === null ? [] : [c.usd]))
          const unknown = charges.length - known.length
          const total = known.reduce((sum, usd) => sum + usd, 0)
          const totalLine = unknown === charges.length
            ? `Total charged: ${UNKNOWN} (${unknown} charge(s) with unknown amount)`
            : unknown > 0
              ? `Total charged: $${total.toFixed(2)} (excludes ${unknown} charge(s) with unknown amount)`
              : `Total charged: $${total.toFixed(2)}`
          return <>
            <ol className="space-y-1">
              {charges.map(c => (
                <li key={c.seq} className="flex items-baseline gap-2 text-xs">
                  <span className="font-mono w-6 text-right">{c.seq}</span>
                  {c.usd === null
                    ? <span className="font-medium text-status-warning-fg">{UNKNOWN}</span>
                    : <span className="font-mono">${c.usd.toFixed(2)}</span>}
                </li>
              ))}
            </ol>
            <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">{charges.length} charge(s)</p>
            <p className="text-xs font-medium mt-1">{totalLine}</p>
          </>
        }}
      </RunSection>

      {auditQ.data && auditQ.data.audit.length > 0 && (
        <div className="mt-4 pt-3 border-t">
          <h4 className="text-xs font-semibold text-gray-700 uppercase tracking-wide mb-2 dark:text-gray-300">
            Operator audit
          </h4>
          <ol className="space-y-1">
            {auditQ.data.audit.map(a => <OperatorAuditRow key={a.id} a={a} />)}
          </ol>
        </div>
      )}
    </div>
  )
}

// RunSection shows one run-detail list with its own loading, error and empty states, so a failed
// fetch reads as a failure and never as an empty list.
function RunSection<T>({ title, label, query, archived, isEmpty, emptyText, children }: {
  title: string
  label: string
  query: UseQueryResult<T, Error>
  /** Set for a compacted run: the section then says what was archived and never reads the query. */
  archived?: string | null
  isEmpty: (data: T) => boolean
  emptyText: string
  children: (data: T) => React.ReactNode
}) {
  let body: React.ReactNode
  if (archived) {
    body = <p className="text-xs text-gray-700 dark:text-gray-300">{archived}</p>
  } else if (query.isPending) {
    body = <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Loading {label}…</p>
  } else if (query.isError) {
    body = <p role="alert" className="text-xs text-status-denied-fg">Could not load {label}: {query.error?.message || 'unknown error'}</p>
  } else {
    const data = query.data as T
    body = isEmpty(data)
      ? <p className="text-xs italic text-gray-600 dark:text-gray-400">{emptyText}</p>
      : children(data)
  }
  return (
    <div className="mt-4 pt-3 border-t">
      <h4 className="text-xs font-semibold text-gray-700 uppercase tracking-wide mb-2 dark:text-gray-300">{title}</h4>
      {body}
    </div>
  )
}

// detailRefetchInterval polls a run's effect, obligation and budget lists every 5 s only while the run can
// still change: never for a compacted run, and not once the run is terminal. The status set is the one
// the models define (ExecutionRunTerminal); failed, stuck and cancelled runs can still be redriven or
// compensated, so they keep polling. A run that is not in the loaded list (null) keeps polling.
export function detailRefetchInterval(run: ExecutionRun | null, compacted: boolean): number | false {
  if (compacted) return false
  return run?.status === 'terminal' ? false : 5_000
}

// Effect outcomes follow `aiplex runs effects`: a missing kind or effect.unknown is unknown, which is
// neither success nor failure, and an unrecognised kind is shown as itself.
const EFFECT_OUTCOMES: Record<string, string> = {
  'effect.begin': 'started',
  'effect.confirmed': 'confirmed',
  'effect.failed': 'failed',
  'effect.duplicate': 'duplicate',
}
function effectOutcome(kind: string): string {
  if (kind === '' || kind === 'effect.unknown') return UNKNOWN
  return EFFECT_OUTCOMES[kind] ?? kind
}

// chargedUSD reads usd_charged from a budget event's payload. Null means the amount is unknown, never zero.
function chargedUSD(payload?: string): number | null {
  if (!payload) return null
  try {
    const amount = (JSON.parse(payload) as { usd_charged?: unknown } | null)?.usd_charged
    return typeof amount === 'number' && Number.isFinite(amount) ? amount : null
  } catch {
    return null
  }
}

function OperatorAuditRow({ a }: { a: OperatorAudit }) {
  const colour = a.status === 'accepted' ? 'text-gray-600 dark:text-gray-400' : 'text-status-denied-fg'
  return (
    <li className={`text-xs ${colour} flex items-baseline gap-2`}>
      <span className="font-mono">{new Date(a.at).toLocaleTimeString()}</span>
      <span className="font-semibold">{a.action}</span>
      <span>by {a.actor}</span>
      {a.reason && <span className="italic">({a.reason})</span>}
      {a.gate_name && <span className="italic">gate={a.gate_name}</span>}
      {a.status === 'failed' && <span className="text-status-denied-fg font-medium">FAILED: {a.error}</span>}
    </li>
  )
}

// Operator action toolbar. Destructive actions
// (cancel, compensate) gated behind a confirm dialog.
function OperatorToolbar({ runID, onActionTaken }: { runID: string; onActionTaken: () => void }) {
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [signalForm, setSignalForm] = useState<{ open: boolean; gate: string; resolution: string }>({
    open: false, gate: '', resolution: '',
  })
  const [cancelForm, setCancelForm] = useState<{ open: boolean; reason: string }>({ open: false, reason: '' })

  const fire = async (label: string, fn: () => Promise<unknown>) => {
    setError(null)
    setPending(label)
    try {
      await fn()
      onActionTaken()
    } catch (ex) {
      setError(`${label} failed: ${(ex as Error).message}`)
    } finally {
      setPending(null)
    }
  }

  const confirm = (label: string, fn: () => Promise<unknown>) => {
    if (!window.confirm(`${label} this run? This action affects the live agent.`)) return
    void fire(label, fn)
  }

  const buttons: { label: string; destructive?: boolean; onClick: () => void }[] = [
    { label: 'Redrive',    onClick: () => fire('Redrive', () => runRedrive(runID)) },
    { label: 'Reconcile',  onClick: () => fire('Reconcile', () => runReconcile(runID)) },
    { label: 'Signal',     onClick: () => setSignalForm({ open: true, gate: '', resolution: '' }) },
    {
      label: 'Cancel', destructive: true,
      onClick: () => setCancelForm({ open: true, reason: '' }),
    },
    {
      label: 'Compensate', destructive: true,
      onClick: () => confirm('Compensate', () => runCompensate(runID)),
    },
  ]

  return (
    <div className="space-y-2 mb-3">
      <div className="flex flex-wrap gap-2">
        {buttons.map(b => (
          <button
            key={b.label}
            disabled={!!pending}
            onClick={b.onClick}
            className={`px-2 py-1 text-xs rounded border ${
              b.destructive
                ? 'border-status-denied-fg text-status-denied-fg hover:bg-status-denied-bg'
                : 'border-gray-300 text-gray-700 hover:bg-gray-50 dark:text-gray-300 dark:border-gray-700 dark:hover:bg-gray-800'
            } disabled:opacity-50`}
          >
            {pending === b.label ? '…' : b.label}
          </button>
        ))}
      </div>
      {error && <div className="text-xs text-status-denied-fg">{error}</div>}
      {cancelForm.open && (
        <div className="p-2 border rounded border-status-denied-fg space-y-2 text-xs">
          <input
            aria-label="Cancel reason" placeholder="Reason for cancel (optional)"
            className="block w-full border rounded px-2 py-1"
            value={cancelForm.reason}
            onChange={e => setCancelForm({ open: true, reason: e.target.value })}
          />
          <div className="flex gap-2">
            <button
              className="px-2 py-1 rounded border border-status-denied-fg text-status-denied-fg"
              onClick={() => {
                void fire('Cancel', () => runCancel(runID, cancelForm.reason))
                setCancelForm({ open: false, reason: '' })
              }}
            >Send cancel</button>
            <button
              className="px-2 py-1 rounded border"
              onClick={() => setCancelForm({ open: false, reason: '' })}
            >Keep run</button>
          </div>
        </div>
      )}
      {signalForm.open && (
        <div className="p-2 border rounded bg-brand-50 space-y-2 text-xs">
          <input
            aria-label="Gate name" placeholder="gate_name (required)"
            className="block w-full border rounded px-2 py-1"
            value={signalForm.gate}
            onChange={e => setSignalForm({ ...signalForm, gate: e.target.value })}
          />
          <input
            aria-label="Resolution JSON" placeholder='resolution_json e.g. {"approved":true}'
            className="block w-full border rounded px-2 py-1 font-mono"
            value={signalForm.resolution}
            onChange={e => setSignalForm({ ...signalForm, resolution: e.target.value })}
          />
          <div className="flex gap-2">
            <button
              className="px-2 py-1 rounded bg-brand-600 text-brand-contrast"
              disabled={!signalForm.gate}
              onClick={() => {
                void fire('Signal', () => runSignal(runID, signalForm.gate, signalForm.resolution))
                setSignalForm({ open: false, gate: '', resolution: '' })
              }}
            >Send signal</button>
            <button
              className="px-2 py-1 rounded border"
              onClick={() => setSignalForm({ open: false, gate: '', resolution: '' })}
            >Cancel</button>
          </div>
        </div>
      )}
    </div>
  )
}

// EmptyRunsChecklist — diagnostic checklist explaining
// what's missing for runs to appear. Each row links to the doc that
// fixes it.
function EmptyRunsChecklist() {
  const healthQ = useQuery({
    queryKey: ['runs-health'],
    queryFn: getRunsHealth,
    refetchInterval: 30_000,
  })

  // Absent values are unknown (for example, deployment-wide values withheld
  // from a caller confined to one tenant); their rows say so instead of
  // showing zero.
  const tapeCount = healthQ.data?.tape_instances_count
  const ingestKnown = healthQ.data?.last_ingest_at !== undefined
  const tapeOK = (tapeCount ?? 0) > 0
  const ingestRecent = (() => {
    if (!healthQ.data?.last_ingest_at) return false
    const last = new Date(healthQ.data.last_ingest_at).getTime()
    return Date.now() - last < 5 * 60 * 1000
  })()
  const hasRuns = healthQ.data?.has_runs ?? false

  const Row = ({ ok, title, body, href }: { ok: boolean; title: string; body: React.ReactNode; href: string }) => (
    <li className={`flex items-start gap-3 p-3 border rounded ${ok ? 'bg-status-ok-bg border-status-ok-fg' : 'bg-white dark:bg-gray-900'}`}>
      <span className={`font-mono text-lg flex-shrink-0 ${ok ? 'text-status-ok-fg' : 'text-gray-600 dark:text-gray-400'}`}>
        {ok ? '✓' : '○'}
      </span>
      <div className="flex-1">
        <div className="font-medium">{title}</div>
        <div className="text-sm text-gray-600 dark:text-gray-400">{body}</div>
        {!ok && (
          <a href={href} className="text-sm text-brand-600 underline">How to fix</a>
        )}
      </div>
    </li>
  )

  return (
    <div className="max-w-2xl mx-auto py-8">
      <h3 className="font-medium text-gray-700 mb-2 dark:text-gray-300">No runs yet</h3>
      <p className="text-sm text-gray-600 dark:text-gray-400 mb-4">
        Three things must be true for runs to appear. The first row tells you
        what's missing.
      </p>
      <ol className="space-y-2">
        <Row
          ok={tapeOK}
          title="At least one Instance with runtime.engine=tape"
          body={tapeCount === undefined ? 'Unknown: the number of Tape-backed instances is not available to you.' : `${tapeCount} Tape-backed instance(s) deployed.`}
          href="https://github.com/vamsiramakrishnan/aiplex/blob/main/docs-site/docs/guides/tape-runtime.md"
        />
        <Row
          ok={ingestRecent}
          title="Tape ingestion seen recently"
          body={
            healthQ.data && !ingestKnown
              ? 'Unknown: the last ingest time is not available to you.'
              : healthQ.data?.last_ingest_at
              ? (new Date(healthQ.data.last_ingest_at).getFullYear() <= 1 ? 'No event has ever been ingested.' : `Last event at ${new Date(healthQ.data.last_ingest_at).toLocaleString()}.`)
              : 'No events ingested yet — has the AIPlex outbox sink been wired into Tape?'
          }
          href="https://github.com/vamsiramakrishnan/durable-agents/blob/main/tape/docs/integrations/aiplex.md"
        />
        <Row
          ok={hasRuns}
          title="At least one run has occurred"
          body="A Tape-backed agent has started at least one durable run."
          href="https://github.com/vamsiramakrishnan/aiplex/blob/main/examples/aiplex-tape-treasury/README.md"
        />
      </ol>
    </div>
  )
}

function EmptyState({ title, body }: { title: string; body: React.ReactNode }) {
  return (
    <div className="text-center py-12 px-6 border-2 border-dashed border-gray-200 rounded dark:border-gray-800">
      <h3 className="font-medium text-gray-700 dark:text-gray-300">{title}</h3>
      <p className="text-sm text-gray-600 dark:text-gray-400 mt-2 max-w-md mx-auto">{body}</p>
    </div>
  )
}
