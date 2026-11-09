import { endpointGroups, endpointGroupsFromConnect, type AccessPointSpec, type ModelSurfaceRecord } from '../api/accessPoints'
import type { ConnectInfo } from '../api/overview'
import CopyButton from './CopyButton'

/** Endpoint URLs of an access point, grouped by protocol, each with a copy button. */
export default function EndpointList({ spec, surfaces, origin, connect, connectError, connectLoading }: {
  spec: AccessPointSpec
  surfaces: ModelSurfaceRecord[] | undefined
  origin?: string
  /** Server-built URLs (preferred). When absent the URLs are derived from this browser's address. */
  connect?: ConnectInfo | null
  connectError?: string
  connectLoading?: boolean
}) {
  const server = !!connect
  const groups = connect ? endpointGroupsFromConnect(spec, connect) : endpointGroups(spec, origin ?? window.location.origin, surfaces)
  return (
    <section aria-label="Endpoints" className="space-y-3">
      <h3 className="text-lg font-medium">Endpoints</h3>
      {server ? (
        <p className="text-xs text-gray-500">URLs are built by the server from its configured public URL. Callers authenticate with their AIPlex token; the access point decides what that token may reach.</p>
      ) : connectLoading ? (
        <p role="status" className="text-xs text-gray-500">Asking the server for its public URLs…</p>
      ) : (
        <p role="note" className="text-xs text-amber-700 dark:text-amber-400">
          These URLs are derived from this console&apos;s address because the server could not provide its own{connectError ? `: ${connectError}` : ''}.
          If clients reach AIPlex through a different public hostname, substitute it.
        </p>
      )}
      {groups.length === 0 && <p className="text-sm text-gray-500">This access point exposes no models, tools or skills yet.</p>}
      {groups.map(g => (
        <div key={g.id} role="group" aria-label={g.title} className="rounded border border-gray-200 dark:border-gray-800 p-3">
          <h4 className="text-sm font-semibold">{g.title}</h4>
          {g.note && <p className="text-xs text-amber-700 dark:text-amber-400 mt-1">{g.note}</p>}
          <ul className="mt-2 space-y-1">
            {g.urls.map(u => (
              <li key={u.url} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="text-xs text-gray-500 min-w-40">{u.label}</span>
                <code className="font-mono text-xs break-all">{u.url}</code>
                <CopyButton text={u.url} label={`${u.label} URL for ${g.title}`} />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  )
}
