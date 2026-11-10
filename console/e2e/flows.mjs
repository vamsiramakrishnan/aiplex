// Drives the Console like a person against the real local API and checks what
// each screen does. Every check is a regression test for a defect found in the
// first browser pass. Run (see console/README.md):
//   node e2e/flows.mjs --harness harness.json [--console http://127.0.0.1:19090] [--only access-points,usage,...]
import { chromium, parseArgs, loadHarness, newSession, settle, Report, a11yScan } from './lib.mjs'

const args = parseArgs()
const h = loadHarness(args.harness ?? 'harness.json')
const base = args.console ?? 'http://127.0.0.1:19090'
const only = args.only ? String(args.only).split(',') : null
const report = new Report()
const browser = await chromium.launch()

const api = async (who, method, path, body, headers = {}) => {
  const token = { admin: h.admin_token, deployer: h.deployer_token, norole: h.norole_token }[who]
  const r = await fetch(h.api_url + path, { method, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', ...headers }, body: body === undefined ? undefined : JSON.stringify(body) })
  const text = await r.text()
  let json; try { json = JSON.parse(text) } catch { json = text }
  return { status: r.status, json }
}
const unexpected = (s, allow = []) => s.problems.filter((p) => !allow.some((re) => re.test(p.text)))

async function session(who, opts) {
  const token = { admin: h.admin_token, deployer: h.deployer_token, norole: h.norole_token, none: null }[who]
  return newSession(browser, base, token, opts)
}
const section = async (name, fn) => {
  if (only && !only.includes(name)) return
  console.log(`\n== ${name}`)
  try { await fn() } catch (e) { report.ok(`${name} ran to completion`, false, e.message.split('\n')[0]) }
}

await section('access-points', async () => {
  const s = await session('admin')
  const { page } = s
  // create through the form
  await page.goto(base + '/access-points/new')
  await settle(page)
  await page.getByLabel(/Access point ID/).fill('e2e-created')
  await page.getByLabel('Display name').fill('Created in the browser')
  await page.getByRole('button', { name: 'Add model' }).click()
  await page.getByLabel('Model surface').selectOption('local-models')
  await page.getByLabel('Native model').fill('mock-gpt')
  await page.getByLabel('Name clients use').fill('friendly')
  await page.getByLabel('Set a QoS class for this access point').check()
  await page.getByLabel(/^Class/).fill('batch')
  await page.getByLabel('Limit scope').selectOption('access_point')
  await page.getByLabel('Requests per minute').fill('50')
  await page.getByRole('button', { name: 'Create access point' }).click()
  await page.waitForURL('**/access-points/e2e-created')
  await settle(page)
  const stored = await api('admin', 'GET', '/api/v1/access-points/e2e-created')
  report.ok('created access point exists with alias and qos scope', stored.json?.spec?.models?.[0]?.name === 'friendly' && stored.json?.spec?.qos?.scope === 'access_point', JSON.stringify(stored.json?.spec))
  report.ok('detail shows the qos scope', (await page.textContent('main')).includes('shared pool'))
  report.ok('detail shows server-built endpoint URLs for the OpenAI wire', (await page.textContent('main')).includes(`${h.api_url}/ap/e2e-created/v1/chat/completions`))

  // validation error carries details.field and lands on the row
  await page.goto(base + '/access-points/e2e-created/edit')
  await settle(page)
  await page.getByLabel('Native model').fill('no-such-native-model')
  await page.getByRole('button', { name: 'Save changes' }).click()
  const alert = page.getByRole('group', { name: 'Model 1' }).getByRole('alert')
  await alert.waitFor({ timeout: 5000 }).catch(() => {})
  report.ok('field error appears on the model row', await alert.isVisible().catch(() => false))
  report.ok('native_model input is marked invalid', (await page.getByLabel('Native model').getAttribute('aria-invalid')) === 'true')

  // conflict: someone else changes it first
  await page.goto(base + '/access-points/e2e-created/edit')
  await settle(page)
  const cur = await api('admin', 'GET', '/api/v1/access-points/e2e-created')
  await api('admin', 'PUT', '/api/v1/access-points/e2e-created', { ...cur.json.spec, display_name: 'Changed elsewhere' }, { 'If-Match': `"${cur.json.revision}"` })
  await page.getByLabel('Display name').fill('Mine')
  await page.getByRole('button', { name: 'Save changes' }).click()
  await page.getByText(/changed by someone else/).waitFor({ timeout: 5000 }).catch(() => {})
  report.ok('conflict is reported', await page.getByText(/changed by someone else/).isVisible().catch(() => false))
  await page.getByRole('button', { name: 'Reload latest version' }).click()
  await settle(page)
  report.ok('reload shows the other change', (await page.getByLabel('Display name').inputValue()) === 'Changed elsewhere')

  // delete
  await page.goto(base + '/access-points/e2e-created')
  await settle(page)
  await page.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('button', { name: 'Confirm delete' }).click()
  await page.waitForURL('**/access-points')
  report.ok('deleted access point is gone', (await api('admin', 'GET', '/api/v1/access-points/e2e-created')).status === 404)
  report.ok('access point flows had no unexpected requests', unexpected(s, [/api\/v1\/access-points\/e2e-created.* -> (400|404|409)/, /Failed to load resource/]).length === 0, JSON.stringify(unexpected(s, [/api\/v1\/access-points\/e2e-created.* -> (400|404|409)/, /Failed to load resource/])))
  await s.context.close()
})

import { execFileSync } from 'node:child_process'
const cliJSON = (who, ...cmd) => JSON.parse(execFileSync('node', [new URL('./cli.mjs', import.meta.url).pathname, args.harness ?? 'harness.json', who, ...cmd], { env: { ...process.env, AIPLEX_BIN: process.env.AIPLEX_BIN ?? new URL('../../bin/aiplex', import.meta.url).pathname }, encoding: 'utf8', maxBuffer: 64e6 }))

await section('usage', async () => {
  // Enough records for a second page.
  for (let i = 0; i < 60; i++) await fetch(`${h.api_url}/ap/local/v1/chat/completions`, { method: 'POST', headers: { Authorization: `Bearer ${h.admin_token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ model: 'mock-gpt', messages: [{ role: 'user', content: `bulk ${i}` }] }) })
  const s = await session('admin')
  const { page } = s
  await page.goto(base + '/usage')
  await settle(page)
  const rows = async () => page.$$eval('section[aria-label="Requests"] tbody tr', (trs) => trs.map((tr) => [tr.querySelector('button')?.getAttribute('aria-label') ?? '', ...[...tr.querySelectorAll('th,td')].slice(1).map((c) => c.textContent.trim())]))
  const filters = page.getByRole('form', { name: 'Usage filters' })
  const fld = (name) => filters.locator('label').filter({ has: page.locator('span', { hasText: new RegExp(`^${name}$`) }) }).locator('select,input')
  const first = await rows()
  const cli = cliJSON('admin', 'usage', 'records', '-o', 'json', '--limit', '50')
  report.ok('records page has 50 rows when more exist', first.length === 50, `rows=${first.length}`)
  const cliFirst = cli.records[0]
  const target = (r) => r.model_id || r.tool_name || r.resource_id || '—'
  report.ok('first row model/tool matches the CLI newest record', first[0][2].startsWith(target(cliFirst)), `${first[0][2]} vs ${target(cliFirst)}`)
  report.ok('first row status matches the CLI', first[0][6].startsWith(cliFirst.status), `${first[0][6]} vs ${cliFirst.status}`)
  const tokensOf = (r) => (r.usage?.input_tokens ?? 'unknown')
  const okRows = first.map((r, i) => String(r[7]).replace(/ \(.*/, '') === String(tokensOf(cli.records[i]))).filter(Boolean).length
  report.ok('input tokens column matches the CLI for every row on the page', okRows === 50, `${okRows}/50`)
  // pagination
  await page.getByRole('button', { name: 'Older' }).click()
  await settle(page)
  const second = await rows()
  report.ok('Older loads the next page with different records', second.length > 0 && second[0][0] !== first[0][0], `second=${second.length}`)
  await page.getByRole('button', { name: 'Newer' }).click()
  await settle(page)
  // summary totals equal the API/CLI summary
  const sum = cliJSON('admin', 'usage', 'summary', '-o', 'json')
  const total = sum.groups.reduce((n, g) => n + Number(g.requests), 0)
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await settle(page)
  const reqText = await page.locator('section[aria-label="Usage totals"] [aria-label="Requests"] p').nth(1).textContent()
  report.ok('headline requests equal the CLI summary', Number(reqText.replace(/,/g, '')) === total, `${reqText} vs ${total}`)
  // filters
  await fld('Status').selectOption('error')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await settle(page)
  const errRows = await rows()
  const cliErr = cliJSON('admin', 'usage', 'records', '-o', 'json', '--status', 'error')
  report.ok('status filter matches the CLI', errRows.length === cliErr.records.length && errRows.every((r) => r[6].startsWith('error')), `${errRows.length} vs ${cliErr.records.length}`)
  await fld('Status').selectOption('')
  await fld('Error class').fill('invalid_request')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await settle(page)
  const ecRows = await rows()
  report.ok('error class filter narrows to the unknown-model request', ecRows.length >= 1 && ecRows.length <= errRows.length, `rows=${ecRows.length}`)
  await fld('Error class').fill('')
  await fld('Batch').selectOption('true')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await settle(page)
  report.ok('batch filter with no batches shows the empty state', (await page.textContent('main')).includes('No requests match these filters'))
  await fld('Batch').selectOption('')
  await page.getByRole('button', { name: 'Apply filters' }).click()
  await settle(page)
  // group by batch + source + error class
  await page.getByRole('group', { name: 'Group by' }).or(page.locator('fieldset', { hasText: 'Group by' })).getByLabel('Error class').check()
  await settle(page)
  const txt = await page.locator('section[aria-label="Usage summary"]').textContent()
  report.ok('group by error class shows the invalid_request group', txt.includes('invalid_request'))
  // drawer: focus and Escape
  const open = page.getByRole('button', { name: /Open details for record/ }).first()
  await open.focus()
  await open.click()
  const drawer = page.getByRole('dialog', { name: 'Usage record detail' })
  report.ok('drawer opens', await drawer.isVisible())
  report.ok('focus moves into the drawer', await page.evaluate(() => !!document.activeElement?.closest('[role=dialog]')))
  for (let i = 0; i < 8; i++) await page.keyboard.press('Tab')
  report.ok('Tab stays inside the drawer', await page.evaluate(() => !!document.activeElement?.closest('[role=dialog]')))
  await page.keyboard.press('Escape')
  report.ok('Escape closes the drawer', !(await drawer.isVisible().catch(() => false)))
  report.ok('focus returns to the control that opened it', await page.evaluate(() => document.activeElement?.getAttribute('aria-label')?.startsWith('Open details for record') ?? false))
  // prices
  await page.getByRole('button', { name: 'Add price' }).click()
  const form = page.getByRole('form', { name: 'Add price' })
  report.ok('price form opens and takes focus', await form.isVisible() && await page.evaluate(() => !!document.activeElement?.closest('form[aria-label="Add price"]')))
  await form.getByLabel('Price ID').fill('e2e-price')
  await form.getByLabel('Model ID').fill('e2e-model')
  await form.getByLabel('input_tokens numerator').fill('1')
  await form.getByLabel('input_tokens denominator').fill('1000')
  await page.keyboard.press('Enter')
  await settle(page)
  const priced = await api('admin', 'GET', '/api/v1/usage/prices/e2e-price')
  report.ok('pressing Enter in the form saves the price', priced.status === 200 && priced.json.spec.model_id === 'e2e-model', JSON.stringify(priced.json).slice(0, 120))
  await page.getByRole('button', { name: 'Edit price e2e-price' }).click()
  await page.getByRole('form', { name: /Edit price/ }).getByLabel('Currency').fill('EUR')
  await page.getByRole('button', { name: 'Save price' }).click()
  await settle(page)
  report.ok('edited price is stored', (await api('admin', 'GET', '/api/v1/usage/prices/e2e-price')).json.spec.currency === 'EUR')
  await page.getByRole('button', { name: 'Delete price e2e-price' }).click()
  await page.getByRole('button', { name: 'Confirm delete' }).click()
  await settle(page)
  report.ok('deleted price is gone', (await api('admin', 'GET', '/api/v1/usage/prices/e2e-price')).status === 404)
  report.ok('usage page had no unexpected requests', unexpected(s, [/Failed to load resource/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/])))
  await s.context.close()
})

await browser.close()
report.finish()
