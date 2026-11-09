import type { ReactNode } from 'react'
import type { AccessPointSpec } from '../api/accessPoints'

/** Compact summary of one access point: name, caller kinds, resource counts, QoS class. */
export default function AccessPointSummaryCard({ spec, revision, footer, headingLevel = 3 }: {
  spec: AccessPointSpec
  revision?: number
  footer?: ReactNode
  headingLevel?: 2 | 3 | 4
}) {
  const Heading = `h${headingLevel}` as 'h2' | 'h3' | 'h4'
  const counts = [
    ['Models', spec.models?.length ?? 0],
    ['Tools', spec.tools?.length ?? 0],
    ['Skills', spec.skills?.length ?? 0],
  ] as const
  return (
    <article aria-label={`Access point ${spec.display_name || spec.id}`} className="rounded border border-gray-200 dark:border-gray-800 p-4 bg-white dark:bg-gray-900">
      <Heading className="font-semibold">{spec.display_name || spec.id}</Heading>
      {spec.display_name && <p className="font-mono text-xs text-gray-500">{spec.id}</p>}
      <dl className="mt-2 grid grid-cols-[auto,1fr] gap-x-3 gap-y-1 text-sm">
        <dt className="text-gray-500">Callers</dt>
        <dd>{spec.caller_kinds.join(', ') || '—'}</dd>
        {counts.map(([label, n]) => <div key={label} className="contents"><dt className="text-gray-500">{label}</dt><dd>{n}</dd></div>)}
        <dt className="text-gray-500">QoS class</dt>
        <dd>{spec.qos ? spec.qos.class : 'none'}</dd>
        {revision !== undefined && <><dt className="text-gray-500">Revision</dt><dd>{revision}</dd></>}
      </dl>
      {footer && <div className="mt-3">{footer}</div>}
    </article>
  )
}
