import { useEffect, useState } from 'react'

const KEY = 'aiplex_dark_mode'

function read(): boolean {
  try {
    const stored = localStorage.getItem(KEY)
    if (stored !== null) return stored === 'true'
  } catch { /* storage may be disabled */ }
  try { return window.matchMedia('(prefers-color-scheme: dark)').matches } catch { return false }
}

/** Applies the theme to the document: Tailwind's `dark` class and the brand tokens' data-theme. */
export function applyTheme(dark: boolean) {
  const root = document.documentElement
  root.classList.toggle('dark', dark)
  root.setAttribute('data-theme', dark ? 'dark' : 'light')
}

/** Call once before the first render so every screen, including sign-in, starts in the right theme. */
export function initTheme() { applyTheme(read()) }

export function useDarkMode() {
  const [dark, setDark] = useState(read)
  useEffect(() => {
    applyTheme(dark)
    try { localStorage.setItem(KEY, String(dark)) } catch { /* ignore */ }
  }, [dark])
  return [dark, () => setDark((d) => !d)] as const
}
