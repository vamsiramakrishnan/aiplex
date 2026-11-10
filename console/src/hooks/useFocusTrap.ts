import { useEffect, useRef, type RefObject } from 'react'

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type=hidden]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * Makes a dialog or drawer modal for the keyboard: focus moves into it when it
 * opens, Tab and Shift+Tab stay inside, Escape closes it, and focus returns to
 * the control that opened it. Attach the returned ref to the dialog element.
 */
export function useFocusTrap<T extends HTMLElement>(active: boolean, onClose: () => void): RefObject<T | null> {
  const ref = useRef<T | null>(null)
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    if (!active) return
    const node = ref.current
    const opener = document.activeElement as HTMLElement | null
    const items = () => (node ? [...node.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null || el === document.activeElement) : [])
    if (node && !node.contains(document.activeElement)) (items()[0] ?? node).focus()
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.stopPropagation(); close.current(); return }
      if (event.key !== 'Tab' || !node) return
      const list = items()
      if (list.length === 0) { event.preventDefault(); node.focus(); return }
      const first = list[0], last = list[list.length - 1]
      if (event.shiftKey && (document.activeElement === first || !node.contains(document.activeElement))) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && (document.activeElement === last || !node.contains(document.activeElement))) { event.preventDefault(); first.focus() }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      if (opener && document.contains(opener)) opener.focus()
    }
  }, [active])
  return ref
}
