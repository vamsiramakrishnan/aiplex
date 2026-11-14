import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import ScopeSelector from '../components/ScopeSelector'
import { ADMIN_REASON, useAccess } from '../hooks/useAccess'
import {
  ROLES, createRoleBinding, deleteRoleBinding, errorStatus, isRevisionConflict, listRoleBindings, listRoles, updateRoleBinding,
  type IAMRole, type RoleBinding, type RoleInfo,
} from '../api/roleBindings'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50'
const primary = 'px-3 py-1.5 text-sm bg-brand-600 text-brand-contrast rounded disabled:opacity-50'
const field = 'border rounded px-3 py-2 text-sm w-full'
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e))

const CHANGED = 'This role binding changed since this page read it. Nothing was saved or deleted. Reload to see the current version, then try again.'

/** Role bindings decide who is an admin, so every state here says what is and is not known. */
export default function RoleBindings() {
  const access = useAccess()
  const cache = useQueryClient()
  const [form, setForm] = useState<{ mode: 'create' } | { mode: 'edit'; binding: RoleBinding } | null>(null)
  const bindings = useQuery({ queryKey: ['role-bindings'], queryFn: listRoleBindings, enabled: access.isAdmin })
  const roles = useQuery({ queryKey: ['iam-roles'], queryFn: listRoles, enabled: access.isAdmin })
  if (!access.isAdmin) {
    return <section className="space-y-2"><p role="status" className="text-sm text-gray-600 dark:text-gray-400">{ADMIN_REASON} Role bindings are visible to admins only.</p></section>
  }
  const refresh = () => cache.invalidateQueries({ queryKey: ['role-bindings'] })
  const rows = bindings.data
  const loadFailed = bindings.isError
  return <section className="space-y-4" aria-label="Role bindings">
    <p className="text-sm text-gray-600 dark:text-gray-400">A role binding gives every member of an identity-provider group an AIPlex role and scopes. Bindings decide who is an admin: review changes carefully.</p>
    <div className="flex gap-2">
      <button type="button" className={primary} disabled={!!form} onClick={() => setForm({ mode: 'create' })}>Add role binding</button>
      <button type="button" className={btn} disabled={bindings.isFetching} onClick={() => void bindings.refetch()}>{bindings.isFetching ? 'Loading…' : 'Reload'}</button>
    </div>
    {form && <BindingForm key={form.mode === 'edit' ? `${form.binding.id}@${form.binding.revision ?? '?'}` : 'create'} form={form} roles={roles.data} rolesError={roles.isError ? msg(roles.error) : undefined}
      onClose={() => setForm(null)} onSaved={() => { setForm(null); void refresh() }} onReload={() => { setForm(null); void bindings.refetch() }} />}
    {bindings.isPending && <p role="status">Loading role bindings…</p>}
    {loadFailed && !rows && <div role="alert" className="text-status-denied-fg">
      <p>Could not load role bindings: {msg(bindings.error)}. This is an error, not an empty list.</p>
      <button type="button" className={`${btn} mt-2`} onClick={() => void bindings.refetch()}>Retry</button>
    </div>}
    {loadFailed && rows && <div role="alert" className="text-status-denied-fg">
      <p>Could not refresh role bindings: {msg(bindings.error)}. The rows below were read earlier and may be out of date.</p>
      <button type="button" className={`${btn} mt-2`} onClick={() => void bindings.refetch()}>Retry</button>
    </div>}
    {rows && rows.length === 0 && !loadFailed && <p role="status">No role bindings. No group is granted a role through bindings.</p>}
    {rows && rows.length > 0 && <div className="overflow-x-auto"><table className="w-full text-sm text-left">
      <caption className="sr-only">{loadFailed ? 'Role bindings (possibly out of date)' : 'Role bindings'}</caption>
      <thead><tr><th className="py-2 pr-3">Group</th><th className="pr-3">Role</th><th className="pr-3">Provider</th><th className="pr-3">Pool</th><th className="pr-3">Scopes</th><th className="pr-3">Description</th><th className="pr-3">Revision</th><th><span className="sr-only">Actions</span></th></tr></thead>
      <tbody>{rows.map((b) => <Row key={b.id} binding={b} stale={loadFailed} editing={form?.mode === 'edit' && form.binding.id === b.id} onEdit={() => setForm({ mode: 'edit', binding: b })} onChanged={() => void refresh()} onReload={() => void bindings.refetch()} />)}</tbody>
    </table></div>}
  </section>
}

function scopesCell(scopes: string[] | null | undefined) {
  if (!Array.isArray(scopes)) return <span>unknown</span>
  if (scopes.length === 0) return <span>none</span>
  return <ul className="font-mono text-xs space-y-0.5">{scopes.map((s) => <li key={s}>{s}</li>)}</ul>
}

