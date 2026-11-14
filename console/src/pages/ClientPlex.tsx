import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { authenticatedRequest } from '../api/client'

interface Profile { id: string; name: string; surface: string; modes: string[]; protocols: string[] }
interface Registration { id: string; profile_id: string; mode: string; protocol: string; principal_binding: string; [key: string]: unknown }
interface Record { id: string; revision: number; spec: Registration }
interface Plan { support_status: string; commands?: string[][]; version_requirements?: string[]; artifacts: { target: string; format: string; merge_strategy: string; content: string }[]; services: { host: string; purpose: string; required: boolean }[]; credential_bindings?: { reference: string; environment: string }[]; warnings: string[] }
const starter: Registration = { id: 'developer', profile_id: 'codex', mode: 'direct', protocol: 'openai-responses', principal_binding: 'replace-with-canonical-principal', project_id: 'your-project', location: 'global', model_id: 'replace-with-enabled-model-id', auth_mode: 'adc' }
const base = '/api/v1/clients'
const idPattern = /^[a-z][a-z0-9-]{0,62}$/

// registrationFromProfile builds a starting registration from the profile the API returned. It takes the
// first gateway mode (direct mode needs Google Cloud project data the page does not have), pairs it with
// the protocol that matches it (the claude-apps protocol belongs only to claude-apps-gateway), and fills
// every key the server requires for that mode (pkg/clientplex, Registration.Validate). Values the operator
// must decide are placeholders starting with "replace-" or "REPLACE_", so saving fails until they are replaced.
function registrationFromProfile(profile: Profile): Registration {
  const mode = profile.modes.find((m) => m !== 'direct') ?? profile.modes[0] ?? ''
  const protocol = profile.protocols.find((p) => (p === 'claude-apps') === (mode === 'claude-apps-gateway')) ?? profile.protocols[0] ?? ''
  const id = idPattern.test(`developer-${profile.id}`) ? `developer-${profile.id}` : 'developer'
  const registration: Registration = { id, profile_id: profile.id, mode, protocol, principal_binding: 'replace-with-canonical-principal', auth_mode: '' }
  if (mode === 'direct') {
    Object.assign(registration, { project_id: 'replace-with-project-id', location: 'replace-with-location', auth_mode: 'adc' })
  } else {
    Object.assign(registration, { destination_ref: 'replace-with-destination-id' })
    if (mode === 'claude-apps-gateway') {
      registration.auth_mode = 'oidc'
    } else {
      Object.assign(registration, { auth_mode: 'gateway-key', credential_env: 'REPLACE_WITH_ENV_VAR', credential_ref: 'replace-with-credential-ref' })
    }
  }
  return registration
}

