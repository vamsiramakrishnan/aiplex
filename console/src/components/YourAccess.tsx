import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useAuth } from '../hooks/useAuth'
import { accessPointView, listAccessPointViews, type AccessPointView } from '../api/accessPoints'
import { TIME_PRESETS, presetSince, usageSummary, type TimePreset } from '../api/usage'
import CopyButton from './CopyButton'
import { ErrorNotice } from './Forbidden'
import { StepCode } from './SetupSteps'
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

/** Server-built endpoint URLs of one access point, with how to authenticate. Nothing here is constructed in the browser. */
export function ReducedConnect({ view, onClose }: { view: AccessPointView; onClose?: () => void }) {
  const models = view.endpoints.find((e) => e.kind === 'models')
  const mcp = view.endpoints.find((e) => e.kind === 'mcp')
  return (
    <section aria-label={`Connect to ${view.id}`} className="space-y-3 rounded border border-gray-200 dark:border-gray-800 p-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-base font-medium">Connect to {view.display_name || view.id}</h3>
        {onClose && <button type="button" onClick={onClose} className="text-sm underline">Close</button>}
      </div>
      <p className="text-sm text-gray-700 dark:text-gray-300">Send your AIPlex token on every request as <code>Authorization: Bearer $AIPLEX_TOKEN</code>. The access point decides what your token may reach.</p>
      {view.endpoints.length === 0 && <p className="text-sm text-gray-600 dark:text-gray-400">This access point has no endpoints for you yet.</p>}
      <ul className="space-y-1">
        {view.endpoints.map((e) => (
          <li key={`${e.kind}-${e.url}`} className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-xs text-gray-600 dark:text-gray-400 min-w-40">{endpointLabel(e)}</span>
            <code className="font-mono text-xs break-all">{e.url}</code>
            <CopyButton text={e.url} label={`${endpointLabel(e)} URL of ${view.id}`} />
          </li>
        ))}
      </ul>
      {models && <StepCode step={{ title: 'List the models you can call', language: 'bash', content: `curl -sS ${models.url} \\\n  -H "Authorization: Bearer $AIPLEX_TOKEN"` }} />}
      {mcp && <StepCode step={{ title: 'MCP client configuration', language: 'json', content: JSON.stringify({ mcpServers: { [`aiplex-${view.id}`]: { type: 'http', url: mcp.url, headers: { Authorization: 'Bearer ${AIPLEX_TOKEN}' } } } }, null, 2) }} />}
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
            {connecting === v.id && <ReducedConnect view={v} onClose={() => setConnecting(null)} />}
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