function Row({ binding: b, stale, editing, onEdit, onChanged, onReload }: { binding: RoleBinding; stale: boolean; editing: boolean; onEdit: () => void; onChanged: () => void; onReload: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const known = typeof b.revision === 'number'
  const remove = useMutation({
    mutationFn: () => deleteRoleBinding(b.id, b.revision as number),
    onSuccess: () => { setConfirming(false); onChanged() },
  })
  const conflict = remove.isError && (isRevisionConflict(remove.error) || errorStatus(remove.error) === 404)
  const why = known ? undefined : 'The revision is unknown, so a change cannot be checked against it. Reload first.'
  return <>
    <tr className="border-t align-top">
      <td className="py-2 pr-3 break-all">{b.group}</td><td className="pr-3">{b.role}</td>
      <td className="pr-3 break-all">{b.provider || 'any'}</td><td className="pr-3 break-all">{b.pool_id || 'any'}</td>
      <td className="pr-3">{scopesCell(b.scopes)}</td><td className="pr-3">{b.description || 'none'}</td>
      <td className="pr-3">{known ? b.revision : 'unknown'}</td>
      <td className="whitespace-nowrap space-x-2">
        <button type="button" className={btn} disabled={!known || stale || editing} title={why ?? (stale ? 'Reload first: the list may be out of date.' : undefined)} aria-label={`Edit ${b.group} ${b.role}`} onClick={onEdit}>Edit</button>
        <button type="button" className={btn} disabled={!known || stale || confirming} title={why ?? (stale ? 'Reload first: the list may be out of date.' : undefined)} aria-label={`Delete ${b.group} ${b.role}`} onClick={() => { remove.reset(); setConfirming(true) }}>Delete</button>
      </td>
    </tr>
    {(confirming || remove.isError) && <tr><td colSpan={8} className="pb-3"><div className="border rounded p-3 space-y-2">
      {confirming && !conflict && <p>Delete the role binding for group <strong>{b.group}</strong> ({b.role})? Members of the group lose the role this binding gives them.</p>}
      {conflict && <div role="alert" className="text-status-denied-fg"><p>{errorStatus(remove.error) === 404 ? 'This role binding no longer exists. Nothing was deleted.' : CHANGED}</p>
        <button type="button" className={`${btn} mt-2`} onClick={() => { setConfirming(false); remove.reset(); onReload() }}>Reload latest version</button></div>}
      {remove.isError && !conflict && <p role="alert" className="text-status-denied-fg">Could not delete: {msg(remove.error)}</p>}
      {confirming && !conflict && <div className="flex gap-2">
        <button type="button" className={primary} disabled={remove.isPending} onClick={() => remove.mutate()}>{remove.isPending ? 'Deleting…' : 'Confirm delete'}</button>
        <button type="button" className={btn} disabled={remove.isPending} onClick={() => { setConfirming(false); remove.reset() }}>Cancel</button>
      </div>}
    </div></td></tr>}
  </>
}

type FormState = { mode: 'create' } | { mode: 'edit'; binding: RoleBinding }

function BindingForm({ form, roles, rolesError, onClose, onSaved, onReload }: { form: FormState; roles: RoleInfo[] | undefined; rolesError: string | undefined; onClose: () => void; onSaved: () => void; onReload: () => void }) {
  const editing = form.mode === 'edit' ? form.binding : null
  const [group, setGroup] = useState(editing?.group ?? '')
  const [role, setRole] = useState<IAMRole>(editing?.role ?? 'viewer')
  const [provider, setProvider] = useState(editing?.provider ?? '')
  const [poolId, setPoolId] = useState(editing?.pool_id ?? '')
  const [description, setDescription] = useState(editing?.description ?? '')
  // Edit: the scopes the form shows are exactly what is sent. null means the stored scopes are unknown and must be stated before saving.
  // Create: 'defaults' omits scopes (the role's defaults); 'explicit' sends the list, possibly empty (grant none).
  const [scopes, setScopes] = useState<string[] | null>(editing ? (Array.isArray(editing.scopes) ? [...editing.scopes] : null) : null)
  const [explicit, setExplicit] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [reset, setReset] = useState<IAMRole | null>(null)
  const defaultsFor = (r: IAMRole) => roles?.find((x) => x.role === r)?.default_scopes
  const save = useMutation({
    mutationFn: () => {
      const input = { group: group.trim(), role, provider: provider.trim(), pool_id: poolId.trim(), description: description.trim() }
      if (editing) return updateRoleBinding(editing.id, editing.revision as number, { ...input, scopes: scopes as string[] })
      return createRoleBinding({ ...input, ...(explicit ? { scopes: scopes ?? [] } : {}) })
    },
    onSuccess: onSaved,
    onError: (e) => { setConflict(isRevisionConflict(e) || (editing !== null && errorStatus(e) === 404)) },
  })
  const scopesStated = editing ? scopes !== null : true
  const valid = group.trim() !== '' && scopesStated
  const locked = save.isPending || conflict
  const dirtyScopes = editing && Array.isArray(editing.scopes) && scopes !== null && (scopes.length !== editing.scopes.length || scopes.some((s) => !editing.scopes!.includes(s)))
  // A new role starts from that role's defaults, shown before saving. Without a loaded defaults list nothing is guessed: the admin must state scopes.
  const changeRole = (next: IAMRole) => {
    setRole(next); save.reset(); setConflict(false)
    if (!editing || next === role) return
    const defaults = defaultsFor(next)
    setScopes(defaults ? [...defaults] : null)
    setReset(next)
  }
  const submit = () => { setConflict(false); save.mutate() }
  const stateScopes = (values: string[]) => { setScopes(values); save.reset(); setConflict(false) }
  return <div className="border rounded-lg p-4 space-y-3" role="group" aria-label={editing ? 'Edit role binding' : 'Add role binding'}>
    <h2 className="font-semibold">{editing ? 'Edit role binding' : 'Add role binding'}</h2>
    <div><label htmlFor="rb-group" className="block text-sm font-medium mb-1">Group</label><input id="rb-group" className={field} value={group} disabled={locked} onChange={(e) => setGroup(e.target.value)} placeholder="Identity provider group, for example aiplex-admins" /></div>
    <div><label htmlFor="rb-role" className="block text-sm font-medium mb-1">Role</label>
      <select id="rb-role" className={field} value={role} disabled={locked} onChange={(e) => changeRole(e.target.value as IAMRole)}>{ROLES.map((r) => <option key={r} value={r}>{r}</option>)}</select>
      {editing && reset && (defaultsFor(reset)
        ? <p role="status" className="text-xs mt-1">Scopes were reset to the default scopes for {reset}. Adjust them below before saving.</p>
        : <p className="text-xs mt-1">The default scopes for {reset} are unavailable{rolesError ? ` (${rolesError})` : ''}, so none are assumed.</p>)}
    </div>
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <div><label htmlFor="rb-provider" className="block text-sm font-medium mb-1">Provider (issuer), optional</label><input id="rb-provider" className={field} value={provider} disabled={locked} onChange={(e) => setProvider(e.target.value)} placeholder="Empty: any issuer" /></div>
      <div><label htmlFor="rb-pool" className="block text-sm font-medium mb-1">Pool ID, optional</label><input id="rb-pool" className={field} value={poolId} disabled={locked} onChange={(e) => setPoolId(e.target.value)} placeholder="Empty: any pool" /></div>
    </div>
    <div><label htmlFor="rb-description" className="block text-sm font-medium mb-1">Description, optional</label><input id="rb-description" className={field} value={description} disabled={locked} onChange={(e) => setDescription(e.target.value)} /></div>
    <fieldset className="space-y-2" disabled={locked}>
      <legend className="text-sm font-medium">Scopes</legend>
      {!editing && <>
        <label className="flex items-center gap-2 text-sm"><input type="radio" name="rb-scope-mode" checked={!explicit} onChange={() => { setExplicit(false); setScopes(null) }} /> Use the role's default scopes</label>
        <label className="flex items-center gap-2 text-sm"><input type="radio" name="rb-scope-mode" checked={explicit} onChange={() => { setExplicit(true); setScopes([...(defaultsFor(role) ?? [])]) }} /> Choose the scopes (an empty choice grants none)</label>
        {!explicit && <p className="text-xs">{defaultsFor(role) ? `Defaults for ${role}: ${defaultsFor(role)!.length ? defaultsFor(role)!.join(', ') : 'none'}.` : `The server applies the defaults for ${role}${rolesError ? ` (could not list them: ${rolesError})` : ''}.`}</p>}
      </>}
      {editing && scopes === null && <p role="alert" className="text-status-denied-fg text-sm">The stored scopes are unknown. State the scopes below before saving; nothing is kept implicitly.</p>}
      {editing && scopes === null && <button type="button" className={btn} onClick={() => stateScopes([])}>Grant no scopes</button>}
      {editing && scopes !== null && !dirtyScopes && <p className="text-xs">Scopes are unchanged: the {scopes.length} stored scope{scopes.length === 1 ? '' : 's'} are sent as shown.</p>}
      {(editing || explicit) && <>
        {scopes !== null && scopes.length === 0 && <p role="status" className="text-sm">This binding will grant no scopes.</p>}
        <ScopeSelector available={[...new Set([...(roles?.flatMap((r) => r.default_scopes) ?? []), ...(scopes ?? [])])]} selected={scopes ?? []} disabled={locked} onChange={stateScopes} />
      </>}
    </fieldset>
    {conflict && <div role="alert" className="text-status-denied-fg"><p>{editing && save.error && errorStatus(save.error) === 404 ? 'This role binding no longer exists. Nothing was saved.' : CHANGED}</p>
      <button type="button" className={`${btn} mt-2`} onClick={onReload}>Reload latest version</button></div>}
    {save.isError && !conflict && <p role="alert" className="text-status-denied-fg">{!editing && errorStatus(save.error) === 409 ? 'A role binding for this provider, pool, group and role already exists. Edit that one instead.' : `Could not save: ${msg(save.error)}`}</p>}
    <div className="flex gap-2">
      <button type="button" className={primary} disabled={!valid || locked} onClick={submit}>{save.isPending ? 'Saving…' : editing ? 'Save changes' : 'Create role binding'}</button>
      <button type="button" className={btn} disabled={save.isPending} onClick={onClose}>Cancel</button>
    </div>
  </div>
}
