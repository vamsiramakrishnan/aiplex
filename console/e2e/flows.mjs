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

await section('llmplex', async () => {
  const s = await session('admin')
  const { page } = s
  await page.goto(base + '/llmplex')
  await settle(page)
  await page.getByRole('button', { name: 'surfaces' }).click()
  await settle(page)
  const byLabel = (text) => page.locator('form label', { hasText: text }).locator('input,textarea,select').first().or(page.locator(`form [aria-label*="${text}"]`)).first()
  for (const x of (await api('admin', 'GET', '/api/v1/llm/surfaces/')).json.filter((x) => x.id.startsWith('e2e-'))) await api('admin', 'DELETE', `/api/v1/llm/surfaces/${x.id}`, undefined, { 'If-Match': `"${x.revision}"` })
  const providers = [['openai', 'openai-chat', 'https://api.openai.com'], ['anthropic', 'anthropic-messages', 'https://api.anthropic.com'], ['azure-openai', 'openai-chat', 'https://e2e-resource.openai.azure.com'], ['bedrock', 'bedrock-converse', 'https://bedrock-runtime.us-east-1.amazonaws.com'], ['openai-compatible', 'openai-chat', h.models_url], ['gemini-developer', 'google-genai', 'https://generativelanguage.googleapis.com']]
  for (const [provider, protocol, origin] of providers) {
    const id = `e2e-${provider}`
    await page.getByRole('button', { name: 'Add surface' }).click()
    await page.locator('form select').nth(0).selectOption(provider)
    await page.getByText(/Loading the .* catalog/).waitFor({ state: 'detached', timeout: 10000 }).catch(() => {})
    await page.locator('form select').nth(1).selectOption(protocol)
    try { await byLabel('Surface ID').fill(id, { timeout: 5000 })
    await byLabel('Origin').fill(origin, { timeout: 5000 })
    await byLabel('Allowed models').fill('model-a', { timeout: 5000 })
    if (provider === 'azure-openai') { await page.locator('form label', { hasText: 'azure.deployments.chat-completions' }).locator('input').check(); await byLabel('API version').fill('2024-10-21', { timeout: 5000 }) }
    if (provider === 'bedrock') await byLabel('Signing region').fill('us-east-1')
    await byLabel('Secret reference name').fill('e2e-secret')
    } catch (e) { report.ok(`fill the ${provider} form`, false, e.message.split('\n')[0] + ' ' + e.message.split('\n').find((l) => l.includes('locator')) ); await page.screenshot({ path: `e2e/shots/llm-${provider}.png`, fullPage: true }); await page.getByRole('button', { name: 'Cancel' }).click().catch(() => {}); continue }
    // choose the first operation offered for the protocol when none is preselected
    const ops = page.locator('form label', { hasText: /(GET|POST|PUT|DELETE) / })
    if (!(await ops.locator('input:checked').count())) await ops.first().locator('input').check()
    await page.getByRole('button', { name: 'Save surface' }).click()
    await settle(page)
    const saved = await api('admin', 'GET', `/api/v1/llm/surfaces/${id}`)
    report.ok(`surface editor saves a ${provider} surface`, saved.status === 200, saved.status === 200 ? '' : (await page.locator('form [role=alert]').allTextContents()).join(' | ') || JSON.stringify(saved.json).slice(0, 160))
    if (saved.status !== 200) await page.getByRole('button', { name: 'Cancel' }).click().catch(() => {})
    await settle(page, 200)
  }
  // capacity panel for a seeded surface and edit round trip
  await page.getByRole('button', { name: 'Capacity of surface local-models' }).click()
  await settle(page)
  report.ok('capacity panel renders for a surface', /capacity|observed|policy/i.test(await page.textContent('main')))
  await page.getByRole('button', { name: 'Edit surface e2e-openai', exact: true }).click()
  await byLabel('Allowed models').fill('model-a\nmodel-b')
  await page.getByRole('button', { name: 'Save surface' }).click()
  await settle(page)
  const edited = await api('admin', 'GET', '/api/v1/llm/surfaces/e2e-openai')
  report.ok('editing a surface stores the change', JSON.stringify(edited.json?.spec?.surface?.models) === JSON.stringify(['model-a', 'model-b']), JSON.stringify(edited.json?.spec?.surface?.models))
  for (const [provider] of providers) {
    await page.getByRole('button', { name: `Delete surface e2e-${provider}`, exact: true }).click().catch(() => {})
    await page.getByRole('button', { name: /Confirm delete/ }).click().catch(() => {})
    await settle(page, 200)
  }
  const left = (await api('admin', 'GET', '/api/v1/llm/surfaces/')).json.filter((x) => x.id.startsWith('e2e-'))
  report.ok('deleting the test surfaces removed them', left.length === 0, left.map((x) => x.id).join(','))
  await page.getByRole('button', { name: 'batches' }).click()
  await settle(page)
  report.ok('batches tab renders the batch controls', (await page.textContent('main')).includes('Newest first'))
  report.ok('llmplex had no unexpected requests', unexpected(s, [/Failed to load resource/, /api\/v1\/llm\/surfaces\/e2e-.* -> (400|404|409)/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/, /api\/v1\/llm\/surfaces\/e2e-.* -> (400|404|409)/])))
  await s.context.close()
})

