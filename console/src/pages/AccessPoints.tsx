import { useState } from 'react'
import { Link, Route, Routes, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  deleteAccessPoint, errorStatus, getAccessPoint, isRevisionConflict, listAccessPoints, listModelSurfaces, putAccessPoint,
  type AccessPointRecord, type AccessPointSpec,
} from '../api/accessPoints'
import AccessPointForm from '../components/AccessPointForm'
import AccessPointSummaryCard from '../components/AccessPointSummaryCard'
import AccessPointUsagePanel from '../components/AccessPointUsagePanel'
import CopyButton from '../components/CopyButton'
import EndpointList from '../components/EndpointList'
import QuotaPlanPanel from '../components/QuotaPlanPanel'
import SubjectTable, { type SubjectRow } from '../components/SubjectTable'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800'
const primary = 'px-3 py-1.5 text-sm bg-brand-600 text-white rounded disabled:opacity-50'

export const accessPointBase = (id: string, origin: string = window.location.origin) => `${origin.replace(/\/$/, '')}/ap/${id}`

function LoadError({ error, what, onRetry }: { error: unknown; what: string; onRetry?: () => void }) {
  const status = errorStatus(error)
  return (
    <div role="alert" className="rounded border border-red-300 dark:border-red-800 p-3 text-sm text-red-700 dark:text-red-400">
      {status === 403 ? <p>You do not have access to {what}.</p>
        : status === 404 ? <p>{what[0].toUpperCase() + what.slice(1)} not found. It may have been deleted.</p>
        : <p>Could not load {what}: {error instanceof Error ? error.message : String(error)}</p>}
      {status !== 403 && onRetry && <button type="button" className="mt-2 underline" onClick={onRetry}>Retry</button>}
    </div>
  )
}

// ---- list ------------------------------------------------------------------

