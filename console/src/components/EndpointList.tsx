import { endpointGroupsFromConnect, type AccessPointSpec } from '../api/accessPoints'
import type { ConnectInfo } from '../api/overview'
import CopyButton from './CopyButton'

/** Endpoint URLs of an access point, grouped by protocol, each with a copy button. The URLs come from the server. */
export default function EndpointList({ spec, connect, connectError, connectLoading }: {
  spec: AccessPointSpec
  /** The server's connect response: the only source of URLs. */
  connect?: ConnectInfo | null
  connectError?: string
  connectLoading?: boolean
}) {
  const groups = connect ? endpointGroupsFromConnect(spec, connect) : []
  return (
    <section aria-label="Endpoints" className="space-y-3">
      <h3 className="text-lg font-medium">Endpoints</h3>
      {connect ? (
        <p className="text-xs text-gray-600 dark:text-gray-400">URLs are built by the server from its configured public URL and list only what this access point serves. Callers authenticate with their AIPlex token; the access point decides what that token may reach.</p>
      ) : connectLoading ? (
        <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Asking the server for its public URLs…</p>
      ) : (
        <p role="note" className="text-xs text-status-warning-fg">
          The server could not provide endpoint URLs{connectError ? `: ${connectError}` : ''}. The Console does not guess them from its own address, which may not be the address clients use.
        </p>
      )}
      {connect && groups.length === 0 && <p className="text-sm text-gray-600 dark:text-gray-400">This access point exposes no models, tools or skills yet.</p>}
      {groups.map(g => (
        <div key={g.id} role="group" aria-label={g.title} className="rounded border border-gray-200 dark:border-gray-800 p-3">
          <h4 className="text-sm font-semibold">{g.title}</h4>
          {g.note && <p className="text-xs text-status-warning-fg mt-1">{g.note}</p>}
          <ul className="mt-2 space-y-1">
            {g.urls.map(u => (
              <li key={u.url} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="text-xs text-gray-600 dark:text-gray-400 min-w-40">{u.label}</span>
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
