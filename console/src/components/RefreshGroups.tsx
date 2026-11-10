import { useId, useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { invalidateMembership, type UserProvider } from '../api/userIdentity'

/** Admin action: drop one user's cached directory groups so the next request re-resolves them. */
export default function RefreshGroups({ provider, onClose }: { provider: UserProvider; onClose: () => void }) {
  const uid = useId()
  const [subject, setSubject] = useState('')
  const refresh = useMutation({ mutationFn: (s: string) => invalidateMembership(provider.id, s) })
  const submit = (e: FormEvent) => { e.preventDefault(); if (subject.trim()) refresh.mutate(subject.trim()) }
  const status = (refresh.error as { status?: number } | null)?.status
  return (
    <section aria-label={`Refresh groups for a user of ${provider.id}`} className="rounded border border-gray-200 dark:border-gray-800 p-3 space-y-2">
      <div className="flex justify-between"><h3 className="text-sm font-semibold">Refresh groups for a user ({provider.id})</h3><button type="button" className="text-xs text-gray-600 dark:text-gray-400" onClick={onClose}>Close</button></div>
      <p className="text-xs text-gray-600 dark:text-gray-400">Forces the next request from this user to look their groups up in the directory again. The cache is per server process: this applies only to the server replica that receives this request. Other replicas refresh when their cache expires.</p>
      <form onSubmit={submit} className="flex flex-wrap items-end gap-2">
        <div>
          <label htmlFor={`${uid}-subject`} className="block text-xs mb-1">User&apos;s subject ({provider.subject_claim})</label>
          <input id={`${uid}-subject`} value={subject} onChange={e => setSubject(e.target.value)} className="border rounded px-2 py-1 text-sm font-mono bg-transparent" />
        </div>
        <button type="submit" disabled={!subject.trim() || refresh.isPending} className="px-3 py-1 text-sm bg-brand-600 text-white rounded disabled:opacity-50">{refresh.isPending ? 'Refreshing…' : 'Refresh groups'}</button>
      </form>
      {refresh.isError && <p role="alert" className="text-xs text-status-denied-fg">{status === 403 ? 'Refreshing groups requires the admin role.' : `Could not refresh groups: ${(refresh.error as Error).message}`}</p>}
      {refresh.data && <p role="status" className="text-xs text-status-ok-fg">Refreshed groups for <code>{refresh.data.principal_id}</code> (not before {refresh.data.not_before}). Scope: {refresh.data.scope}.</p>}
    </section>
  )
}
