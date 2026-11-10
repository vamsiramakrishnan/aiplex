import { useEffect, useRef, useState } from 'react'

/** navigator.clipboard needs a secure context; over plain http on a LAN name it is absent, so fall back to a selection copy. */
async function writeClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(text); return } catch { /* fall through to the selection copy */ }
  }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.cssText = 'position:fixed;top:0;left:0;opacity:0'
  document.body.appendChild(area)
  const active = document.activeElement as HTMLElement | null
  area.select()
  try {
    if (!document.execCommand('copy')) throw new Error('copy command refused')
  } finally {
    area.remove()
    active?.focus?.()
  }
}

/** Copies text to the clipboard; the label names what is copied for assistive tech. */
export default function CopyButton({ text, label, className }: { text: string; label: string; className?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    try {
      await writeClipboard(text)
      setState('copied')
    } catch {
      setState('failed')
    }
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), 2000)
  }
  return (
    <>
      <button type="button" onClick={copy} aria-label={`Copy ${label}`} className={className ?? 'text-xs rounded border border-gray-300 dark:border-gray-600 px-2 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800'}>
        {state === 'copied' ? 'Copied' : state === 'failed' ? 'Copy failed' : 'Copy'}
      </button>
      <span role="status" className="sr-only">{state === 'copied' ? 'Copied to clipboard' : state === 'failed' ? 'Copy failed' : ''}</span>
    </>
  )
}
