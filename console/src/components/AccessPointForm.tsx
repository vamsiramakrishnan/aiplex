import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  CALLER_KINDS, parseFieldPath, surfaceOptionLabel, surfaceUnroutableReason, wiresForProtocol, WIRES, listAgents, listClientRegistrations, listModelSurfaces, listToolConnections, parseList,
  type AccessPointSpec, type CallerKind, type FieldIssue, type GroupRef, type QoSScope,
} from '../api/accessPoints'
import { listInstances } from '../api/client'

const input = 'w-full border rounded px-2 py-1 text-sm bg-transparent border-gray-300 dark:border-gray-700'
const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800'
const ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/
const CLASS_PATTERN = /^[a-z][a-z0-9-]{0,62}$/

interface ModelRow { name: string; surface_ref: string; native_model: string }
interface ToolRow { connection_ref: string; expose: string }
interface Draft {
  id: string
  display_name: string
  caller_kinds: CallerKind[]
  principals: string
  groups: GroupRef[]
  agents: string[]
  registrations: string[]
  models: ModelRow[]
  tools: ToolRow[]
  skills: string[]
  qosOn: boolean
  qosClass: string
  qosWeight: string
  qosScope: '' | QoSScope
  rpm: string
  tpm: string
  concurrency: string
  budgets: string
}

export function draftFromSpec(spec: AccessPointSpec): Draft {
  return {
    id: spec.id,
    display_name: spec.display_name ?? '',
    caller_kinds: [...spec.caller_kinds],
    principals: (spec.subjects.principals ?? []).join('\n'),
    groups: (spec.subjects.groups ?? []).map(g => ({ ...g })),
    agents: [...(spec.subjects.agents ?? [])],
    registrations: [...(spec.subjects.client_registrations ?? [])],
    models: (spec.models ?? []).map(m => ({ ...m })),
    tools: (spec.tools ?? []).map(t => ({ connection_ref: t.connection_ref, expose: (t.expose ?? []).join('\n') })),
    skills: (spec.skills ?? []).map(s => s.instance_ref),
    qosOn: !!spec.qos,
    qosClass: spec.qos?.class ?? '',
    qosWeight: spec.qos?.weight ? String(spec.qos.weight) : '',
    qosScope: spec.qos?.scope ?? '',
    rpm: spec.qos?.limits?.requests_per_minute ? String(spec.qos.limits.requests_per_minute) : '',
    tpm: spec.qos?.limits?.tokens_per_minute ? String(spec.qos.limits.tokens_per_minute) : '',
    concurrency: spec.qos?.limits?.concurrency ? String(spec.qos.limits.concurrency) : '',
    budgets: (spec.qos?.budget_refs ?? []).join('\n'),
  }
}

const num = (s: string) => (s.trim() ? Number(s) : undefined)

/** Applies the draft over the original spec so fields this form does not know are preserved. */
export function specFromDraft(d: Draft, original?: AccessPointSpec): AccessPointSpec {
  const subjects: AccessPointSpec['subjects'] = { ...(original?.subjects ?? {}) }
  const set = <K extends keyof AccessPointSpec['subjects']>(key: K, value: AccessPointSpec['subjects'][K]) => {
    if (value && (value as unknown[]).length) subjects[key] = value
    else delete subjects[key]
  }
  set('principals', parseList(d.principals))
  set('groups', d.groups.filter(g => g.source.trim() || g.id.trim()).map(g => ({ source: g.source.trim(), id: g.id.trim() })))
  set('agents', d.agents)
  set('client_registrations', d.registrations)
  const spec: AccessPointSpec = {
    ...original,
    id: d.id.trim(),
    caller_kinds: CALLER_KINDS.filter(k => d.caller_kinds.includes(k)),
    subjects,
    models: d.models.map(m => ({ name: m.name.trim(), surface_ref: m.surface_ref, native_model: m.native_model.trim() })),
    tools: d.tools.map(t => { const expose = parseList(t.expose); return { connection_ref: t.connection_ref, ...(expose.length ? { expose } : {}) } }),
    skills: d.skills.map(s => ({ instance_ref: s })),
  }
  if (d.display_name.trim()) spec.display_name = d.display_name.trim(); else delete spec.display_name
  if (d.qosOn) {
    const limits = { requests_per_minute: num(d.rpm), tokens_per_minute: num(d.tpm), concurrency: num(d.concurrency) }
    const present = Object.fromEntries(Object.entries(limits).filter(([, v]) => v !== undefined))
    const budgets = parseList(d.budgets)
    spec.qos = {
      ...original?.qos,
      class: d.qosClass.trim(),
      ...(num(d.qosWeight) !== undefined ? { weight: num(d.qosWeight) } : {}),
      ...(d.qosScope ? { scope: d.qosScope } : {}),
      ...(Object.keys(present).length ? { limits: present } : {}),
      ...(budgets.length ? { budget_refs: budgets } : {}),
    }
    if (num(d.qosWeight) === undefined) delete spec.qos.weight
    if (!d.qosScope) delete spec.qos.scope
    if (!Object.keys(present).length) delete spec.qos.limits
    if (!budgets.length) delete spec.qos.budget_refs
  } else {
    delete spec.qos
  }
  return spec
}

