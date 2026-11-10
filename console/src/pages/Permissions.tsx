import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ScopeSelector from '../components/ScopeSelector'
import { authenticatedRequest, listInstances } from '../api/client'
import { useAuth } from '../hooks/useAuth'
import NativeGovernance from './NativeGovernance'

const canonicalPrincipal = /^principal-[0-9a-f]{64}$/
interface UserScopes { user_id: string; scopes: string[]; by_plane: Record<string, string[]>; revision?: number }
export default function Permissions() {
  const cache = useQueryClient()
  const { user } = useAuth()
  const [tab, setTab] = useState<'users' | 'native'>('users')
  const [principal, setPrincipal] = useState('')
  const [loadedPrincipal, setLoadedPrincipal] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  // The server requires the revision the grants were read at (If-Match); a stale one is a 409, never a silent overwrite.
  const [revision, setRevision] = useState(0)
  const scopes = useQuery({ queryKey: ['permission-scope-options'], queryFn: () => listInstances(), enabled: tab === 'users' })
  const available = [...new Set(scopes.data?.flatMap((instance) => instance.scopes ?? []) ?? [])]
  const load = useMutation({
    mutationFn: (id: string) => authenticatedRequest<UserScopes>(`/auth/users/${encodeURIComponent(id)}/scopes`),
    onSuccess: (result, id) => { if (result.user_id !== id) throw new Error('Permission response identity mismatch.'); setLoadedPrincipal(id); setSelected(result.scopes ?? []); setRevision(result.revision ?? 0) },
  })
  const save = useMutation({
    mutationFn: ({ id, values }: { id: string; values: string[] }) => authenticatedRequest<void>(`/auth/users/${encodeURIComponent(id)}/scopes`, { method: 'PUT', body: JSON.stringify({ scopes: values }), headers: { 'If-Match': String(revision) } }),
    onSuccess: (_r, { id }) => { void cache.invalidateQueries({ queryKey: ['userScopes'] }); load.mutate(id) },
  })
  function changePrincipal(value: string) { setPrincipal(value); setLoadedPrincipal(''); setSelected([]); load.reset(); save.reset() }
  const busy = load.isPending || save.isPending
  return <div className="max-w-4xl space-y-5">
    <div><h1 className="text-2xl font-bold">Permissions</h1><p className="text-gray-600 dark:text-gray-400">Manage manual user ceilings and review native cloud governance.</p></div>
    <div className="flex gap-2" role="tablist" aria-label="Permission settings"><button role="tab" aria-selected={tab === 'users'} onClick={() => setTab('users')} className={`border rounded px-4 py-2 text-sm ${tab === 'users' ? 'bg-brand-600 border-brand-600 text-brand-contrast' : ''}`}>User scopes</button><button role="tab" aria-selected={tab === 'native'} onClick={() => setTab('native')} className={`border rounded px-4 py-2 text-sm ${tab === 'native' ? 'bg-brand-600 border-brand-600 text-brand-contrast' : ''}`}>Native governance</button></div>
    {tab === 'native' ? <NativeGovernance /> : <section className="space-y-4">
      <p className="text-sm text-gray-600 dark:text-gray-400">Admin access is required. Manual scopes are keyed by the server's canonical principal ID; email addresses and native cloud principal URIs are separate identifiers. Group-derived grants remain controlled by IAM role bindings.</p>
      <div><label htmlFor="permission-principal" className="block text-sm font-medium mb-1">Canonical principal ID</label><input id="permission-principal" value={principal} disabled={busy} onChange={(e) => changePrincipal(e.target.value.trim())} placeholder="principal- followed by 64 lowercase hex characters" className="border rounded px-3 py-2 text-sm w-full font-mono" /></div>
      <div className="flex gap-2"><button disabled={busy || !canonicalPrincipal.test(principal)} onClick={() => { setLoadedPrincipal(''); setSelected([]); save.reset(); load.mutate(principal) }} className="border rounded px-3 py-2 text-sm disabled:opacity-50">{load.isPending ? 'Loading…' : 'Load scopes'}</button>{canonicalPrincipal.test(user?.sub ?? '') && <button className="border rounded px-3 py-2 text-sm" disabled={busy} onClick={() => changePrincipal(user!.sub)}>Use my principal</button>}</div>
      {load.error && <p role="alert" className="text-status-denied-fg">{load.error.message}</p>}
      {loadedPrincipal !== '' && loadedPrincipal === principal && <>
        <p className="text-sm break-all">Editing manual grants for <code>{loadedPrincipal}</code>.</p>
        <p className="text-xs text-gray-600 dark:text-gray-400">Options come from deployed instance scopes. Add exact scopes for other configured capabilities.</p>
        {scopes.error && <p role="alert">Scope discovery unavailable: {scopes.error.message}. Existing scopes and explicit custom scopes remain available.</p>}
        <ScopeSelector available={available} selected={selected} disabled={busy} onChange={(values) => { setSelected(values); save.reset() }} />
        <button onClick={() => save.mutate({ id: loadedPrincipal, values: [...selected] })} disabled={busy} className="bg-brand-600 text-brand-contrast rounded px-4 py-2 text-sm disabled:opacity-50">{save.isPending ? 'Saving…' : 'Save permissions'}</button>
        {save.isSuccess && <p role="status" className="text-status-ok-fg">Manual grants saved.</p>}{save.error && <p role="alert" className="text-status-denied-fg">{save.error.message}</p>}
      </>}
    </section>}
  </div>
}
