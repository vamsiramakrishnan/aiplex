import { describe, expect, it } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { statusTone } from '../components/StatusBadge'

const root = path.resolve(__dirname, '..')
const files = (dir: string): string[] => readdirSync(dir).flatMap((f) => {
  const p = path.join(dir, f)
  if (statSync(p).isDirectory()) return f === '__tests__' ? [] : files(p)
  return /\.(tsx|ts)$/.test(f) ? [p] : []
})

describe('status colours come from the brand tokens', () => {
  it('uses no raw Tailwind status palette in components', () => {
    const raw = /\b(?:text|bg|border|ring|outline|divide)-(?:red|green|amber|yellow|orange|emerald|rose)-\d{2,3}\b/
    const offenders = files(root).filter((f) => raw.test(readFileSync(f, 'utf8'))).map((f) => path.relative(root, f))
    expect(offenders).toEqual([])
  })

  it('maps status words to tones and treats unknown words as unknown, not ok', () => {
    expect(statusTone('running')).toBe('ok')
    expect(statusTone('degraded')).toBe('warning')
    expect(statusTone('failed')).toBe('denied')
    expect(statusTone('something-new')).toBe('unknown')
  })

  it('derives the Tailwind brand scale from the token scale, not color-mix', () => {
    const config = readFileSync(path.resolve(root, '..', 'tailwind.config.js'), 'utf8')
    expect(config).toContain('--aiplex-primary-')
    expect(config).not.toContain('color-mix')
    expect(readFileSync(path.join(root, 'index.css'), 'utf8')).not.toContain('color-mix')
  })
})
