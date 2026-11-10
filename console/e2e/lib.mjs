// Shared helpers for the Console browser checks (plain Node + the pre-installed
// Playwright; no test runner). See console/README.md, "Browser checks".
import fs from 'node:fs'
import path from 'node:path'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
export const { chromium } = require('playwright')

export function parseArgs(argv = process.argv.slice(2)) {
  const out = {}
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith('--')) continue
    const key = argv[i].slice(2)
    const next = argv[i + 1]
    if (next === undefined || next.startsWith('--')) out[key] = true
    else { out[key] = next; i++ }
  }
  return out
}

export function loadHarness(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'))
}

/** Tracks failures; the process exits non-zero if any check failed. */
export class Report {
  constructor() { this.failures = []; this.passes = 0; this.notes = [] }
  ok(name, cond, detail = '') {
    if (cond) { this.passes++; return true }
    this.failures.push(`${name}${detail ? ': ' + detail : ''}`)
    console.log(`  FAIL ${name}${detail ? ': ' + detail : ''}`)
    return false
  }
  note(text) { this.notes.push(text); console.log(`  note ${text}`) }
  finish() {
    console.log(`\n${this.passes} checks passed, ${this.failures.length} failed`)
    for (const f of this.failures) console.log(` - ${f}`)
    process.exitCode = this.failures.length ? 1 : 0
  }
}

/** A browser context signed in as the given token (null: signed out). */
export async function newSession(browser, base, token, { width = 1440, height = 900, dark = false } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, colorScheme: dark ? 'dark' : 'light', permissions: ['clipboard-read', 'clipboard-write'] })
  await context.addInitScript(([t, d]) => {
    try {
      if (t) localStorage.setItem('aiplex_token', t)
      localStorage.setItem('aiplex_dark_mode', String(d))
    } catch { /* ignore */ }
  }, [token, dark])
  const page = await context.newPage()
  const problems = []
  page.on('console', (m) => { if (m.type() === 'error') problems.push({ kind: 'console', text: m.text() }) })
  page.on('pageerror', (e) => problems.push({ kind: 'pageerror', text: String(e) }))
  page.on('requestfailed', (r) => { if (!r.failure()?.errorText?.includes('ABORT')) problems.push({ kind: 'requestfailed', text: `${r.method()} ${r.url()} ${r.failure()?.errorText}` }) })
  page.on('response', (r) => { if (r.status() >= 400 && r.url().startsWith(base)) problems.push({ kind: 'http', status: r.status(), text: `${r.request().method()} ${new URL(r.url()).pathname}${new URL(r.url()).search} -> ${r.status()}` }) })
  return { context, page, problems, base }
}

export async function settle(page, ms = 400) {
  await page.waitForLoadState('networkidle').catch(() => {})
  await page.waitForTimeout(ms)
}

export function shotPath(dir, name) {
  fs.mkdirSync(dir, { recursive: true })
  return path.join(dir, name)
}

/**
 * Targeted accessibility checks that need no library: every form control has a
 * name, every button/link has an accessible name, one main landmark, one h1,
 * no horizontal page overflow, images have alt, and contrast of visible text
 * nodes against the effective background (WCAG AA).
 */