function FieldError({ id, message }: { id: string; message?: string }) {
  return message ? <p id={id} role="alert" className="text-xs text-status-denied-fg mt-1">{message}</p> : null
}

function Section({ title, hint, error, errorId, children }: { title: string; hint?: ReactNode; error?: string; errorId: string; children: ReactNode }) {
  return (
    <fieldset className="border border-gray-200 dark:border-gray-800 rounded p-3 space-y-2" aria-describedby={error ? errorId : undefined}>
      <legend className="text-sm font-semibold px-1">{title}</legend>
      {hint && <p className="text-xs text-gray-600 dark:text-gray-400">{hint}</p>}
      {children}
      <FieldError id={errorId} message={error} />
    </fieldset>
  )
}

/** Checkbox list of picker options; ids that no longer exist stay checked and are labelled so. */
function CheckList({ legend, options, selected, onChange, loading, error, empty }: {
  legend: string
  options: { id: string; label: string }[]
  selected: string[]
  onChange: (next: string[]) => void
  loading: boolean
  error?: Error | null
  empty: string
}) {
  if (loading) return <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Loading {legend.toLowerCase()}…</p>
  if (error) return <p role="alert" className="text-xs text-status-denied-fg">Could not load {legend.toLowerCase()}: {error.message}</p>
  const known = new Set(options.map(o => o.id))
  const all = [...options, ...selected.filter(s => !known.has(s)).map(s => ({ id: s, label: `${s} (not found)` }))]
  if (all.length === 0) return <p className="text-xs text-gray-600 dark:text-gray-400">{empty}</p>
  const toggle = (id: string) => onChange(selected.includes(id) ? selected.filter(s => s !== id) : [...selected, id])
  return (
    <div role="group" aria-label={legend} className="flex flex-wrap gap-x-4 gap-y-1">
      {all.map(o => (
        <label key={o.id} className="flex items-center gap-1 text-sm">
          <input type="checkbox" checked={selected.includes(o.id)} onChange={() => toggle(o.id)} /> {o.label}
        </label>
      ))}
    </div>
  )
}

export interface AccessPointFormProps {
  /** Existing spec when editing; omitted to create. */
  initial?: AccessPointSpec
  saving: boolean
  /** Server error for the last save attempt (non-conflict): its message and the field it names. */
  serverError?: FieldIssue
  /** Disables saving while a revision conflict is unresolved. */
  locked?: boolean
  onSubmit: (spec: AccessPointSpec) => void
  onCancel: () => void
}

