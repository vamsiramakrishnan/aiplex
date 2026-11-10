import { describe, expect, it } from 'vitest'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'

// The Console keeps committed copies of the brand assets it uses (its Docker build
// context cannot see ../brand). Regenerate with `npm run sync:brand`.
const root = path.resolve(__dirname, '..', '..')
const copies: [string, string][] = [
  ['tokens/tokens.css', 'src/brand/tokens.css'],
  ['logo/aiplex-lockup-horizontal.svg', 'src/brand/aiplex-lockup-horizontal.svg'],
  ['logo/aiplex-lockup-horizontal-reversed.svg', 'src/brand/aiplex-lockup-horizontal-reversed.svg'],
  ['logo/aiplex-favicon.svg', 'public/favicon.svg'],
]
const brandDir = path.join(root, '..', 'brand')

describe('brand asset copies', () => {
  for (const [from, to] of copies) {
    it(`${to} matches brand/${from}`, () => {
      expect(existsSync(path.join(root, to))).toBe(true)
      // brand/ is absent when the Console is tested outside the full repository.
      if (!existsSync(brandDir)) return
      expect(readFileSync(path.join(root, to), 'utf8')).toBe(readFileSync(path.join(brandDir, from), 'utf8'))
    })
  }
})
