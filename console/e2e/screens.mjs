// Walks every Console route as a given caller, capturing screenshots (desktop
// and phone, light and dark), console errors, failed requests and the
// accessibility scan for each screen.
//
//   node e2e/screens.mjs --harness harness.json --console http://127.0.0.1:19090 \
//        [--as admin|deployer|norole] [--out e2e/shots] [--variants 1440-light,1440-dark,390-light,390-dark]
import { chromium, parseArgs, loadHarness, newSession, settle, shotPath, a11yScan, Report } from './lib.mjs'

const args = parseArgs()
const h = loadHarness(args.harness ?? 'harness.json')
const base = args.console ?? 'http://127.0.0.1:19090'
const out = args.out ?? 'e2e/shots'
const who = args.as ?? 'admin'
const variants = (args.variants ?? '1440-light,1440-dark,390-light,390-dark').split(',')
const token = { admin: h.admin_token, deployer: h.deployer_token, norole: h.norole_token, anonymous: null }[who]

export const ROUTES = [
  ['overview', '/overview'], ['dashboard', '/dashboard'], ['mcplex', '/mcplex'], ['a2aplex', '/a2aplex'], ['llmplex', '/llmplex'],
  ['skillsplex', '/skillsplex'], ['clientplex', '/clientplex'], ['quotaplex', '/quotaplex'], ['usage', '/usage'], ['agents', '/agents'],
  ['access-points', '/access-points'], ['access-point-new', '/access-points/new'], ['access-point-local', '/access-points/local'],
  ['user-identity', '/identity/users'], ['runs', '/runs'], ['deploy', '/deploy'], ['permissions', '/permissions'],
]

const report = new Report()
const browser = await chromium.launch()
const summary = []
for (const v of variants) {
  const [w, theme] = v.split('-')
  const s = await newSession(browser, base, token, { width: Number(w), height: w === '390' ? 844 : 900, dark: theme === 'dark' })
  const routes = who === 'anonymous' ? [['login', '/login']] : ROUTES
  for (const [name, route] of routes) {
    s.problems.length = 0
    await s.page.goto(base + route)
    await settle(s.page, 600)
    await s.page.screenshot({ path: shotPath(`${out}/${who}`, `${name}-${v}.png`), fullPage: true })
    const a11y = await a11yScan(s.page)
    const url = new URL(s.page.url()).pathname
    summary.push({ who, variant: v, name, route, landed: url, problems: [...s.problems], a11y })
    const bad = s.problems.filter((p) => !(p.kind === 'console' && /Failed to load resource/.test(p.text)))
    console.log(`${who} ${v} ${name} -> ${url} problems=${s.problems.length} a11y=${a11y.length}`)
    for (const p of s.problems) console.log(`     ${p.kind}: ${p.text}`)
    if (v === variants[0]) for (const i of a11y) console.log(`     a11y ${i.rule}: ${i.el}`)
    report.ok(`${who} ${name} ${v} loads without page errors`, !bad.some((p) => p.kind === 'pageerror'))
  }
  await s.context.close()
}
await browser.close()
import('node:fs').then((fs) => fs.writeFileSync(shotPath(`${out}/${who}`, 'summary.json'), JSON.stringify(summary, null, 1)))
report.finish()