await section('connect', async () => {
  const s = await session('admin')
  const { page } = s
  const profiles = (await api('admin', 'GET', '/api/v1/clients/profiles')).json
  report.ok('client profiles are listed', profiles.length > 3, `${profiles.length}`)
  await page.goto(base + '/access-points/team-research')
  await settle(page)
  // copy buttons copy: first endpoint on the detail page
  const endpoints = page.getByRole('region', { name: 'Endpoints' })
  await endpoints.getByRole('button', { name: /^Copy/ }).first().click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  report.ok('endpoint Copy button puts the URL on the clipboard', copied.startsWith(`${h.api_url}/ap/team-research/`), copied)
  report.ok('endpoints list no URL on the console origin', !(await endpoints.textContent()).includes(base))
  await page.getByRole('button', { name: 'Connect a client' }).click()
  const dialog = page.getByRole('dialog', { name: /Connect a client to team-research/ })
  report.ok('wizard opens as a modal with focus inside', await dialog.isVisible() && await page.evaluate(() => !!document.activeElement?.closest('[role=dialog]')))
  const options = ['generic', ...profiles.filter((p) => p.protocols.some((x) => x !== 'claude-apps')).map((p) => p.id)]
  for (const id of options) {
    await dialog.locator('select').selectOption(id)
    await dialog.getByRole('button', { name: 'Show connection details' }).click()
    const details = dialog.getByLabel('Connection details')
    const err = dialog.locator('p[role=alert]')
    await details.or(err).first().waitFor({ timeout: 10000 }).catch(() => {})
    const ok = await details.isVisible().catch(() => false)
    const msg = ok ? '' : (await err.first().textContent().catch(() => '')) ?? ''
    report.ok(`connect wizard for ${id} shows details or a clear refusal`, ok || /cannot be connected|not applicable|does not|No client/i.test(msg), msg.slice(0, 160))
    if (ok) {
      const snippets = await details.locator('pre').count()
      report.ok(`connect details for ${id} carry no token value`, !(await details.textContent()).includes(h.admin_token))
      if (id === 'generic') report.ok('generic connect details have snippets', snippets > 0, `${snippets}`)
    }
  }
  await dialog.locator('select').selectOption('generic')
  await dialog.getByRole('button', { name: 'Show connection details' }).click()
  await dialog.getByLabel('Connection details').waitFor()
  await dialog.getByRole('button', { name: /^Copy code for/ }).first().click()
  const snip = await page.evaluate(() => navigator.clipboard.readText())
  report.ok('snippet Copy button copies the snippet', snip.length > 20 && snip.includes(h.api_url), snip.slice(0, 80))
  await page.keyboard.press('Escape')
  report.ok('Escape closes the wizard and returns focus to Connect a client', !(await dialog.isVisible().catch(() => false)) && (await page.evaluate(() => document.activeElement?.textContent)) === 'Connect a client')
  report.ok('connect flows had no unexpected requests', unexpected(s, [/Failed to load resource/, /overview\/access-points\/.*\/connect.* -> (404|409|422)/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/, /overview\/access-points\/.*\/connect.* -> (404|409|422)/])))
  await s.context.close()
})