export default function AccessPointForm({ initial, saving, serverError, locked, onSubmit, onCancel }: AccessPointFormProps) {
  const uid = useId()
  const editing = !!initial
  const [d, setD] = useState<Draft>(() => draftFromSpec(initial ?? { id: '', caller_kinds: ['user'], subjects: {} }))
  const patch = (p: Partial<Draft>) => setD(cur => ({ ...cur, ...p }))

  const surfaces = useQuery({ queryKey: ['ref-model-surfaces'], queryFn: listModelSurfaces })
  const connections = useQuery({ queryKey: ['ref-tool-connections'], queryFn: listToolConnections })
  const registrations = useQuery({ queryKey: ['ref-client-registrations'], queryFn: listClientRegistrations })
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents })
  const skillInstances = useQuery({ queryKey: ['ref-skill-instances'], queryFn: () => listInstances('skillsplex') })

  const managedConnections = (connections.data ?? []).filter(c => !!c.spec.execution && Object.keys(c.spec.execution).length > 0)

  // The server names the field in details.field (for example models/1/surface_ref); show the
  // message there, and at the form level when it names none.
  const where = parseFieldPath(serverError?.field)
  const rowMsg = (section: string, index: number, leaf?: string) => (where?.section === section && where.index === index && (leaf === undefined || where.leaf === leaf) ? serverError?.message : undefined)
  const sectionMsg = (section: string) => (where?.section === section ? serverError?.message : undefined)
  const wholeSectionMsg = (section: string) => (where?.section === section && where.index === undefined ? serverError?.message : undefined)
  const serverField = where?.section
  const formLevel = serverError && !where ? serverError.message : undefined

  const idValid = ID_PATTERN.test(d.id)
  const kindsOk = d.caller_kinds.length > 0
  const qosOk = !d.qosOn || CLASS_PATTERN.test(d.qosClass)
  const modelsOk = d.models.every(m => m.name.trim() && m.surface_ref && m.native_model.trim() && !(surfaces.data?.some(x => x.id === m.surface_ref && surfaceUnroutableReason(x.spec.surface))))
  const toolsOk = d.tools.every(t => t.connection_ref)
  const resources = d.models.length + d.tools.length + d.skills.length
  const canSave = idValid && kindsOk && qosOk && modelsOk && toolsOk && resources > 0 && !saving && !locked

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (canSave) onSubmit(specFromDraft(d, initial))
  }
  const surfaceModels = (ref: string) => surfaces.data?.find(s => s.id === ref)?.spec.surface.models ?? []
  const updateModel = (i: number, p: Partial<ModelRow>) => patch({ models: d.models.map((m, j) => (j === i ? { ...m, ...p } : m)) })
  const updateTool = (i: number, p: Partial<ToolRow>) => patch({ tools: d.tools.map((t, j) => (j === i ? { ...t, ...p } : t)) })
  const updateGroup = (i: number, p: Partial<GroupRef>) => patch({ groups: d.groups.map((g, j) => (j === i ? { ...g, ...p } : g)) })

  return (
    <form onSubmit={submit} aria-label={editing ? `Edit access point ${initial!.id}` : 'Create access point'} className="space-y-4 max-w-3xl">
      <div className="grid gap-3 sm:grid-cols-2">
        <div>
          <label htmlFor={`${uid}-id`} className="block text-xs font-medium mb-1">Access point ID <span aria-hidden="true" className="text-status-denied-fg">*</span></label>
          <input id={`${uid}-id`} value={d.id} required aria-required="true" readOnly={editing}
            aria-invalid={(!!d.id && !idValid) || serverField === 'id'} aria-describedby={`${uid}-id-err`}
            onChange={e => patch({ id: e.target.value })} placeholder="ide-platform-team" className={`${input} font-mono ${editing ? 'opacity-70' : ''}`} />
          <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">Becomes the URL prefix <code>/ap/{d.id || '{id}'}</code>. Cannot be changed after creation.</p>
          {d.id && !idValid && <p role="alert" className="text-xs text-status-denied-fg">Use letters, digits, dots, hyphens and underscores, starting with a letter or digit.</p>}
          <FieldError id={`${uid}-id-err`} message={sectionMsg('id')} />
        </div>
        <div>
          <label htmlFor={`${uid}-name`} className="block text-xs font-medium mb-1">Display name</label>
          <input id={`${uid}-name`} value={d.display_name} onChange={e => patch({ display_name: e.target.value })} aria-invalid={serverField === 'display_name'} aria-describedby={`${uid}-name-err`} className={input} />
          <FieldError id={`${uid}-name-err`} message={sectionMsg('display_name')} />
        </div>
      </div>

      <Section title="Caller kinds" errorId={`${uid}-kinds-err`} error={sectionMsg('caller_kinds') ?? (!kindsOk ? 'Choose at least one caller kind.' : undefined)}
        hint="Which kinds of caller may use this access point. The kind is derived from the verified identity, never from the request.">
        <div className="flex gap-4">
          {CALLER_KINDS.map(k => (
            <label key={k} className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={d.caller_kinds.includes(k)}
                onChange={() => patch({ caller_kinds: d.caller_kinds.includes(k) ? d.caller_kinds.filter(x => x !== k) : [...d.caller_kinds, k] })} /> {k}
            </label>
          ))}
        </div>
      </Section>

      <Section title="Who may call (subject constraints)" errorId={`${uid}-subj-err`}
        error={sectionMsg('subjects')}
        hint={<>Leave everything empty to admit every caller of an allowed kind. <strong>Principals, groups and agents are one identity constraint</strong>: a caller matching any one of them passes it. <strong>Client registrations are a separate constraint</strong>: when both are set, a caller must satisfy <strong>both</strong> (they are AND-ed). A registration identified only by a public OAuth client id is attribution, not a security boundary.</>}>
        <div>
          <label htmlFor={`${uid}-principals`} className="block text-xs font-medium mb-1">Principals (one per line)</label>
          <textarea id={`${uid}-principals`} rows={2} value={d.principals} onChange={e => patch({ principals: e.target.value })} className={`${input} font-mono`} />
          <p className="text-xs text-gray-600 dark:text-gray-400">Canonical principal IDs. There is no directory to pick from, so these are typed.</p>
        </div>
        <div>
          <span className="block text-xs font-medium mb-1">Groups (source and ID)</span>
          {d.groups.map((g, i) => (
            <div key={i} className="flex gap-2 mb-1">
              <input aria-label={`Group ${i + 1} source`} value={g.source} onChange={e => updateGroup(i, { source: e.target.value })} placeholder="source" className={`${input} font-mono`} />
              <input aria-label={`Group ${i + 1} ID`} value={g.id} onChange={e => updateGroup(i, { id: e.target.value })} placeholder="group id" className={`${input} font-mono`} />
              <button type="button" className={btn} aria-label={`Remove group ${i + 1}`} onClick={() => patch({ groups: d.groups.filter((_, j) => j !== i) })}>Remove</button>
            </div>
          ))}
          <button type="button" className={btn} onClick={() => patch({ groups: [...d.groups, { source: '', id: '' }] })}>Add group</button>
        </div>
        <div>
          <span className="block text-xs font-medium mb-1">Agents</span>
          <CheckList legend="Agents" selected={d.agents} onChange={a => patch({ agents: a })} loading={agents.isLoading} error={agents.error}
            options={(agents.data ?? []).map(a => ({ id: a.client_id, label: a.display_name && a.display_name !== a.client_id ? `${a.display_name} (${a.client_id})` : a.client_id }))}
            empty="No agents are registered." />
        </div>
        <div>
          <span className="block text-xs font-medium mb-1">Client registrations (AND-ed with the identity constraint above)</span>
          <CheckList legend="Client registrations" selected={d.registrations} onChange={r => patch({ registrations: r })} loading={registrations.isLoading} error={registrations.error}
            options={(registrations.data ?? []).map(r => ({ id: r.id, label: r.spec.profile_id ? `${r.id} (${r.spec.profile_id})` : r.id }))}
            empty="No client registrations exist." />
        </div>
      </Section>

      <Section title="Models" errorId={`${uid}-models-err`} error={wholeSectionMsg('models')}
        hint={`Each entry maps a client-facing model name to a saved model surface and the provider-native model. A model is reachable only through the wire protocols its surface speaks natively. Wires an access point can serve: ${WIRES.map(w => w.label).join(', ')}.`}>
        {surfaces.isLoading && <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Loading model surfaces…</p>}
        {surfaces.error && <p role="alert" className="text-xs text-status-denied-fg">Could not load model surfaces: {(surfaces.error as Error).message}</p>}
        {d.models.map((m, i) => (
          <div key={i} role="group" aria-label={`Model ${i + 1}`} className="grid gap-2 sm:grid-cols-[1fr,1fr,1fr,auto] items-end">
            <div>
              <label htmlFor={`${uid}-m${i}-surface`} className="block text-xs mb-1">Model surface</label>
              <select id={`${uid}-m${i}-surface`} aria-invalid={!!rowMsg('models', i, 'surface_ref')} value={m.surface_ref} onChange={e => updateModel(i, { surface_ref: e.target.value })} className={input}>
                <option value="">Choose a surface…</option>
                {(surfaces.data ?? []).map(s => <option key={s.id} value={s.id} disabled={!!surfaceUnroutableReason(s.spec.surface)}>{surfaceOptionLabel(s.spec.surface)}</option>)}
                {m.surface_ref && !(surfaces.data ?? []).some(s => s.id === m.surface_ref) && <option value={m.surface_ref}>{m.surface_ref} (not found)</option>}
              </select>
            </div>
            <div>
              <label htmlFor={`${uid}-m${i}-native`} className="block text-xs mb-1">Native model</label>
              <input id={`${uid}-m${i}-native`} aria-invalid={!!rowMsg('models', i, 'native_model')} list={`${uid}-m${i}-list`} value={m.native_model} onChange={e => updateModel(i, { native_model: e.target.value, ...(m.name === '' || m.name === m.native_model ? { name: e.target.value } : {}) })} className={`${input} font-mono`} />
              <datalist id={`${uid}-m${i}-list`}>{surfaceModels(m.surface_ref).filter(x => !x.includes('*')).map(x => <option key={x} value={x} />)}</datalist>
            </div>
            <div>
              <label htmlFor={`${uid}-m${i}-name`} className="block text-xs mb-1">Name clients use</label>
              <input id={`${uid}-m${i}-name`} aria-invalid={!!rowMsg('models', i, 'name')} value={m.name} onChange={e => updateModel(i, { name: e.target.value })} className={`${input} font-mono`} />
            </div>
            {(() => { const surf = surfaces.data?.find(x => x.id === m.surface_ref); return surf && !surfaceUnroutableReason(surf.spec.surface) ? <p className="text-xs text-gray-600 dark:text-gray-400 sm:col-span-4">Reachable through: {wiresForProtocol(surf.spec.surface.protocol).map(w => w.label).join(', ')}.</p> : null })()}
            {(() => { const surf = surfaces.data?.find(x => x.id === m.surface_ref); const why = surf && surfaceUnroutableReason(surf.spec.surface); return why ? <p role="alert" className="text-xs text-status-denied-fg sm:col-span-4">Surface {m.surface_ref} is unavailable for access points: {why}. Choose another surface.</p> : null })()}
            {rowMsg('models', i) && <p role="alert" className="text-xs text-status-denied-fg sm:col-span-4">{rowMsg('models', i)}</p>}
            <button type="button" className={btn} aria-label={`Remove model ${i + 1}`} onClick={() => patch({ models: d.models.filter((_, j) => j !== i) })}>Remove</button>
          </div>
        ))}
        <button type="button" className={btn} onClick={() => patch({ models: [...d.models, { name: '', surface_ref: '', native_model: '' }] })}>Add model</button>
      </Section>

      <Section title="Tools" errorId={`${uid}-tools-err`} error={wholeSectionMsg('tools')}
        hint="Expose saved tool connections (managed execution connections only). Expose patterns narrow the tools: names or prefixes ending in *, one per line. Empty exposes every tool the connection itself permits.">
        {connections.isLoading && <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Loading tool connections…</p>}
        {connections.error && <p role="alert" className="text-xs text-status-denied-fg">Could not load tool connections: {(connections.error as Error).message}</p>}
        {d.tools.map((t, i) => {
          const taken = new Set(d.tools.filter((_, j) => j !== i).map(x => x.connection_ref))
          return (
            <div key={i} role="group" aria-label={`Tool ${i + 1}`} className="grid gap-2 sm:grid-cols-[1fr,2fr,auto] items-start">
              <div>
                <label htmlFor={`${uid}-t${i}-conn`} className="block text-xs mb-1">Tool connection</label>
                <select id={`${uid}-t${i}-conn`} aria-invalid={!!rowMsg('tools', i, 'connection_ref')} value={t.connection_ref} onChange={e => updateTool(i, { connection_ref: e.target.value })} className={input}>
                  <option value="">Choose a connection…</option>
                  {managedConnections.filter(c => !taken.has(c.id)).map(c => <option key={c.id} value={c.id}>{c.id}</option>)}
                  {t.connection_ref && !managedConnections.some(c => c.id === t.connection_ref) && <option value={t.connection_ref}>{t.connection_ref} (not found)</option>}
                </select>
              </div>
              <div>
                <label htmlFor={`${uid}-t${i}-expose`} className="block text-xs mb-1">Expose patterns (optional)</label>
                <textarea id={`${uid}-t${i}-expose`} rows={2} value={t.expose} onChange={e => updateTool(i, { expose: e.target.value })} placeholder="search*" className={`${input} font-mono`} />
              </div>
              {rowMsg('tools', i) && <p role="alert" className="text-xs text-status-denied-fg sm:col-span-3">{rowMsg('tools', i)}</p>}
              <button type="button" className={`${btn} mt-5`} aria-label={`Remove tool ${i + 1}`} onClick={() => patch({ tools: d.tools.filter((_, j) => j !== i) })}>Remove</button>
            </div>
          )
        })}
        <button type="button" className={btn} onClick={() => patch({ tools: [...d.tools, { connection_ref: '', expose: '' }] })}>Add tool connection</button>
      </Section>

      <Section title="Skills" errorId={`${uid}-skills-err`} error={sectionMsg('skills')} hint="Deployed skill servers. Each must carry the aiplex.tenant label for this tenant.">
        <CheckList legend="Skill instances" selected={d.skills} onChange={s => patch({ skills: s })} loading={skillInstances.isLoading} error={skillInstances.error}
          options={(skillInstances.data ?? []).map(i => ({ id: i.id, label: i.display_name && i.display_name !== i.id ? `${i.display_name} (${i.id})` : i.id }))}
          empty="No skill instances are deployed." />
      </Section>
            {resources === 0 && <p className="text-xs text-gray-600 dark:text-gray-400">Add at least one model, tool connection or skill.</p>}

      <Section title="Quality of service" errorId={`${uid}-qos-err`}
        error={sectionMsg('qos')}
        hint="A QoS block is rendered into a QuotaPlex policy fragment for review. With a QoS block, requests are rejected until a matching quota binding is active.">
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={d.qosOn} onChange={e => patch({ qosOn: e.target.checked })} /> Set a QoS class for this access point</label>
        {d.qosOn && (
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label htmlFor={`${uid}-qclass`} className="block text-xs mb-1">Class <span aria-hidden="true" className="text-status-denied-fg">*</span></label>
              <input id={`${uid}-qclass`} value={d.qosClass} required aria-required="true" aria-invalid={!!d.qosClass && !CLASS_PATTERN.test(d.qosClass)} onChange={e => patch({ qosClass: e.target.value })} placeholder="interactive" className={`${input} font-mono`} />
              {d.qosClass && !CLASS_PATTERN.test(d.qosClass) && <p role="alert" className="text-xs text-status-denied-fg">Lower-case letters, digits and hyphens, starting with a letter.</p>}
            </div>
            <div>
              <label htmlFor={`${uid}-qweight`} className="block text-xs mb-1">Weight (1-1000)</label>
              <input id={`${uid}-qweight`} type="number" min={1} max={1000} value={d.qosWeight} onChange={e => patch({ qosWeight: e.target.value })} className={input} />
            </div>
            <div>
              <label htmlFor={`${uid}-qscope`} className="block text-xs mb-1">Limit scope</label>
              <select id={`${uid}-qscope`} value={d.qosScope} onChange={e => patch({ qosScope: e.target.value as '' | QoSScope })} className={input}>
                <option value="">Server default</option>
                <option value="principal">Per caller: each principal gets the full limits</option>
                <option value="access_point">Shared pool: every caller draws from one set of limits</option>
              </select>
              <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">Applies to the rate and concurrency limits.</p>
            </div>
            <div>
              <label htmlFor={`${uid}-rpm`} className="block text-xs mb-1">Requests per minute</label>
              <input id={`${uid}-rpm`} type="number" min={1} value={d.rpm} onChange={e => patch({ rpm: e.target.value })} className={input} />
            </div>
            <div>
              <label htmlFor={`${uid}-tpm`} className="block text-xs mb-1">Tokens per minute</label>
              <input id={`${uid}-tpm`} type="number" min={1} value={d.tpm} onChange={e => patch({ tpm: e.target.value })} className={input} />
            </div>
            <div>
              <label htmlFor={`${uid}-conc`} className="block text-xs mb-1">Concurrency</label>
              <input id={`${uid}-conc`} type="number" min={1} value={d.concurrency} onChange={e => patch({ concurrency: e.target.value })} className={input} />
            </div>
            <div>
              <label htmlFor={`${uid}-budgets`} className="block text-xs mb-1">Budget references (one per line)</label>
              <textarea id={`${uid}-budgets`} rows={2} value={d.budgets} onChange={e => patch({ budgets: e.target.value })} className={`${input} font-mono`} />
              <p className="text-xs text-gray-600 dark:text-gray-400">IDs of budgets in the tenant&apos;s quota policy; the server checks they exist.</p>
            </div>
          </div>
        )}
      </Section>

      {formLevel && <p role="alert" className="text-sm text-status-denied-fg">{formLevel}</p>}
      <div className="flex gap-2">
        <button type="submit" disabled={!canSave} className="px-4 py-2 text-sm bg-brand-600 text-white rounded disabled:opacity-50">{saving ? 'Saving…' : editing ? 'Save changes' : 'Create access point'}</button>
        <button type="button" onClick={onCancel} className={btn}>Cancel</button>
      </div>
    </form>
  )
}
