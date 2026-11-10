// Debug aid: node e2e/dump.mjs harness.json /route [admin|deployer|norole] [click-text ...]
// Prints headings, buttons, links and form controls of a screen (after optional clicks).
import { chromium, loadHarness, newSession, settle } from './lib.mjs'

const [file, route, who = 'admin', ...clicks] = process.argv.slice(2)
const h = loadHarness(file)
const token = { admin: h.admin_token, deployer: h.deployer_token, norole: h.norole_token }[who]
const base = process.env.CONSOLE ?? 'http://127.0.0.1:19090'
const browser = await chromium.launch()
const s = await newSession(browser, base, token)
await s.page.goto(base + route)
await settle(s.page)
for (const c of clicks) {
  await s.page.getByRole('button', { name: c }).or(s.page.getByRole('tab', { name: c })).or(s.page.getByRole('link', { name: c })).first().click()
  await settle(s.page)
}
const out = await s.page.evaluate(() => {
  const t = (e) => (e.getAttribute('aria-label') || e.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 60)
  return {
    url: location.pathname + location.search,
    headings: [...document.querySelectorAll('h1,h2,h3,h4')].map((e) => `${e.tagName} ${t(e)}`),
    buttons: [...document.querySelectorAll('main button, main [role=tab]')].map(t),
    links: [...document.querySelectorAll('main a')].map((e) => `${t(e)} -> ${e.getAttribute('href')}`),
    controls: [...document.querySelectorAll('main input, main select, main textarea')].map((e) => `${e.tagName.toLowerCase()}${e.type ? ':' + e.type : ''} ${(e.labels?.[0]?.textContent || e.getAttribute('aria-label') || e.placeholder || e.name || e.id).trim().slice(0, 50)}`),
    alerts: [...document.querySelectorAll('[role=alert],[role=status]')].map(t),
  }
})
console.log(JSON.stringify(out, null, 1))
console.log('problems', JSON.stringify(s.problems))
await browser.close()
