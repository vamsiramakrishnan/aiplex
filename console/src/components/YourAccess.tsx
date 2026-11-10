import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../hooks/useAuth'
import { accessPointView, listAccessPointViews } from '../api/accessPoints'
import { TIME_PRESETS, presetSince, usageSummary, type TimePreset } from '../api/usage'
import CopyButton from './CopyButton'
import { ErrorNotice } from './Forbidden'
import ConnectWizard from './ConnectWizard'
import UsageSummaryTable, { UsageHeadline } from './UsageSummaryTable'

const ENDPOINT_LABELS: Record<string, string> = {
  models: 'Model listing (OpenAI-compatible)',
  mcp: 'MCP tools (Streamable HTTP)',
  mcp_catalog: 'MCP registry catalog',
  skills: 'Skills manifest',
}
export const endpointLabel = (e: { kind: string; label?: string }) => e.label ?? ENDPOINT_LABELS[e.kind] ?? e.kind.replace(/[_-]/g, ' ')

/** Who the server says you are, and what you hold. */
export function IdentityCard() {
  const { user, ambient } = useAuth()
  if (!user) return null
  return (
    <section aria-label="Your identity" className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 space-y-2">
      <h2 className="text-lg font-medium">You</h2>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
        <dt className="text-gray-600 dark:text-gray-400">Name</dt><dd>{user.display_name || user.email}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Email</dt><dd className="break-all">{user.email}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Principal</dt>
        <dd className="flex flex-wrap items-center gap-2"><code className="font-mono text-xs break-all">{user.sub}</code><CopyButton text={user.sub} label="your principal ID" /></dd>
        <dt className="text-gray-600 dark:text-gray-400">Role</dt><dd>{user.roles.length ? user.roles.join(', ') : 'none (you can use the access points that admit you)'}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Grants</dt>
        <dd>{user.scopes.length ? <span className="font-mono text-xs break-all">{user.scopes.join(' ')}</span> : 'none yet. Ask an administrator for model and tool grants.'}</dd>
        {ambient && <><dt className="text-gray-600 dark:text-gray-400">Session</dt><dd>Signed in by this server.</dd></>}
      </dl>
    </section>
  )
}

/** The access points that admit the caller, each with endpoint URLs and a Connect panel. */
export function YourAccessPoints({ hideHeading }: { hideHeading?: boolean } = {}) {
  const list = useQuery({ queryKey: ['access-point-views'], queryFn: listAccessPointViews })
  const [connecting, setConnecting] = useState<string | null>(null)
  if (list.isLoading) return <p role="status">Loading your access points…</p>
  if (list.isError) return <ErrorNotice error={list.error} what="access points" onRetry={() => void list.refetch()} />
  const views = (list.data ?? []).map(accessPointView)
  return (
    <section aria-label="Your access points" className="space-y-3">
      {!hideHeading && (
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-lg font-medium">Access points that admit you</h2>
          <Link to="/access-points" className="text-sm text-brand-600 hover:underline">All access points</Link>
        </div>
      )}
      {views.length === 0 && <p className="text-sm text-gray-600 dark:text-gray-400">No access point admits you yet. Ask an administrator to add you to one.</p>}
      <ul className="grid gap-4 md:grid-cols-2">
        {views.map((v) => (
          <li key={v.id} className="rounded-lg border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-4 space-y-2">
            <h3 className="font-medium">{v.display_name || v.id}</h3>
            <p className="font-mono text-xs text-gray-600 dark:text-gray-400">{v.id}</p>
            <ul className="space-y-1">
              {v.endpoints.slice(0, 3).map((e) => (
                <li key={e.url} className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="text-xs text-gray-600 dark:text-gray-400">{endpointLabel(e)}</span>
                  <CopyButton text={e.url} label={`${endpointLabel(e)} URL of ${v.id}`} />
                </li>
              ))}
            </ul>
            <div className="flex gap-3 text-sm">
              <Link to={`/access-points/${encodeURIComponent(v.id)}`} className="text-brand-600 hover:underline" aria-label={`Open ${v.display_name || v.id}`}>Open</Link>
              <button type="button" className="text-brand-600 hover:underline" aria-label={`Connect a client to ${v.id}`} onClick={() => setConnecting(connecting === v.id ? null : v.id)} aria-expanded={connecting === v.id}>Connect</button>
            </div>
            {connecting === v.id && <ConnectWizard accessPointId={v.id} onClose={() => setConnecting(null)} />}
          </li>
        ))}
      </ul>
    </section>
  )
}

/** The caller's own usage (the server limits non-admins to their own records). */
export function YourUsage() {
  const [preset, setPreset] = useState<TimePreset>('24h')
  const [since, setSince] = useState(() => presetSince('24h'))
  const q = useQuery({ queryKey: ['own-usage', since ?? ''], queryFn: () => usageSummary(since ? { since } : {}, ['access_point_id']) })
  return (
    <section aria-label="Your usage" className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <h2 className="text-lg font-medium">Your usage</h2>
        <div>
          <label htmlFor="own-usage-range" className="block text-xs text-gray-600 dark:text-gray-400 mb-1">Time range</label>
          <select id="own-usage-range" value={preset} onChange={(e) => { const p = e.target.value as TimePreset; setPreset(p); setSince(presetSince(p)) }} className="rounded border border-gray-300 bg-transparent px-2 py-1 text-sm dark:border-gray-700">
            {TIME_PRESETS.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}
          </select>
        </div>
      </div>
      {q.isLoading && <p role="status">Loading your usage…</p>}
      {q.isError && <ErrorNotice error={q.error} what="usage" onRetry={() => void q.refetch()} />}
      {q.data && (q.data.groups.length === 0
        ? <p className="text-sm text-gray-600 dark:text-gray-400">No requests in this window.</p>
        : <><UsageHeadline groups={q.data.groups} label="Your usage totals" /><UsageSummaryTable groups={q.data.groups} keyField="access_point_id" heading="Access point" /></>)}
      <p className="text-sm"><Link to="/usage" className="text-brand-600 hover:underline">See every request</Link></p>
    </section>
  )
}
