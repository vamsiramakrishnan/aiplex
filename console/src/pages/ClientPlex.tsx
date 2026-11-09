import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { authenticatedRequest } from '../api/client'

interface Profile { id: string; name: string; surface: string; modes: string[]; protocols: string[] }
interface Registration { id: string; profile_id: string; mode: string; protocol: string; principal_binding: string; [key: string]: unknown }
interface Record { id: string; revision: number; spec: Registration }
interface Plan { support_status: string; commands?: string[][]; version_requirements?: string[]; artifacts: { target: string; format: string; merge_strategy: string; content: string }[]; services: { host: string; purpose: string; required: boolean }[]; credential_bindings?: { reference: string; environment: string }[]; warnings: string[] }
const starter: Registration = { id: 'developer', profile_id: 'codex', mode: 'direct', protocol: 'openai-responses', principal_binding: 'replace-with-canonical-principal', project_id: 'your-project', location: 'global', model_id: 'replace-with-enabled-model-id', auth_mode: 'adc' }
const base = '/api/v1/clients'

export default function ClientPlex() {
  const cache = useQueryClient()
  const profiles = useQuery({ queryKey: ['clientplex-profiles'], queryFn: () => authenticatedRequest<Profile[]>(`${base}/profiles`) })
  const registrations = useQuery({ queryKey: ['clientplex-registrations'], queryFn: () => authenticatedRequest<Record[]>(`${base}/registrations`) })
  const [editor, setEditor] = useState(JSON.stringify(starter, null, 2))
  const [revision, setRevision] = useState(0)
  const [selected, setSelected] = useState('')
  const [plan, setPlan] = useState<Plan | null>(null)
  const save = useMutation({
    mutationFn: async () => {
      const spec = JSON.parse(editor) as Registration
      if (!/^[a-z][a-z0-9-]{0,62}$/.test(spec.id)) throw new Error('A valid registration ID is required')
      if (revision > 0 && spec.id !== selected) throw new Error('Start a new registration to change the ID')
      return authenticatedRequest<Record>(`${base}/registrations/${encodeURIComponent(spec.id)}`, { method: 'PUT', headers: revision ? { 'If-Match': `"${revision}"` } : {}, body: JSON.stringify(spec) })
    },
    onSuccess: (record) => { setSelected(record.id); setRevision(record.revision); setPlan(null); cache.invalidateQueries({ queryKey: ['clientplex-registrations'] }) },
  })
  const render = useMutation({ mutationFn: (id: string) => authenticatedRequest<Plan>(`${base}/registrations/${encodeURIComponent(id)}/plan`, { method: 'POST' }), onSuccess: setPlan })
  const remove = useMutation({ mutationFn: (record: Record) => authenticatedRequest<void>(`${base}/registrations/${encodeURIComponent(record.id)}`, { method: 'DELETE', headers: { 'If-Match': `"${record.revision}"` } }), onSuccess: () => { setSelected(''); setRevision(0); setPlan(null); cache.invalidateQueries({ queryKey: ['clientplex-registrations'] }) } })
  const error = profiles.error ?? registrations.error ?? save.error ?? render.error ?? remove.error
  return <div className="space-y-6">
    <div><h2 className="text-2xl font-bold">ClientPlex</h2><p className="text-gray-500">Register developer clients, check native protocol compatibility, and review configuration and network requirements.</p></div>
    {error && <p role="alert" className="text-red-600">{error.message}</p>}
    <section><h3 className="font-semibold mb-2">Supported profiles</h3><div className="grid md:grid-cols-3 gap-3">{profiles.data?.map((profile) => <div className="border rounded p-3" key={profile.id}><h4 className="font-medium">{profile.name}</h4><p className="text-sm text-gray-500">{profile.surface} · {profile.modes.join(', ')}</p><p className="text-xs mt-1">{profile.protocols.join(', ')}</p></div>)}</div></section>
    <section><h3 className="font-semibold mb-2">Registrations</h3><table className="w-full text-sm"><thead><tr className="text-left"><th>ID</th><th>Profile</th><th>Mode</th><th>Revision</th><th>Actions</th></tr></thead><tbody>{registrations.data?.map((record) => <tr key={record.id} className="border-t"><td className="py-2">{record.id}</td><td>{record.spec.profile_id}</td><td>{record.spec.mode}</td><td>{record.revision}</td><td className="space-x-3"><button onClick={() => { setSelected(record.id); setRevision(record.revision); setEditor(JSON.stringify(record.spec, null, 2)); setPlan(null) }}>Edit</button><button onClick={() => render.mutate(record.id)}>Plan</button><button onClick={() => remove.mutate(record)} disabled={remove.isPending}>Delete</button></td></tr>)}</tbody></table></section>
    <section className="max-w-3xl"><div className="flex items-center gap-4 mb-2"><h3 className="font-semibold">{revision ? `Edit ${selected}, revision ${revision}` : 'New registration'}</h3><button onClick={() => { setRevision(0); setSelected(''); setEditor(JSON.stringify(starter, null, 2)); setPlan(null) }} className="text-sm">New</button></div><label htmlFor="client-registration" className="text-sm text-gray-500">Use credential references and environment-variable names. Do not paste tokens or keys.</label><textarea id="client-registration" className="block w-full border rounded p-3 mt-2 font-mono text-sm" rows={16} value={editor} onChange={(event) => setEditor(event.target.value)} /><button className="mt-3 bg-brand-600 text-white rounded px-4 py-2" disabled={save.isPending} onClick={() => save.mutate()}>{save.isPending ? 'Saving…' : 'Save registration'}</button></section>
    {plan && <section className="space-y-4"><h3 className="font-semibold">Connection plan · {plan.support_status}</h3>{plan.version_requirements?.map((requirement) => <p key={requirement} className="text-sm">{requirement}</p>)}{plan.commands?.map((command) => <pre key={command.join(" ")} className="text-sm bg-gray-100 dark:bg-gray-900 rounded p-3">{command.join(" ")}</pre>)}{plan.warnings.map((warning) => <p key={warning} className="text-sm text-gray-600">{warning}</p>)}{plan.credential_bindings?.map((binding) => <p key={binding.environment} className="text-sm">Credential reference <code>{binding.reference}</code> → environment <code>{binding.environment}</code></p>)}{plan.artifacts.map((artifact) => <div key={artifact.target}><h4 className="font-medium">{artifact.target}</h4><p className="text-xs text-gray-500">{artifact.format} · {artifact.merge_strategy}</p><pre className="text-sm overflow-auto bg-gray-100 dark:bg-gray-900 rounded p-3 mt-1">{artifact.content}</pre></div>)}<table className="w-full text-sm"><thead><tr className="text-left"><th>Host</th><th>Purpose</th><th>Required</th></tr></thead><tbody>{plan.services.map((service) => <tr key={`${service.host}:${service.purpose}`} className="border-t"><td className="py-2">{service.host}</td><td>{service.purpose}</td><td>{service.required ? 'Yes' : 'When configured'}</td></tr>)}</tbody></table></section>}
  </div>
}
