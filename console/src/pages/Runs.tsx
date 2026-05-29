import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  ExecutionEventKind,
  ExecutionRun,
  ExecutionRunStatus,
  listRuns,
  listRunEvents,
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
      {runs.data && runs.data.runs.length === 0 && <EmptyRunsState />}

      {runs.data && runs.data.runs.length > 0 && (
        <div className="grid grid-cols-12 gap-4">
          <div className="col-span-7">
            <RunList runs={runs.data.runs} selected={selected} onSelect={setSelected} />
          </div>
          <div className="col-span-5">
            {selected ? (
              <RunDetail runID={selected} />
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
            <span className={`px-2 py-0.5 text-xs rounded ${STATUS_STYLE[r.status]}`}>
              {r.status}
            </span>
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

function RunDetail({ runID }: { runID: string }) {
  const events = useQuery({
    queryKey: ['run-events', runID],
    queryFn: () => listRunEvents(runID),
    refetchInterval: 3_000,
  })
  return (
    <div className="border rounded p-3 sticky top-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="font-semibold">Timeline</h3>
        <code className="text-xs text-gray-500 font-mono truncate max-w-[18ch]">{runID}</code>
      </div>
      {events.isLoading && <div className="text-gray-500 text-sm">Loading…</div>}
      {events.data && events.data.events.length === 0 && (
        <div className="text-gray-400 italic text-sm">No events yet.</div>
      )}
      <ol className="space-y-1 max-h-[28rem] overflow-y-auto">
        {events.data?.events.map(ev => {
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
    </div>
  )
}

function EmptyRunsState() {
  return (
    <EmptyState
      title="No runs yet"
      body={
        <>
          Enable the Tape runtime on an Instance — set{' '}
          <code className="font-mono text-xs">runtime: {'{ engine: tape }'}</code> on its
          deploy config — to see durable execution timelines here. The{' '}
          <a className="underline" href="https://github.com/vamsiramakrishnan/aiplex/blob/main/examples/durable-tape-runtime.yaml">
            example YAML
          </a>{' '}walks through the contract.
        </>
      }
    />
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