export default function ClientPlex() {
  const cache = useQueryClient()
  const profiles = useQuery({ queryKey: ['clientplex-profiles'], queryFn: () => authenticatedRequest<Profile[]>(`${base}/profiles`) })
  const registrations = useQuery({ queryKey: ['clientplex-registrations'], queryFn: () => authenticatedRequest<Record[]>(`${base}/registrations`) })
  const [editor, setEditor] = useState(JSON.stringify(starter, null, 2))
  const [revision, setRevision] = useState(0)
  const [selected, setSelected] = useState('')
  const [plan, setPlan] = useState<Plan | null>(null)
  // Save acknowledgements belong to an editor session; deletes also depend on its text.
  const editorSession = useRef(0)
  const draftGeneration = useRef(0)
  const planGeneration = useRef(0)
  const clearPlan = () => { planGeneration.current++; setPlan(null) }
  const updateEditor = (text: string) => {
    draftGeneration.current++
    setEditor(text); clearPlan()
  }
  const changeEditor = (text: string, id: string, expected: number) => {
    editorSession.current++
    setSelected(id); setRevision(expected); updateEditor(text)
  }
  const save = useMutation({
    mutationFn: async ({ editor, revision, selected, session }: { editor: string; revision: number; selected: string; session: number }) => {
      const spec = JSON.parse(editor) as Registration
      if (!idPattern.test(spec.id)) throw new Error('A valid registration ID is required')
      // The server accepts placeholder values, so the page refuses to save them.
      const placeholders = Object.entries(spec).filter(([, value]) => typeof value === 'string' && /^(replace-|REPLACE_)/.test(value)).map(([key]) => key)
      if (placeholders.length) throw new Error(`Replace the placeholder values before saving: ${placeholders.join(', ')}`)
      if (revision > 0 && spec.id !== selected) throw new Error('Start a new registration to change the ID')
      const record = await authenticatedRequest<Record>(`${base}/registrations/${encodeURIComponent(spec.id)}`, { method: 'PUT', headers: revision ? { 'If-Match': `"${revision}"` } : {}, body: JSON.stringify(spec) })
      return { record, session }
    },
    onSuccess: ({ record, session }) => {
      cache.invalidateQueries({ queryKey: ['clientplex-registrations'] })
      if (session !== editorSession.current) return
      setSelected(record.id); setRevision(record.revision); clearPlan()
    },
  })
  const render = useMutation({
    mutationFn: async ({ id, generation }: { id: string; generation: number }) => {
      const plan = await authenticatedRequest<Plan>(`${base}/registrations/${encodeURIComponent(id)}/plan`, { method: 'POST' })
      return { generation, plan }
    },
    onSuccess: ({ generation, plan }) => { if (generation === planGeneration.current) setPlan(plan) },
  })
  const requestPlan = (id: string) => {
    const generation = ++planGeneration.current
    setPlan(null)
    render.mutate({ id, generation })
  }
  const remove = useMutation({
    mutationFn: ({ record }: { record: Record; generation: number }) => authenticatedRequest<void>(`${base}/registrations/${encodeURIComponent(record.id)}`, { method: 'DELETE', headers: { 'If-Match': `"${record.revision}"` } }),
    onSuccess: (_result, { record, generation }) => {
      cache.invalidateQueries({ queryKey: ['clientplex-registrations'] })
      clearPlan()
      if (generation === draftGeneration.current && selected === record.id) changeEditor(JSON.stringify(starter, null, 2), '', 0)
    },
  })
  // A profile prefills a new registration from the fields the API returned for it: its ID, its first mode and protocol.
  const prefill = (profile: Profile) => changeEditor(JSON.stringify(registrationFromProfile(profile), null, 2), '', 0)
  const error = profiles.error ?? registrations.error ?? save.error ?? render.error ?? remove.error
  return <div className="space-y-6">
    <div><h1 className="text-2xl font-bold">ClientPlex</h1><p className="text-gray-600 dark:text-gray-400">Register developer clients, check native protocol compatibility, and review configuration and network requirements.</p></div>
    {error && <p role="alert" className="text-status-denied-fg">{error.message}</p>}
    <section><h3 className="font-semibold mb-2">Supported profiles</h3><div className="grid md:grid-cols-3 gap-3">{profiles.data?.map((profile) => <div className="border rounded p-3" key={profile.id}><h4 className="font-medium">{profile.name}</h4><p className="text-sm text-gray-600 dark:text-gray-400">{profile.surface} · {profile.modes.join(', ')}</p><p className="text-xs mt-1">{profile.protocols.join(', ')}</p><button type="button" className="mt-2 text-sm text-brand-600 underline disabled:opacity-50" disabled={!profile.modes.length || !profile.protocols.length} onClick={() => prefill(profile)}>Use this profile</button></div>)}</div></section>
    <section><h3 className="font-semibold mb-2">Registrations</h3><table className="w-full text-sm"><thead><tr className="text-left"><th>ID</th><th>Profile</th><th>Mode</th><th>Revision</th><th>Actions</th></tr></thead><tbody>{registrations.isPending && <tr><td colSpan={5} className="py-2 text-gray-600 dark:text-gray-400">Loading registrations…</td></tr>}{registrations.isError && <tr><td colSpan={5} className="py-2 text-status-denied-fg">Could not load registrations: {registrations.error.message}</td></tr>}{registrations.data?.length === 0 && <tr><td colSpan={5} className="py-2 text-gray-600 dark:text-gray-400">No registrations yet. Use a profile above or start a new registration.</td></tr>}{registrations.data?.map((record) => <tr key={record.id} className="border-t"><td className="py-2">{record.id}</td><td>{record.spec.profile_id}</td><td>{record.spec.mode}</td><td>{record.revision}</td><td className="space-x-3"><button onClick={() => changeEditor(JSON.stringify(record.spec, null, 2), record.id, record.revision)}>Edit</button><button onClick={() => requestPlan(record.id)}>Plan</button><button onClick={() => remove.mutate({ record, generation: draftGeneration.current })} disabled={remove.isPending}>Delete</button></td></tr>)}</tbody></table></section>
    <section className="max-w-3xl"><div className="flex items-center gap-4 mb-2"><h3 className="font-semibold">{revision ? `Edit ${selected}, revision ${revision}` : 'New registration'}</h3><button onClick={() => changeEditor(JSON.stringify(starter, null, 2), '', 0)} className="text-sm">New</button></div><label htmlFor="client-registration" className="text-sm text-gray-600 dark:text-gray-400">Use credential references and environment-variable names. Do not paste tokens or keys.</label><textarea id="client-registration" className="block w-full border rounded p-3 mt-2 font-mono text-sm" rows={16} value={editor} onChange={(event) => updateEditor(event.target.value)} /><button className="mt-3 bg-brand-600 text-brand-contrast rounded px-4 py-2" disabled={save.isPending} onClick={() => save.mutate({ editor, revision, selected, session: editorSession.current })}>{save.isPending ? 'Saving…' : 'Save registration'}</button></section>
    {plan && <section className="space-y-4"><h3 className="font-semibold">Connection plan · {plan.support_status}</h3>{plan.version_requirements?.map((requirement) => <p key={requirement} className="text-sm">{requirement}</p>)}{plan.commands?.map((command) => <pre key={command.join(" ")} className="text-sm bg-gray-100 dark:bg-gray-900 rounded p-3">{command.join(" ")}</pre>)}{plan.warnings.map((warning) => <p key={warning} className="text-sm text-gray-600 dark:text-gray-400">{warning}</p>)}{plan.credential_bindings?.map((binding) => <p key={binding.environment} className="text-sm">Credential reference <code>{binding.reference}</code> → environment <code>{binding.environment}</code></p>)}{plan.artifacts.map((artifact) => <div key={artifact.target}><h4 className="font-medium">{artifact.target}</h4><p className="text-xs text-gray-600 dark:text-gray-400">{artifact.format} · {artifact.merge_strategy}</p><pre className="text-sm overflow-auto bg-gray-100 dark:bg-gray-900 rounded p-3 mt-1">{artifact.content}</pre></div>)}<table className="w-full text-sm"><thead><tr className="text-left"><th>Host</th><th>Purpose</th><th>Required</th></tr></thead><tbody>{plan.services.map((service) => <tr key={`${service.host}:${service.purpose}`} className="border-t"><td className="py-2">{service.host}</td><td>{service.purpose}</td><td>{service.required ? 'Yes' : 'When configured'}</td></tr>)}</tbody></table></section>}
  </div>
}
