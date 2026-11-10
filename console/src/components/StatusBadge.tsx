import type { ReactNode } from 'react'

/** The four status tones of the brand tokens (--aiplex-color-status-*). */
export type Tone = 'ok' | 'warning' | 'denied' | 'unknown'

const TONE_CLASS: Record<Tone, string> = {
  ok: 'bg-status-ok-bg text-status-ok-fg',
  warning: 'bg-status-warning-bg text-status-warning-fg',
  denied: 'bg-status-denied-bg text-status-denied-fg',
  unknown: 'bg-status-unknown-bg text-status-unknown-fg',
}
const TEXT_CLASS: Record<Tone, string> = { ok: 'text-status-ok-fg', warning: 'text-status-warning-fg', denied: 'text-status-denied-fg', unknown: 'text-status-unknown-fg' }

/** Tone of a status word; anything unrecognised is "unknown", never silently "ok". */
export function statusTone(status: string): Tone {
  switch (status) {
    case 'running': case 'active': case 'ok': case 'enabled': case 'settled': case 'valid': case 'pass': return 'ok'
    case 'provisioning': case 'degraded': case 'open': case 'estimated': case 'skipped': return 'warning'
    case 'failed': case 'suspended': case 'error': case 'denied': case 'fail': case 'invalid': return 'denied'
    default: return 'unknown'
  }
}
export const toneText = (tone: Tone) => TEXT_CLASS[tone]

/** A status word in its tone: the one place status colours are decided. */
export default function StatusBadge({ status, tone }: { status: string; tone?: Tone }) {
  return <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${TONE_CLASS[tone ?? statusTone(status)]}`}>{status}</span>
}

/** A callout in a status tone, for warnings and notices that need more than a word. */
export function Notice({ tone, role, children, className = '' }: { tone: Tone; role?: 'alert' | 'status' | 'note'; children: ReactNode; className?: string }) {
  return <div role={role} className={`rounded border border-current p-3 text-sm ${TONE_CLASS[tone]} ${className}`}>{children}</div>
}
