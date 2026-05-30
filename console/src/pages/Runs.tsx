import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ExecutionEvent,
  ExecutionEventKind,
  ExecutionRun,
  ExecutionRunStatus,
  OperatorAudit,
  getRunsHealth,
  listOperatorAudit,
  listRunEvents,
  listRuns,
  runCancel,
  runCompensate,
  runReconcile,
  runRedrive,
  runSignal,
} from '../api/client'

// Visual priority for the run-timeline view. Each entry from Tape's
// journal gets a colour + glyph so a Console reader can scan a wall of
// rows and spot the interesting things (UNKNOWNs, denials, gates) at a
// glance. Stable across SDK versions because the kinds are typed in
// the wire contract.
const KIND_STYLE: Record<ExecutionEventKind, { colour: string; glyph: string; label: string }> = {
  'run.started':         { colour: 'bg-gray-100   text-gray-700',   glyph: '▶', label: 'run started' },
  'run.completed':       { colour: 'bg-green-100  text-green-700',  glyph: '✓', label: 'completed' },
  'run.failed':          { colour: 'bg-red-100    text-red-700',    glyph: '✗', label: 'failed' },
  'decision.recorded':   { colour: 'bg-blue-100   text-blue-700',   glyph: '◆', label: 'decision' },
  'effect.begin':        { colour: 'bg-amber-100  text-amber-800',  glyph: '⟶', label: 'effect begin' },
  'effect.confirmed':    { colour: 'bg-green-100  text-green-700',  glyph: '✓', label: 'effect confirmed' },
  'effect.failed':       { colour: 'bg-red-100    text-red-700',    glyph: '✗', label: 'effect failed' },
  'effect.unknown':      { colour: 'bg-orange-100 text-orange-800', glyph: '?', label: 'UNKNOWN' },
  'effect.duplicate':    { colour: 'bg-yellow-100 text-yellow-800', glyph: '⚠', label: 'duplicate' },
  'obligation.created':  { colour: 'bg-purple-100 text-purple-800', glyph: '↺', label: 'obligation' },
  'gate.waiting':        { colour: 'bg-indigo-100 text-indigo-700', glyph: '⏸', label: 'waiting on gate' },
  'timer.scheduled':     { colour: 'bg-gray-100   text-gray-600',   glyph: '⏲', label: 'timer' },
  'budget.charged':      { colour: 'bg-blue-50    text-blue-600',   glyph: '$', label: 'budget' },
  'policy.violation':    { colour: 'bg-red-100    text-red-800',    glyph: '✦', label: 'POLICY' },
  'run.compacted':       { colour: 'bg-gray-100   text-gray-700',   glyph: '▣', label: 'archived' },
}