export async function a11yScan(page) {
  return page.evaluate(() => {
    const issues = []
    const visible = (el) => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el); return r.width > 0 && r.height > 0 && s.visibility !== 'hidden' && s.display !== 'none' }
    const label = (el) => {
      const aria = el.getAttribute('aria-label'); if (aria?.trim()) return aria
      const by = el.getAttribute('aria-labelledby')
      if (by) { const t = by.split(/\s+/).map((id) => document.getElementById(id)?.textContent ?? '').join(' ').trim(); if (t) return t }
      if (el.labels && el.labels.length) { const t = [...el.labels].map((l) => l.textContent).join(' ').trim(); if (t) return t }
      const title = el.getAttribute('title'); if (title?.trim()) return title
      const t = el.textContent?.trim(); if (t) return t
      const img = el.querySelector('img[alt]'); if (img?.getAttribute('alt')?.trim()) return img.getAttribute('alt')
      return ''
    }
    const desc = (el) => `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}${el.getAttribute('name') ? '[name=' + el.getAttribute('name') + ']' : ''} "${(el.textContent || el.getAttribute('placeholder') || '').trim().slice(0, 30)}"`
    for (const el of document.querySelectorAll('input:not([type=hidden]), select, textarea')) {
      if (!visible(el)) continue
      if (!label(el) && !el.getAttribute('placeholder')) issues.push({ rule: 'input-unlabelled', el: desc(el) })
      else if (!el.labels?.length && !el.getAttribute('aria-label') && !el.getAttribute('aria-labelledby') && !el.getAttribute('title')) issues.push({ rule: 'input-label-placeholder-only', el: desc(el) })
    }
    for (const el of document.querySelectorAll('button, [role=button], a[href]')) {
      if (!visible(el)) continue
      if (!label(el)) issues.push({ rule: 'name-missing', el: desc(el) + ' ' + el.outerHTML.slice(0, 80) })
    }
    for (const el of document.querySelectorAll('img')) if (visible(el) && !el.hasAttribute('alt')) issues.push({ rule: 'img-alt', el: desc(el) })
    if (document.querySelectorAll('main').length !== 1) issues.push({ rule: 'landmark-main', el: `${document.querySelectorAll('main').length} main landmarks` })
    if (document.querySelectorAll('h1').length < 1) issues.push({ rule: 'h1-missing', el: '' })
    if (document.documentElement.scrollWidth > window.innerWidth + 1) issues.push({ rule: 'page-overflow-x', el: `scrollWidth ${document.documentElement.scrollWidth} > ${window.innerWidth}` })
    // Contrast
    const parse = (c) => { const m = c.match(/rgba?\(([^)]+)\)/); if (!m) { const c2 = c.match(/color\(srgb ([^)]+)\)/); if (c2) { const p = c2[1].split(/[\s/]+/).map(Number); return [p[0] * 255, p[1] * 255, p[2] * 255, p[3] ?? 1] } return null } const p = m[1].split(/[,/\s]+/).filter(Boolean).map(Number); return [p[0], p[1], p[2], p[3] ?? 1] }
    const lum = ([r, g, b]) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b) }
    const over = (fg, bg) => [fg[0] * fg[3] + bg[0] * (1 - fg[3]), fg[1] * fg[3] + bg[1] * (1 - fg[3]), fg[2] * fg[3] + bg[2] * (1 - fg[3]), 1]
    const effectiveBg = (el) => {
      const stack = []
      for (let n = el; n; n = n.parentElement) { const c = parse(getComputedStyle(n).backgroundColor); if (c && c[3] > 0) { stack.push(c); if (c[3] >= 1) break } }
      let bg = [255, 255, 255, 1]
      const body = parse(getComputedStyle(document.body).backgroundColor); if (body && body[3] > 0) bg = body
      for (const c of stack.reverse()) bg = over(c, bg)
      return bg
    }
    const seen = new Set()
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = n.textContent.trim(); const el = n.parentElement
      if (!text || !el || !visible(el) || ['SCRIPT', 'STYLE'].includes(el.tagName)) continue
      if (el.closest('[disabled], [aria-disabled=true]')) continue
      const s = getComputedStyle(el)
      const fg0 = parse(s.color); if (!fg0) continue
      const bg = effectiveBg(el); const fg = over([fg0[0], fg0[1], fg0[2], fg0[3] * (parseFloat(s.opacity) || 1)], bg)
      const l1 = lum(fg), l2 = lum(bg); const ratio = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)
      const size = parseFloat(s.fontSize); const bold = parseInt(s.fontWeight) >= 700
      const need = size >= 24 || (size >= 18.66 && bold) ? 3 : 4.5
      if (ratio < need) { const key = `${el.tagName}.${el.className}|${s.color}|${Math.round(ratio * 10)}`; if (!seen.has(key)) { seen.add(key); issues.push({ rule: 'contrast', el: `${desc(el)} ratio ${ratio.toFixed(2)} < ${need} (${s.color} on rgb(${bg.slice(0, 3).map(Math.round)}))` }) } }
    }
    return issues
  })
}
