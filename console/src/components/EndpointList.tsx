import { endpointGroups, type AccessPointSpec, type ModelSurfaceRecord } from '../api/accessPoints'
import CopyButton from './CopyButton'

/** Endpoint URLs of an access point, grouped by protocol, each with a copy button. */
export default function EndpointList({ spec, surfaces, origin }: {
  spec: AccessPointSpec
  surfaces: ModelSurfaceRecord[] | undefined
  origin?: string
}) {
  const groups = endpointGroups(spec, origin ?? window.location.origin, surfaces)
  return (
    <section aria-label="Endpoints" className="space-y-3">
      <h3 className="text-lg font-medium">Endpoints</h3>
      <p className="text-xs text-gray-500">
        URLs use this console&apos;s origin. If clients reach AIPlex through a different public hostname, substitute it.
        Callers authenticate with their AIPlex token; the access point decides what that token may reach.
      </p>
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