function AccessPointList() {
  const list = useQuery({ queryKey: ['access-points'], queryFn: listAccessPoints })
  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-2xl font-bold">Access points</h2>
          <p className="text-gray-500">One stable URL per team, tool or client that enforces who can reach which models, tools and skills.</p>
        </div>
        <Link to="/access-points/new" className={primary}>Create access point</Link>
      </div>
      {list.isLoading && <p role="status">Loading access points…</p>}
      {list.isError && <LoadError error={list.error} what="access points" onRetry={() => void list.refetch()} />}
      {list.data && list.data.length === 0 && (
        <p className="text-gray-500">No access points yet. Create one to give IDEs, agents and users a single URL for models and tools.</p>
      )}
      {!!list.data?.length && (
        <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label="Access points">
          {list.data.map(r => (
            <li key={r.id}>
              <AccessPointSummaryCard spec={r.spec} revision={r.revision} footer={
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <code className="font-mono text-xs break-all">{accessPointBase(r.id)}</code>
                    <CopyButton text={accessPointBase(r.id)} label={`base URL of ${r.id}`} />
                  </div>
                  <Link to={`/access-points/${encodeURIComponent(r.id)}`} className="text-sm text-brand-600 hover:underline" aria-label={`Open ${r.spec.display_name || r.id}`}>Open</Link>
                </div>
              } />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ---- create / edit ---------------------------------------------------------

function AccessPointEditor() {
  const { id } = useParams()
  const editing = !!id
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [conflict, setConflict] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [formKey, setFormKey] = useState(0)

  // The form is keyed by formKey and the record never refetches by itself, so a
  // background refresh cannot replace what the user is typing.
  const record = useQuery({ queryKey: ['access-point', id], queryFn: () => getAccessPoint(id!), enabled: editing, staleTime: Infinity, refetchOnWindowFocus: false })

  const save = useMutation({
    mutationFn: (spec: AccessPointSpec) => putAccessPoint(spec, record.data?.revision ?? 0),
    onSuccess: (saved) => {
      queryClient.setQueryData(['access-point', saved.id], saved)
      void queryClient.invalidateQueries({ queryKey: ['access-points'] })
      navigate(`/access-points/${encodeURIComponent(saved.id)}`)
    },
    onError: (e) => {
      if (isRevisionConflict(e)) {
        setError('')
        setConflict(editing
          ? 'This access point was changed by someone else since you opened it. Your edits were not saved and nothing was overwritten. Reload the latest version to continue (your unsaved edits will be discarded).'
          : 'An access point with this ID already exists. Choose a different ID, or open the existing one to edit it.')
      } else {
        setConflict(null)
        setError(e instanceof Error ? e.message : 'Could not save the access point.')
      }
    },
  })

  const reload = async () => {
    try {
      const latest = await getAccessPoint(id!)
      queryClient.setQueryData(['access-point', id], latest)
      setConflict(null); setError(''); save.reset(); setFormKey(k => k + 1)
    } catch (e) {
      setConflict(errorStatus(e) === 404 ? 'This access point no longer exists; it was deleted by someone else.' : `Could not reload: ${e instanceof Error ? e.message : String(e)}`)
    }
  }

  const title = editing ? `Edit ${id}` : 'Create access point'
  return (
    <div>
      <p className="mb-2 text-sm"><Link to={editing ? `/access-points/${encodeURIComponent(id!)}` : '/access-points'} className="text-brand-600 hover:underline">← Back</Link></p>
      <h2 className="text-2xl font-bold mb-4">{title}</h2>
      {editing && record.isLoading && <p role="status">Loading access point…</p>}
      {editing && record.isError && <LoadError error={record.error} what="this access point" onRetry={() => void record.refetch()} />}
      {conflict && (
        <div role="alert" className="mb-4 rounded border border-amber-400 bg-amber-50 dark:bg-amber-950 p-3 text-sm text-amber-900 dark:text-amber-200">
          <p>{conflict}</p>
          {editing && <button type="button" className={`${btn} mt-2`} onClick={() => void reload()}>Reload latest version</button>}
        </div>
      )}
      {(!editing || record.data) && (
        <AccessPointForm
          key={formKey}
          initial={editing ? record.data!.spec : undefined}
          saving={save.isPending}
          serverError={error || undefined}
          locked={!!conflict && editing}
          onSubmit={(spec) => { setConflict(null); setError(''); save.mutate(spec) }}
          onCancel={() => navigate(editing ? `/access-points/${encodeURIComponent(id!)}` : '/access-points')}
        />
      )}
    </div>
  )
}

// ---- detail ----------------------------------------------------------------

function subjectRows(spec: AccessPointSpec): SubjectRow[] {
  const s = spec.subjects
  return [
    ...(s.principals ?? []).map((id): SubjectRow => ({ kind: 'principal', id })),
    ...(s.groups ?? []).map((g): SubjectRow => ({ kind: 'group', id: g.id, detail: `source ${g.source}` })),
    ...(s.agents ?? []).map((id): SubjectRow => ({ kind: 'agent', id })),
    ...(s.client_registrations ?? []).map((id): SubjectRow => ({ kind: 'registration', id, detail: 'AND-ed with the identity constraint' })),
  ]
}

function AccessPointDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const record = useQuery({ queryKey: ['access-point', id], queryFn: () => getAccessPoint(id) })
  const surfaces = useQuery({ queryKey: ['ref-model-surfaces'], queryFn: listModelSurfaces })
  const [confirming, setConfirming] = useState(false)
  const [conflict, setConflict] = useState(false)

  const remove = useMutation({
    mutationFn: (r: AccessPointRecord) => deleteAccessPoint(r.id, r.revision),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: ['access-point', id] })
      void queryClient.invalidateQueries({ queryKey: ['access-points'] })
      navigate('/access-points')
    },
    onError: (e) => { if (isRevisionConflict(e)) setConflict(true) },
  })

  if (record.isLoading) return <p role="status">Loading access point…</p>
  if (record.isError) return <LoadError error={record.error} what="this access point" onRetry={() => void record.refetch()} />
  const r = record.data!
  const spec = r.spec
  const rows = subjectRows(spec)

  return (
    <div className="space-y-8">
      <div>
        <p className="mb-2 text-sm"><Link to="/access-points" className="text-brand-600 hover:underline">← All access points</Link></p>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-2xl font-bold">{spec.display_name || spec.id}</h2>
          <div className="flex gap-2">
            <Link to={`/access-points/${encodeURIComponent(id)}/edit`} className={btn}>Edit</Link>
            <button type="button" className={`${btn} text-red-600`} onClick={() => setConfirming(true)} disabled={confirming}>Delete</button>
          </div>
        </div>
      </div>

      {confirming && (
        <div role="alertdialog" aria-label={`Confirm delete of ${spec.id}`} className="rounded border border-red-300 dark:border-red-800 p-3 text-sm">
          <p>Delete access point <strong>{spec.id}</strong>? Clients using <code>/ap/{spec.id}</code> will stop working. This cannot be undone.</p>
          <div className="mt-2 flex gap-2">
            <button type="button" className="px-3 py-1.5 text-sm bg-red-600 text-white rounded disabled:opacity-50" disabled={remove.isPending} onClick={() => remove.mutate(r)}>
              {remove.isPending ? 'Deleting…' : 'Confirm delete'}
            </button>
            <button type="button" className={btn} onClick={() => { setConfirming(false); setConflict(false); remove.reset() }}>Cancel</button>
          </div>
        </div>
      )}
      {conflict && (
        <div role="alert" className="rounded border border-amber-400 bg-amber-50 dark:bg-amber-950 p-3 text-sm text-amber-900 dark:text-amber-200">
          <p>This access point was changed by someone else, so it was not deleted. Reload to review the latest version before deleting.</p>
          <button type="button" className={`${btn} mt-2`} onClick={() => { setConflict(false); setConfirming(false); remove.reset(); void record.refetch() }}>Reload latest version</button>
        </div>
      )}
      {remove.isError && !conflict && (
        <p role="alert" className="text-sm text-red-600">{errorStatus(remove.error) === 403 ? 'Deleting access points requires the admin role.' : `Could not delete: ${(remove.error as Error).message}`}</p>
      )}

      <AccessPointSummaryCard spec={spec} revision={r.revision} headingLevel={3} />

      <section aria-label="Who may call" className="space-y-2">
        <h3 className="text-lg font-medium">Who may call</h3>
        <p className="text-sm text-gray-600 dark:text-gray-400">Caller kinds: <strong>{spec.caller_kinds.join(', ')}</strong>. Principals, groups and agents form one identity constraint; client registrations are a separate constraint that is AND-ed with it.</p>
        <SubjectTable rows={rows} caption="Subject constraints" empty="No subject constraints: every caller of an allowed kind is admitted." />
      </section>

      <section aria-label="Resources" className="space-y-3">
        <h3 className="text-lg font-medium">Resources</h3>
        {!!spec.models?.length && (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">Models</caption>
            <thead><tr className="text-xs text-gray-500"><th scope="col" className="py-1 pr-4">Model name</th><th scope="col" className="pr-4">Surface</th><th scope="col">Native model</th></tr></thead>
            <tbody>{spec.models.map(m => <tr key={m.name} className="border-t border-gray-200 dark:border-gray-800"><th scope="row" className="py-1 pr-4 font-mono text-xs font-normal">{m.name}</th><td className="pr-4 font-mono text-xs">{m.surface_ref}</td><td className="font-mono text-xs">{m.native_model}</td></tr>)}</tbody>
          </table>
        )}
        {!!spec.tools?.length && (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">Tool connections</caption>
            <thead><tr className="text-xs text-gray-500"><th scope="col" className="py-1 pr-4">Tool connection</th><th scope="col">Exposed tools</th></tr></thead>
            <tbody>{spec.tools.map(t => <tr key={t.connection_ref} className="border-t border-gray-200 dark:border-gray-800"><th scope="row" className="py-1 pr-4 font-mono text-xs font-normal">{t.connection_ref}</th><td className="font-mono text-xs">{t.expose?.length ? t.expose.join(', ') : 'everything the connection permits'}</td></tr>)}</tbody>
          </table>
        )}
        {!!spec.skills?.length && <p className="text-sm">Skills: {spec.skills.map(s => s.instance_ref).join(', ')}</p>}
        {spec.qos && (
          <p className="text-sm">QoS class <strong>{spec.qos.class}</strong>{spec.qos.weight ? `, weight ${spec.qos.weight}` : ''}
            {spec.qos.limits && `, limits: ${[
              spec.qos.limits.requests_per_minute && `${spec.qos.limits.requests_per_minute} requests/min`,
              spec.qos.limits.tokens_per_minute && `${spec.qos.limits.tokens_per_minute} tokens/min`,
              spec.qos.limits.concurrency && `${spec.qos.limits.concurrency} concurrent`,
            ].filter(Boolean).join(', ')}`}
            {spec.qos.budget_refs?.length ? `, budgets: ${spec.qos.budget_refs.join(', ')}` : ''}</p>
        )}
      </section>

      <EndpointList spec={spec} surfaces={surfaces.data} />
      <QuotaPlanPanel spec={spec} />
      <AccessPointUsagePanel accessPointId={spec.id} />
    </div>
  )
}

export default function AccessPoints() {
  return (
    <Routes>
      <Route index element={<AccessPointList />} />
      <Route path="new" element={<AccessPointEditor />} />
      <Route path=":id" element={<AccessPointDetail />} />
      <Route path=":id/edit" element={<AccessPointEditor />} />
    </Routes>
  )
}