await section('identity', async () => {
  const s = await session('admin')
  const { page } = s
  // Agent identities
  await page.goto(base + '/agents')
  await settle(page)
  await page.getByRole('button', { name: 'Tutor agent' }).click()
  await page.getByRole('button', { name: 'Show setup plan for ci' }).click()
  await settle(page)
  report.ok('agent identity plan shows the issuer', (await page.textContent('main')).includes('agents.console.example'))
  await page.getByRole('button', { name: 'Test a token against ci' }).click()
  await page.getByLabel('Workload token').fill(h.workload_token)
  await page.getByRole('button', { name: 'Test token' }).click()
  await settle(page)
  report.ok('a valid workload token verifies', /valid for this/i.test(await page.textContent('main')) || /Token is valid/i.test(await page.textContent('main')), (await page.textContent('main')).slice(-200))
  report.ok('the token value is not echoed on the page', !(await page.textContent('main')).includes(h.workload_token))
  await page.getByLabel('Workload token').fill('not.a.token')
  await page.getByRole('button', { name: 'Test token' }).click()
  await settle(page)
  report.ok('a malformed token reports not valid, not a crash', /not valid/i.test(await page.textContent('main')))
  await page.getByRole('button', { name: 'Add identity' }).click()
  await settle(page)
  report.ok('add identity form opens with a preset choice', (await page.locator('main select').count()) > 0)
  report.ok('agents page had no unexpected requests', unexpected(s, [/Failed to load resource/, /identities\/verify.* -> 4/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/, /identities\/verify.* -> 4/])))

  // User identity
  await page.goto(base + '/identity/users')
  await settle(page)
  await page.getByRole('button', { name: 'Show setup plan for corp' }).click()
  await settle(page)
  report.ok('user provider plan shows the issuer', (await page.textContent('main')).includes('login.console.example'))
  await page.getByRole('button', { name: 'Test a token against corp' }).click()
  await page.getByLabel('User token').fill(h.user_token)
  await page.getByRole('button', { name: 'Test token' }).click()
  await settle(page)
  report.ok('a valid user token verifies', /valid for this provider/i.test(await page.textContent('main')), (await page.textContent('main')).slice(-200))
  await page.getByRole('button', { name: 'Add provider' }).click()
  await settle(page)
  report.ok('add provider opens a form with a preset picker', (await page.getByRole('form').count()) > 0 || (await page.locator('main select').count()) > 0)
  report.ok('identity pages had no unexpected requests', unexpected(s, [/Failed to load resource/, /user-providers\/verify.* -> 4/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/, /user-providers\/verify.* -> 4/])))
  await s.context.close()
})

await section('mcplex', async () => {
  const s = await session('admin')
  const { page } = s
  await page.goto(base + '/mcplex')
  await settle(page)
  await page.getByRole('button', { name: 'Tool connections' }).click()
  await settle(page)
  report.ok('connections list shows the seeded connection', (await page.textContent('main')).includes('local-tools'))
  await page.getByRole('button', { name: 'Edit auth for local-tools' }).click()
  await page.getByLabel('Allowed tools').fill('echo\nadd')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await settle(page)
  const conn = await api('admin', 'GET', '/api/v1/mcp/connections/local-tools')
  report.ok('saving the auth editor stores the tool policy', JSON.stringify(conn.json?.spec?.tools?.allow ?? conn.json?.spec?.tools) .includes('echo'), JSON.stringify(conn.json?.spec?.tools))
  const call = await fetch(`${h.api_url}/ap/local/mcp`, { method: 'POST', headers: { Authorization: `Bearer ${h.admin_token}`, 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-06-18' }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' }) })
  report.ok('the narrowed policy is enforced by the gateway', !(await call.text()).includes('reset_counter'))
  report.ok('mcplex had no unexpected requests', unexpected(s, [/Failed to load resource/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/])))
  await s.context.close()
})

