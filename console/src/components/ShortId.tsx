/** A long identifier (such as a canonical principal ID) shortened in the middle for tables; the full value is in the tooltip and the accessible text. */
export default function ShortId({ value, empty = '—', head = 14, tail = 6 }: { value?: string | null; empty?: string; head?: number; tail?: number }) {
  if (!value) return <>{empty}</>
  if (value.length <= head + tail + 3) return <>{value}</>
  return <span title={value} className="font-mono text-xs"><span aria-hidden="true">{value.slice(0, head)}…{value.slice(-tail)}</span><span className="sr-only">{value}</span></span>
}
