import { useEffect, useRef, useState } from 'react'

/** Copies text to the clipboard; the label names what is copied for assistive tech. */
export default function CopyButton({ text, label, className }: { text: string; label: string; className?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setState('copied')
    } catch {
      setState('failed')
    }
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), 2000)
  }
  return (
    <button type="button" onClick={copy} aria-label={`Copy ${label}`} className={className ?? 'text-xs rounded border px-2 py-0.5 hover:bg-gray-100 dark:hover:bg-gray-800'}>
      {state === 'copied' ? 'Copied' : state === 'failed' ? 'Copy failed' : 'Copy'}
    </button>
  )
}