const ROUTES = ['/overview', '/dashboard', '/mcplex', '/a2aplex', '/llmplex', '/skillsplex', '/clientplex', '/quotaplex', '/usage', '/agents', '/access-points', '/access-points/new', '/access-points/local', '/access-points/private', '/identity/users', '/runs', '/deploy', '/permissions', '/no-such-page']

await section('no-role', async () => {
  const s = await session('norole')
  const { page } = s
  const main = async () => (await page.textContent('main')) ?? ''
  for (const route of ROUTES) {
    s.problems.length = 0
    await page.goto(base + route)
    await settle(page, 300)
    const text = await main()
    const stuck = (await page.getByRole('status').allTextContents()).some((t) => /^Loading|Verifying/.test(t.trim()))
    report.ok(`no-role ${route}: no infinite spinner`, !stuck)
    report.ok(`no-role ${route}: no raw 403 or JSON on the page`, !/ROLE_REQUIRED|insufficient role|\{"code"|HTTP 403|Request failed/.test(text), text.slice(0, 120))
    const requests403 = s.problems.filter((p) => p.kind === 'http' && /-> 403/.test(p.text))
    report.ok(`no-role ${route}: makes no request the server refuses`, requests403.length === 0, requests403.map((p) => p.text).join(' | '))
    if (!['/overview', '/usage', '/access-points', '/access-points/local', '/no-such-page'].includes(route)) report.ok(`no-role ${route}: says which role is needed`, /You need the admin( or deployer)? role|not found|Access denied|Page not found/.test(text + (await page.locator('[role=alert]').allTextContents()).join(' ')), text.slice(0, 120))
  }
  await page.goto(base + '/overview')
  await settle(page)
  const home = await main()
  report.ok('home shows who you are', home.includes('bob@localhost') && home.includes(h.norole_principal))
  report.ok('home lists the access points that admit bob', home.includes('Local development') && home.includes('Research team'))
  report.ok('home hides the access point that does not admit bob', !home.includes('Private (someone else only)') && !home.includes('Shared batch pool'))
  report.ok('navigation offers no operator screens', !(await page.locator('nav[aria-label=Main] a').allTextContents()).some((t) => /MCPlex|A2APlex|LLMPlex|Agents|Runs|User identity/.test(t)))
  report.ok('admin-only navigation is disabled with a reason', (await page.locator('nav[aria-label=Main] [aria-disabled=true]').count()) >= 1)
  const cliUsage = cliJSON('norole', 'usage', 'summary', '-o', 'json')
  const total = cliUsage.groups.reduce((n, g) => n + Number(g.requests), 0)
  const shown = await page.locator('section[aria-label="Your usage totals"], [aria-label="Your usage totals"]').locator('[aria-label=Requests] p').nth(1).textContent()
  report.ok('own usage on the home page equals the CLI summary', Number(shown.replace(/,/g, '')) === total, `${shown} vs ${total}`)
  await page.getByRole('button', { name: 'Connect a client to local' }).click()
  const panel = page.getByRole('dialog', { name: 'Connect a client to local' })
  await panel.getByRole('button', { name: 'Show connection details' }).click()
  const details = panel.getByLabel('Connection details')
  await details.waitFor({ timeout: 10000 })
  report.ok('the real Connect wizard shows server-built URLs and snippets to a no-role caller', (await details.textContent()).includes(`${h.api_url}/ap/local/`) && (await details.locator('pre').count()) > 0)
  await panel.getByRole('button', { name: /^Copy/ }).first().click()
  report.ok('copy works for a no-role caller', (await page.evaluate(() => navigator.clipboard.readText())).startsWith(`${h.api_url}/ap/local/`))
  await page.keyboard.press('Escape')
  await page.goto(base + '/usage')
  await settle(page)
  report.ok('usage shows only bob\'s records', (await page.$$eval('section[aria-label="Requests"] tbody tr', (r) => r.length)) === cliJSON('norole', 'usage', 'records', '-o', 'json').records.length)
  report.ok('usage hides the price admin for a no-role caller', !(await main()).includes('Model prices'))
  await page.goto(base + '/access-points/local')
  await settle(page)
  report.ok('access point detail offers endpoints and no Edit or Delete', (await main()).includes('/ap/local/') && (await page.getByRole('link', { name: 'Edit' }).count()) === 0 && (await page.getByRole('button', { name: 'Delete' }).count()) === 0)
  await page.goto(base + '/access-points/private')
  await settle(page)
  report.ok('an access point that does not admit bob reads as not found', /not found/i.test(await main()))
  await s.context.close()
})

await section('deployer', async () => {
  const s = await session('deployer')
  const { page } = s
  const main = async () => (await page.textContent('main')) ?? ''
  for (const route of ['/overview', '/mcplex', '/llmplex', '/agents', '/identity/users', '/access-points', '/access-points/local', '/usage', '/dashboard', '/permissions']) {
    s.problems.length = 0
    await page.goto(base + route)
    await settle(page, 400)
    const text = await main()
    const stuck = (await page.getByRole('status').allTextContents()).some((t) => /^Loading|Verifying/.test(t.trim()))
    report.ok(`deployer ${route}: no infinite spinner`, !stuck)
    report.ok(`deployer ${route}: no raw 403 or JSON on the page`, !/ROLE_REQUIRED|insufficient role|\{"code"|HTTP 403/.test(text), text.slice(0, 120))
    const refused = s.problems.filter((p) => p.kind === 'http' && /-> 403/.test(p.text))
    report.ok(`deployer ${route}: makes no request the server refuses`, refused.length === 0, refused.map((p) => p.text).join(' | '))
  }
  await page.goto(base + '/access-points')
  await settle(page)
  report.ok('deployer sees Create access point disabled with the reason', await page.getByRole('button', { name: 'Create access point' }).isDisabled() && (await main()).includes('You need the admin role'))
  await page.goto(base + '/access-points/local')
  await settle(page)
  report.ok('deployer sees Edit and Delete disabled', await page.getByRole('button', { name: 'Edit' }).isDisabled() && await page.getByRole('button', { name: 'Delete' }).isDisabled())
  await page.goto(base + '/llmplex')
  await settle(page)
  await page.getByRole('button', { name: 'batches' }).click()
  await settle(page)
  report.ok('batches for a deployer says the admin role is needed', /You need the admin role for this/.test(await main()))
  await page.goto(base + '/usage')
  await settle(page)
  report.ok('deployer usage prices are listed but not editable', await page.getByRole('button', { name: 'Add price' }).isDisabled())
  await s.context.close()
})

await section('dashboard', async () => {
  const s = await session('admin')
  const { page } = s
  await page.goto(base + '/dashboard')
  await page.waitForTimeout(7000) // let the live stream deliver its first event
  const text = await page.textContent('main')
  const sum = cliJSON('admin', 'usage', 'summary', '-o', 'json')
  const total = sum.groups.reduce((n, g) => n + Number(g.requests), 0)
  const requests = await page.locator('p', { hasText: /^Requests$/ }).locator('xpath=following-sibling::p').first().textContent()
  report.ok('dashboard requests equal the usage ledger (after the live stream starts)', Number(requests.replace(/,/g, '')) === total, `${requests} vs ${total}`)
  report.ok('dashboard shows cost per currency and basis from the ledger', /USD [0-9.]+/.test(text) && /estimated|settled/.test(text), text.slice(0, 200))
  await page.goto(base + '/llmplex')
  await settle(page)
  await page.getByRole('button', { name: 'costs' }).click()
  await settle(page)
  const costs = await page.textContent('main')
  const llm = cliJSON('admin', 'usage', 'summary', '-o', 'json', '--plane', 'llmplex')
  report.ok('LLMPlex costs tab lists the ledger models', llm.groups.length === 0 || costs.includes('mock-gpt'), costs.slice(0, 160))
  await s.context.close()
})

await section('keyboard-and-login', async () => {
  const s = await session('admin')
  const { page } = s
  for (const route of ['/overview', '/access-points/new', '/usage', '/llmplex']) {
    await page.goto(base + route)
    await settle(page)
    let unfocused = 0, seen = new Set()
    for (let i = 0; i < 60; i++) {
      await page.keyboard.press('Tab')
      const r = await page.evaluate(() => { const e = document.activeElement; if (!e || e === document.body) return null; const cs = getComputedStyle(e); const visible = (cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0) || cs.boxShadow !== 'none'; return { key: e.tagName + (e.id || e.getAttribute('aria-label') || e.textContent?.slice(0, 20)), visible } })
      if (!r) continue
      if (seen.has(r.key)) break
      seen.add(r.key)
      if (!r.visible) unfocused++
    }
    report.ok(`${route}: every tab stop shows a focus indicator`, unfocused === 0 && seen.size > 3, `${unfocused} without, ${seen.size} stops`)
  }
  await page.goto(base + '/access-points/new')
  await settle(page)
  await page.getByLabel(/Access point ID/).fill('enter-submit')
  await page.getByRole('button', { name: 'Add tool connection' }).click()
  await page.getByLabel('Tool connection').selectOption('local-tools')
  await page.getByLabel(/Access point ID/).press('Enter')
  await page.waitForURL('**/access-points/enter-submit', { timeout: 5000 }).catch(() => {})
  report.ok('Enter in a form field submits the access point form', page.url().endsWith('/access-points/enter-submit'))
  const created = await api('admin', 'GET', '/api/v1/access-points/enter-submit')
  const gone = await api('admin', 'DELETE', '/api/v1/access-points/enter-submit', undefined, { 'If-Match': `"${created.json?.revision}"` })
  report.ok('cleanup of the Enter-submit access point', gone.status === 204 || gone.status === 200, String(gone.status))
  await s.context.close()
  for (const dark of [false, true]) {
    const a = await newSession(browser, base, null, { dark, width: dark ? 1440 : 390, height: 800 })
    await a.page.goto(base + '/login')
    await settle(a.page)
    const issues = (await a11yScan(a.page)).filter((i) => i.rule !== 'h1-missing')
    report.ok(`login (${dark ? 'dark' : 'light'}) passes the accessibility scan`, issues.length === 0, JSON.stringify(issues).slice(0, 300))
    report.ok(`login (${dark ? 'dark' : 'light'}) follows the theme`, (await a.page.evaluate(() => getComputedStyle(document.body).backgroundColor)) === (dark ? 'rgb(11, 13, 19)' : 'rgb(255, 255, 255)'))
    await a.page.getByLabel('Bearer Token').fill('not-a-token')
    await a.page.getByRole('button', { name: 'Sign In' }).click()
    await a.page.getByRole('alert').waitFor({ timeout: 5000 })
    report.ok(`login (${dark ? 'dark' : 'light'}) rejects a bad token with a message`, /rejected|valid|verification/i.test(await a.page.getByRole('alert').first().textContent()))
    await a.context.close()
  }
})

await section('permissions', async () => {
  const s = await session('admin')
  const { page } = s
  await page.goto(base + '/permissions')
  await settle(page)
  await page.getByLabel('Canonical principal ID').fill(h.norole_principal)
  await page.getByRole('button', { name: 'Load scopes' }).click()
  await settle(page)
  report.ok('loading shows the principal and its current grants', (await page.textContent('main')).includes('llm:model:mock-gpt'))
  await page.getByLabel('Add custom scope').fill('llm:model:extra-model')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await page.getByRole('button', { name: 'Save permissions' }).click()
  await settle(page)
  const stored = await api('admin', 'GET', `/auth/users/${h.norole_principal}/scopes`)
  report.ok('saving stores the added scope', JSON.stringify(stored.json).includes('llm:model:extra-model'), JSON.stringify(stored.json).slice(0, 200))
  await page.getByRole('tab', { name: 'Native governance' }).click()
  await settle(page)
  report.ok('native governance tab loads', (await page.textContent('main')).includes('Native agent governance'))
  report.ok('permissions had no unexpected requests', unexpected(s, [/Failed to load resource/]).length === 0, JSON.stringify(unexpected(s, [/Failed to load resource/])))
  await s.context.close()
})

await browser.close()
report.finish()