const STATUS_STYLE: Record<ExecutionRunStatus, string> = {
  runnable:     'bg-gray-100    text-gray-600',
  running:      'bg-blue-100    text-blue-700',
  waiting:      'bg-indigo-100  text-indigo-700',
  terminal:     'bg-green-100   text-green-700',
  failed:       'bg-red-100     text-red-700',
  compensating: 'bg-purple-100  text-purple-700',
  stuck:        'bg-red-200     text-red-900',
  cancelled:    'bg-gray-200    text-gray-700',
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
        <h2 className="text-2xl font-bold">Runs</h2>
      </div>
      <p className="text-gray-500 mb-6">
        Durable agent runs (Tape-backed). What the agent decided, attempted,
        confirmed, replayed, compensated, or left UNKNOWN. See the{' '}
        <a className="underline" href="https://github.com/vamsiramakrishnan/aiplex/blob/main/docs-site/docs/guides/tape-runtime.md">
          Tape runtime guide
        </a>{' '}for the wire contract.
      </p>

      <FiltersBar filters={filters} onChange={setFilters} />

      {runs.isLoading && <div className="text-gray-500 py-8">Loading runs…</div>}
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
              <div className="text-gray-400 italic p-6 border rounded">
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
    <div className="flex flex-wrap items-center gap-3 mb-4 p-3 bg-gray-50 rounded">
      <input
        type="text"
        placeholder="tenant_id"
        value={filters.tenant_id}
        onChange={e => onChange({ ...filters, tenant_id: e.target.value })}
        className="px-3 py-1.5 border rounded text-sm"
      />
      <input
        type="text"
        placeholder="agent_id"
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
            <code className="text-xs font-mono text-gray-600 truncate">{r.run_id}</code>
            <div className="flex items-center gap-1">
              {r.compacted && (
                <span
                  className="px-2 py-0.5 text-xs rounded bg-gray-200 text-gray-700"
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
          <div className="text-sm text-gray-700">
            <span className="font-medium">{r.agent_id || '(no agent)'}</span>
            {' · '}
            <span className="text-gray-500">{r.tenant_id || 'no tenant'}</span>
            {' · '}
            <span className="text-gray-500">{r.subject || '(no subject)'}</span>
          </div>
          <div className="flex items-center gap-3 mt-1 text-xs text-gray-500">
            <span>{r.decisions_count} decisions</span>
            <span>{r.effects_count} effects</span>
            {r.unknown_effects > 0 && (
              <span className="text-orange-700">{r.unknown_effects} UNKNOWN</span>
            )}
            {r.obligations > 0 && (
              <span className="text-purple-700">{r.obligations} obligations</span>
            )}
            {r.policy_violations > 0 && (
              <span className="text-red-700">{r.policy_violations} denials</span>
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

// useRunTimelineSSE prefers Server-Sent Events (PR 11 item 11) and
// falls back to React Query polling if SSE doesn't work (older
// browsers, proxies that buffer). Returns the accumulated events
// in seq order.
function useRunTimelineSSE(runID: string): { events: ExecutionEvent[]; live: boolean } {
  const [events, setEvents] = useState<ExecutionEvent[]>([])
  const [live, setLive] = useState(false)
  const seqSeen = useRef<Set<number>>(new Set())

  useEffect(() => {
    setEvents([])
    seqSeen.current = new Set()
    setLive(false)
    if (typeof EventSource === 'undefined') return

    const token = (() => {
      try { return localStorage.getItem('aiplex_token') ?? '' } catch { return '' }
    })()
    const url = `/events/stream?run_id=${encodeURIComponent(runID)}` +
      (token ? `&token=${encodeURIComponent(token)}` : '')
    const es = new EventSource(url)

    const onEvent = (msg: MessageEvent) => {
      try {
        const ev: ExecutionEvent = JSON.parse(msg.data)
        if (seqSeen.current.has(ev.seq)) return
        seqSeen.current.add(ev.seq)
        setEvents(prev => {
          const next = [...prev, ev]
          next.sort((a, b) => a.seq - b.seq)
          return next
        })
      } catch { /* skip malformed */ }
    }
    es.addEventListener('run_event', onEvent as EventListener)
    es.onopen = () => setLive(true)
    es.onerror = () => setLive(false)

    return () => {
      es.removeEventListener('run_event', onEvent as EventListener)
      es.close()
      setLive(false)
    }
  }, [runID])

  // Fallback: poll initial backlog via the read API when SSE hasn't
  // connected within 800ms. Once SSE lands, the live==true path
  // takes over (the polling query stays cached but de-prioritised).
  const pollFallback = useQuery({
    queryKey: ['run-events-fallback', runID],
    queryFn: () => listRunEvents(runID),
    refetchInterval: live ? false : 3000,
    enabled: !live,
  })
  useEffect(() => {
    if (pollFallback.data) {
      setEvents(prev => {
        if (prev.length >= pollFallback.data.events.length) return prev
        const next = [...pollFallback.data.events]
        seqSeen.current = new Set(next.map(e => e.seq))
        return next
      })
    }
  }, [pollFallback.data])

  return { events, live }
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

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['run-audit', runID] })
    queryClient.invalidateQueries({ queryKey: ['runs'] })
  }

  return (
    <div className="border rounded p-3 sticky top-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="font-semibold">Timeline {live && <span className="text-xs text-green-600 ml-2">● live</span>}</h3>
        <code className="text-xs text-gray-500 font-mono truncate max-w-[18ch]">{runID}</code>
      </div>

      {compacted ? (
        <div className="text-xs p-3 mb-3 bg-gray-100 text-gray-700 rounded">
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
        <div className="text-gray-400 italic text-sm">No events yet.</div>
      )}
      <ol className="space-y-1 max-h-[28rem] overflow-y-auto mt-3">
        {events.map(ev => {
          const s = KIND_STYLE[ev.kind] ?? { colour: 'bg-gray-100 text-gray-700', glyph: '·', label: ev.kind }
          return (
            <li key={ev.seq} className={`flex items-start gap-2 text-xs p-2 rounded ${s.colour}`}>
              <span className="font-mono flex-shrink-0 w-5 text-center">{s.glyph}</span>
              <span className="font-mono flex-shrink-0 w-6 text-right">{ev.seq}</span>
              <div className="min-w-0 flex-1">
                <div className="font-medium">{s.label}{ev.tool ? ` · ${ev.tool}` : ''}{ev.scope ? ` · ${ev.scope}` : ''}</div>
                {ev.payload_json && (
                  <div className="font-mono text-[10px] text-gray-600 truncate">
                    {ev.payload_json}
                  </div>
                )}
              </div>
            </li>
          )
        })}
      </ol>

      {auditQ.data && auditQ.data.audit.length > 0 && (
        <div className="mt-4 pt-3 border-t">
          <h4 className="text-xs font-semibold text-gray-700 uppercase tracking-wide mb-2">
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

function OperatorAuditRow({ a }: { a: OperatorAudit }) {
  const colour = a.status === 'accepted' ? 'text-gray-600' : 'text-red-700'
  return (
    <li className={`text-xs ${colour} flex items-baseline gap-2`}>
      <span className="font-mono">{new Date(a.at).toLocaleTimeString()}</span>
      <span className="font-semibold">{a.action}</span>
      <span>by {a.actor}</span>
      {a.reason && <span className="italic">({a.reason})</span>}
      {a.gate_name && <span className="italic">gate={a.gate_name}</span>}
      {a.status === 'failed' && <span className="text-red-700 font-medium">FAILED: {a.error}</span>}
    </li>
  )
}

// Operator action toolbar (PR 11 item 10). Destructive actions
// (cancel, compensate) gated behind a confirm dialog.
function OperatorToolbar({ runID, onActionTaken }: { runID: string; onActionTaken: () => void }) {
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [signalForm, setSignalForm] = useState<{ open: boolean; gate: string; resolution: string }>({
    open: false, gate: '', resolution: '',
  })

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
      onClick: () => {
        const reason = window.prompt('Reason for cancel:')
        if (reason === null) return
        void fire('Cancel', () => runCancel(runID, reason))
      },
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
                ? 'border-red-300 text-red-700 hover:bg-red-50'
                : 'border-gray-300 text-gray-700 hover:bg-gray-50'
            } disabled:opacity-50`}
          >
            {pending === b.label ? '…' : b.label}
          </button>
        ))}
      </div>
      {error && <div className="text-xs text-red-700">{error}</div>}
      {signalForm.open && (
        <div className="p-2 border rounded bg-indigo-50 space-y-2 text-xs">
          <input
            placeholder="gate_name (required)"
            className="block w-full border rounded px-2 py-1"
            value={signalForm.gate}
            onChange={e => setSignalForm({ ...signalForm, gate: e.target.value })}
          />
          <input
            placeholder='resolution_json e.g. {"approved":true}'
            className="block w-full border rounded px-2 py-1 font-mono"
            value={signalForm.resolution}
            onChange={e => setSignalForm({ ...signalForm, resolution: e.target.value })}
          />
          <div className="flex gap-2">
            <button
              className="px-2 py-1 rounded bg-indigo-600 text-white"
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

// EmptyRunsChecklist (PR 11 item 13) — diagnostic checklist explaining
// what's missing for runs to appear. Each row links to the doc that
// fixes it.
function EmptyRunsChecklist() {
  const healthQ = useQuery({
    queryKey: ['runs-health'],
    queryFn: getRunsHealth,
    refetchInterval: 30_000,
  })

  const tapeOK = (healthQ.data?.tape_instances_count ?? 0) > 0
  const ingestRecent = (() => {
    if (!healthQ.data?.last_ingest_at) return false
    const last = new Date(healthQ.data.last_ingest_at).getTime()
    return Date.now() - last < 5 * 60 * 1000
  })()
  const hasRuns = healthQ.data?.has_runs ?? false

  const Row = ({ ok, title, body, href }: { ok: boolean; title: string; body: React.ReactNode; href: string }) => (
    <li className={`flex items-start gap-3 p-3 border rounded ${ok ? 'bg-green-50 border-green-200' : 'bg-white'}`}>
      <span className={`font-mono text-lg flex-shrink-0 ${ok ? 'text-green-700' : 'text-gray-400'}`}>
        {ok ? '✓' : '○'}
      </span>
      <div className="flex-1">
        <div className="font-medium">{title}</div>
        <div className="text-sm text-gray-600">{body}</div>
        {!ok && (
          <a href={href} className="text-sm text-brand-600 underline">How to fix</a>
        )}
      </div>
    </li>
  )

  return (
    <div className="max-w-2xl mx-auto py-8">
      <h3 className="font-medium text-gray-700 mb-2">No runs yet</h3>
      <p className="text-sm text-gray-500 mb-4">
        Three things must be true for runs to appear. The first row tells you
        what's missing.
      </p>
      <ol className="space-y-2">
        <Row
          ok={tapeOK}
          title="At least one Instance with runtime.engine=tape"
          body={`${healthQ.data?.tape_instances_count ?? 0} Tape-backed instance(s) deployed.`}
          href="https://github.com/vamsiramakrishnan/aiplex/blob/main/docs-site/docs/guides/tape-runtime.md"
        />
        <Row
          ok={ingestRecent}
          title="Tape ingestion seen recently"
          body={
            healthQ.data?.last_ingest_at
              ? `Last event at ${new Date(healthQ.data.last_ingest_at).toLocaleString()}.`
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
    <div className="text-center py-12 px-6 border-2 border-dashed border-gray-200 rounded">
      <h3 className="font-medium text-gray-700">{title}</h3>
      <p className="text-sm text-gray-500 mt-2 max-w-md mx-auto">{body}</p>
    </div>
  )
}
