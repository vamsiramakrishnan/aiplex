import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Overview from '../pages/Overview'
import ConnectWizard from '../components/ConnectWizard'
import { accessLabel } from '../components/SubjectDetailPanel'
import * as api from '../api/overview'
import * as aps from '../api/accessPoints'
import { connectErrorMessage } from '../api/overview'

vi.mock('../api/overview', async (original) => ({
  ...(await original<typeof import('../api/overview')>()),
  listSubjects: vi.fn(), getSubject: vi.fn(), getAccessPointOverview: vi.fn(), listAllOverviewAccessPoints: vi.fn(), getConnect: vi.fn(), listClientProfiles: vi.fn(),
}))
vi.mock('../api/accessPoints', async (original) => ({ ...(await original<typeof import('../api/accessPoints')>()), listAccessPoints: vi.fn() }))

const usage = (over: Partial<api.OverviewUsage> = {}): api.OverviewUsage => ({
  requests: 10, records: 10, errors: 0, denied: 0, tokens: { input_tokens: 100, output_tokens: 50 }, tokens_unknown: {}, cost: [{ currency: 'USD', basis: 'settled', amount: '1500', scale: 6 }], unknown_cost_records: 0, ...over,
})
const subject = (over: Partial<api.OverviewSubject> = {}): api.OverviewSubject => ({
  kind: 'agent', id: 'tutor-agent', display_name: 'Tutor', identity: { source: 'workload identity', detail: 'github-actions binding' },
  access_points: ['ide-team'], last_seen: '2026-11-09T10:00:00Z', usage: usage(), known_from: ['registry'], ...over,
})
const page = (over: Partial<api.SubjectPage> = {}): api.SubjectPage => ({
  kind: 'agent', since: '2026-11-08T00:00:00Z', subjects: [subject()], usage_available: true, usage_truncated: false, derived_from: [], limitations: [], ...over,
})
const coverage = (state: api.QuotaCoverage['state'], detail = 'detail text'): api.AccessPointOverview['quota'] => ({ access_point_id: 'x', coverage: { state, detail }, allocations: null })
const apOverview = (id: string, state: api.QuotaCoverage['state'], detail?: string) => ({ quota: { ...coverage(state, detail), access_point_id: id } }) as unknown as api.AccessPointOverview
const listRow = (id: string, state: api.QuotaCoverage['state'], detail: string): api.AccessPointListRow => ({ id, caller_kinds: ['user'], counts: { models: 1, tools: 0, skills: 0 }, quota_coverage: { state, detail } })
const detail = (over: Partial<api.SubjectDetail> = {}): api.SubjectDetail => ({
  kind: 'agent', id: 'tutor-agent', display_name: 'Tutor', identity: { source: 'workload identity', detail: 'binding gh' },
  access_points: [{ id: 'ide-team', admits: 'unknown', reason: 'admitted if a member of a listed group' }],
  access: {
    models: [
      { kind: 'model', name: 'sure-model', access_point_id: 'ide-team', state: 'granted', reasons: [{ type: 'scope', detail: 'has llm:model:sure-model' }] },
      { kind: 'model', name: 'maybe-model', access_point_id: 'ide-team', state: 'unknown', reasons: [{ type: 'role-binding', detail: 'depends on group membership, which is not stored' }] },
      { kind: 'model', name: 'cond-model', access_point_id: 'ide-team', state: 'granted', reasons: [{ type: 'role-binding', ref: 'rb1', detail: 'if member of platform-eng', conditional: true }] },
      { kind: 'model', name: 'no-model', access_point_id: 'ide-team', state: 'not_granted', reasons: [{ type: 'agent-ceiling', detail: 'outside the agent scope ceiling' }] },
    ],
    tools: [], skills: null, scopes_known: false, scopes_note: 'No registered agent resolves this identity.', truncated: false,
  },
  quota: { access_points: [{ access_point_id: 'ide-team', qos_class: 'interactive', coverage: { state: 'uncovered', detail: 'no active binding' }, allocations: { budgets: [], rates: [{ id: 'r1' }], concurrency: null } }], balance: { available: false, reason: 'balances are not tracked per subject' } },
  since: 'x', last_seen: null, usage: usage({ tokens: {}, cost: [] }), usage_by_model: [{ key: { model_id: 'gemini' }, usage: usage({ tokens: {}, cost: [] }) }], usage_by_tool: [], notes: ['a server note'], known_from: [], ...over,
})

let client: QueryClient
function show() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><MemoryRouter><Overview /></MemoryRouter></QueryClientProvider>)
}
const apiError = (message: string, status: number, code?: string) => Object.assign(new Error(message), { status, code })
const ap = (id: string): aps.AccessPointRecord => ({ id, revision: 1, spec: { id, caller_kinds: ['user'], subjects: {}, models: [{ name: 'm', surface_ref: 's', native_model: 'm' }], qos: { class: 'interactive' } } })

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.listSubjects).mockResolvedValue(page())
  vi.mocked(api.getSubject).mockResolvedValue(detail())
  vi.mocked(aps.listAccessPoints).mockResolvedValue([ap('ide-team'), ap('batch')])
  vi.mocked(api.listAllOverviewAccessPoints).mockResolvedValue([listRow('ide-team', 'uncovered', 'no active quota binding matches'), listRow('batch', 'covered', 'bound')])
  vi.mocked(api.listClientProfiles).mockResolvedValue([{ id: 'codex', name: 'Codex', surface: 'cli', modes: ['gateway'], protocols: ['openai-responses'] }])
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true })
})

describe('subject tabs', () => {
  it('shows who, how they authenticate, access points, last seen, requests, tokens and cost', async () => {
    show()
    const row = (await screen.findByText('Tutor (tutor-agent)')).closest('tr')!
    expect(within(row).getByText('workload identity')).toBeInTheDocument()
    expect(within(row).getByText(/github-actions binding/)).toBeInTheDocument()
    expect(within(row).getByText('ide-team')).toBeInTheDocument()
    expect(within(row).getByText('10')).toBeInTheDocument()
    expect(within(row).getByText(/in 100 \/ out 50/)).toBeInTheDocument()
    expect(within(row).getByText('USD 0.001500')).toBeInTheDocument()
    expect(api.listSubjects).toHaveBeenCalledWith('agent', '24h', '')
  })

  it('never shows tokens, cost or usage that was not reported as zero', async () => {
    vi.mocked(api.listSubjects).mockResolvedValue(page({ subjects: [
      subject({ id: 'a', display_name: undefined, usage: usage({ tokens: {}, cost: [], unknown_cost_records: 0 }) }),
      subject({ id: 'b', display_name: undefined, usage: usage({ tokens: { input_tokens: 0, output_tokens: 0 }, tokens_unknown: { output_tokens: 3 }, requests: 0, records: 4 }) }),
      subject({ id: 'c', display_name: undefined, usage: null }),
    ] }))
    show()
    const a = (await screen.findByText('a')).closest('tr')!
    expect(a).toHaveTextContent('in unknown / out unknown')
    expect(a).toHaveTextContent(/out unknown\s*unknown/)    // cost
    const b = screen.getByText('b').closest('tr')!
    expect(b).toHaveTextContent('in 0 / out 0 (+3 unreported)')  // reported zero stays 0, missing is flagged
    const c = screen.getByText('c').closest('tr')!
    expect(within(c).getAllByText('unknown').length).toBeGreaterThanOrEqual(3)
  })

  it('treats a ledger that is not configured as unknown, and shows server limitations and truncation', async () => {
    vi.mocked(api.listSubjects).mockResolvedValue(page({
      kind: 'user', usage_available: false, usage_truncated: true,
      limitations: ['Users are derived from access points and usage; there is no user directory.'],
      subjects: [subject({ kind: 'user', id: 'u1', display_name: undefined, last_seen: null })],
    }))
    show()
    fireEvent.click(await screen.findByRole('tab', { name: 'Users' }))
    expect(await screen.findByText(/there is no user directory/)).toBeInTheDocument()
    expect(screen.getByText(/Usage is truncated/)).toBeInTheDocument()
    expect(screen.getByText(/ledger is not configured/)).toBeInTheDocument()
    const row = screen.getByText('u1').closest('tr')!
    expect(row).not.toHaveTextContent('never seen')       // unknown, not "never"
    expect(row).toHaveTextContent('unknown')
    expect(api.listSubjects).toHaveBeenCalledWith('user', '24h', '')
  })

  it('does not fold costs of different currencies together', async () => {
    vi.mocked(api.listSubjects).mockResolvedValue(page({ subjects: [subject({ usage: usage({ cost: [
      { currency: 'USD', basis: 'settled', amount: '100', scale: 2 }, { currency: 'EUR', basis: 'estimated', amount: '5', scale: 2 },
    ], unknown_cost_records: 2 }) })] }))
    show()
    const row = (await screen.findByText('Tutor (tutor-agent)')).closest('tr')!
    expect(row).toHaveTextContent('USD 1.00')
    expect(row).toHaveTextContent('EUR 0.05')
    expect(row).toHaveTextContent('+2 records with unknown cost')
  })

  it('pages with the server cursor and passes the chosen range', async () => {
    vi.mocked(api.listSubjects).mockResolvedValueOnce(page({ next_cursor: 'abc' })).mockResolvedValue(page({ subjects: [subject({ id: 'second', display_name: undefined })] }))
    show()
    await screen.findByText('Tutor (tutor-agent)')
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByText('second')).toBeInTheDocument()
    expect(api.listSubjects).toHaveBeenLastCalledWith('agent', '24h', 'abc')
    fireEvent.click(screen.getByRole('button', { name: 'Previous' }))
    await screen.findByText('Tutor (tutor-agent)')
    fireEvent.change(screen.getByLabelText('Time range'), { target: { value: '7d' } })
    await waitFor(() => expect(api.listSubjects).toHaveBeenLastCalledWith('agent', '7d', ''))
  })

  it('shows a forbidden state for viewers', async () => {
    vi.mocked(api.listSubjects).mockRejectedValue(apiError('forbidden', 403))
    show()
    expect(await screen.findByText(/administrators and deployers only/)).toBeInTheDocument()
  })

  it('explains an empty tab', async () => {
    vi.mocked(api.listSubjects).mockResolvedValue(page({ subjects: [] }))
    show()
    expect(await screen.findByText(/No agents found/)).toBeInTheDocument()
  })
})

describe('subject detail', () => {
  const open = async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Details for tutor-agent' }))
    return screen.findByRole('complementary', { name: 'Details for tutor-agent' })
  }

  it('renders unknown and conditional access honestly, never as granted', async () => {
    const panel = await open()
    const models = await within(panel).findByRole('table', { name: 'Models' })
    const state = (name: string) => within(within(models).getByText(name).closest('tr')!)
    expect(state('sure-model').getByText('Granted')).toBeInTheDocument()
    expect(state('maybe-model').getByText('Unknown')).toBeInTheDocument()
    expect(state('maybe-model').queryByText('Granted')).toBeNull()
    expect(state('maybe-model').getByText(/depends on group membership/)).toBeInTheDocument()
    expect(state('cond-model').getByText('Conditional')).toBeInTheDocument()
    expect(state('cond-model').queryByText('Granted')).toBeNull()
    expect(state('cond-model').getByText('conditional')).toBeInTheDocument()
    expect(state('cond-model').getByText(/if member of platform-eng/)).toBeInTheDocument()
    expect(state('no-model').getByText('Not granted')).toBeInTheDocument()
    // admission is unknown too
    expect(within(panel).getByText('Unknown', { selector: 'strong' })).toBeInTheDocument()
    expect(within(panel).getByText(/shown as unknown, not as granted/)).toBeInTheDocument()
  })

  it('shows quota coverage, allocations and the reason a balance is unavailable', async () => {
    const panel = await open()
    const quota = await within(panel).findByRole('region', { name: 'Quota' })
    expect(within(quota).getByText(/UNCOVERED/)).toBeInTheDocument()
    expect(within(quota).getByText(/0 budgets, 1 rate limits, 0 concurrency limits/)).toBeInTheDocument()
    expect(within(quota).getByText(/Balance unavailable: balances are not tracked per subject/)).toBeInTheDocument()
  })

  it('shows unknown usage as unknown in the detail too, plus identity and notes', async () => {
    const panel = await open()
    const u = await within(panel).findByRole('region', { name: 'Usage' })
    expect(u).toHaveTextContent('in unknown / out unknown')
    const byModel = within(u).getByRole('table', { name: 'Usage by model' })
    expect(within(byModel).getByText('gemini').closest('tr')).toHaveTextContent('unknown')
    expect(within(panel).getByText('a server note')).toBeInTheDocument()
    expect(within(panel).getByText(/never seen in ledger/)).toBeInTheDocument()
    expect(api.getSubject).toHaveBeenCalledWith('agent', 'tutor-agent', '24h')
  })

  it('accessLabel guards the granted label', () => {
    expect(accessLabel({ state: 'granted', reasons: [] }).text).toBe('Granted')
    expect(accessLabel({ state: 'granted', reasons: [{ type: 'a', conditional: true }, { type: 'b', conditional: true }] }).text).toBe('Conditional')
    expect(accessLabel({ state: 'granted', reasons: [{ type: 'a', conditional: true }, { type: 'b' }] }).text).toBe('Granted')
    expect(accessLabel({ state: 'unknown', reasons: [{ type: 'a' }] }).text).toBe('Unknown')
    expect(accessLabel({ state: 'unknown', reasons: null }).text).toBe('Unknown')
  })

  it('shows a detail error', async () => {
    vi.mocked(api.getSubject).mockRejectedValue(apiError('boom', 500))
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Details for tutor-agent' }))
    expect(await screen.findByText(/Could not load details: boom/)).toBeInTheDocument()
  })
})

describe('access point strip', () => {
  it('flags uncovered access points prominently and shows each one coverage', async () => {
    show()
    const alert = await screen.findByText(/reject/, { selector: 'strong' })
    expect(alert.closest('[role="alert"]')).toHaveTextContent(/ide-team/)
    expect(alert.closest('[role="alert"]')).not.toHaveTextContent(/batch/)
    const ideCoverage = await screen.findByLabelText('Quota coverage for ide-team')
    expect(ideCoverage).toHaveTextContent('Quota UNCOVERED: requests are rejected')
    expect(ideCoverage).toHaveTextContent('no active quota binding matches')
    expect(await screen.findByLabelText('Quota coverage for batch')).toHaveTextContent('Quota covered')
    // One list call for the whole strip, never one detail call per access point.
    expect(api.listAllOverviewAccessPoints).toHaveBeenCalledTimes(1)
    expect(api.getAccessPointOverview).not.toHaveBeenCalled()
  })

  it('does not call an access point covered when the overview list call fails', async () => {
    vi.mocked(api.listAllOverviewAccessPoints).mockRejectedValue(apiError('nope', 500))
    show()
    expect(await screen.findAllByText(/Quota coverage unknown/)).toHaveLength(2)
    expect(screen.queryByText('Quota covered')).toBeNull()
    expect(screen.queryByText(/UNCOVERED/)).toBeNull()
  })

  it('opens the connect wizard from a card', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Connect a client to batch' }))
    expect(await screen.findByRole('dialog', { name: 'Connect a client to batch' })).toBeInTheDocument()
  })
})

const connect: api.ConnectInfo = {
  access_point_id: 'ide-team', client: 'codex',
  base_urls: { openai: 'https://aiplex.example.com/ap/ide-team/v1' }, models: [{ name: 'gpt', protocols: ['openai-responses'] }],
  mcp_url: 'https://aiplex.example.com/ap/ide-team/mcp', mcp_catalog_url: 'https://aiplex.example.com/ap/ide-team/v0.1/servers',
  auth: { scheme: 'Bearer', header: 'Authorization', token_env: 'AIPLEX_TOKEN', caller_kinds: ['user'], steps: ['Sign in with your organisation account'] },
  snippets: [{ title: 'Codex config', language: 'toml', content: 'model_provider = "aiplex"' }], notes: ['Snippets read the token from $AIPLEX_TOKEN'],
}

describe('connect wizard', () => {
  const wizard = () => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ConnectWizard accessPointId="ide-team" onClose={() => {}} /></QueryClientProvider>)

  it('picks a profile, then shows URLs, auth steps, snippets and notes with copy buttons', async () => {
    vi.mocked(api.getConnect).mockResolvedValue(connect)
    wizard()
    const select = await screen.findByLabelText('Client')
    await waitFor(() => expect(within(select).getAllByRole('option').map(o => o.textContent)).toEqual(['Generic (any OpenAI, Anthropic, Google or MCP client)', 'Codex (openai-responses)']))
    fireEvent.change(select, { target: { value: 'codex' } })
    fireEvent.click(screen.getByRole('button', { name: 'Show connection details' }))
    const details = await screen.findByLabelText('Connection details')
    expect(api.getConnect).toHaveBeenCalledWith('ide-team', 'codex')
    expect(within(details).getByText('https://aiplex.example.com/ap/ide-team/v1')).toBeInTheDocument()
    expect(within(details).getByText('https://aiplex.example.com/ap/ide-team/mcp')).toBeInTheDocument()
    expect(within(details).getByText('https://aiplex.example.com/ap/ide-team/v0.1/servers')).toBeInTheDocument()
    expect(within(details).getByText('Sign in with your organisation account')).toBeInTheDocument()
    expect(within(details).getByText('model_provider = "aiplex"')).toBeInTheDocument()
    expect(within(details).getByText('Authorization', { selector: 'code' })).toBeInTheDocument()
    expect(within(details).getByText('$AIPLEX_TOKEN', { selector: 'code' })).toBeInTheDocument()
    expect(within(details).getByText(/Snippets read the token/)).toBeInTheDocument()
    fireEvent.click(within(details).getByRole('button', { name: 'Copy code for Codex config' }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('model_provider = "aiplex"'))
    fireEvent.click(within(details).getByRole('button', { name: 'Copy MCP URL URL' }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith(connect.mcp_url))
  })

  it.each([
    [422, 'CLIENT_NOT_APPLICABLE', 'no model reaches this client', /cannot be connected to this access point: no model reaches this client/],
    [404, 'UNKNOWN_CLIENT', 'client must be one of: generic, codex', /Not found: client must be one of/],
    [409, 'PUBLIC_URL_REQUIRED', 'public URL is not set', /no public URL configured.*public URL is not set/],
    [403, 'FORBIDDEN', 'nope', /administrator or deployer role/],
  ])('explains a %i error with the server message', async (status, code, message, expected) => {
    vi.mocked(api.getConnect).mockRejectedValue(apiError(message, status, code))
    wizard()
    fireEvent.click(await screen.findByRole('button', { name: 'Show connection details' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(expected)
    expect(connectErrorMessage(apiError(message, status, code))).toMatch(expected)
  })

  it('lists the Bedrock and WebSocket endpoints the connect response provides', async () => {
    vi.mocked(api.getConnect).mockResolvedValue({
      ...connect,
      endpoints: [
        { protocol: 'bedrock-converse', transport: 'http', url: 'https://aiplex.example.com/ap/ide-team/model/{model}/converse', label: 'Converse', models: ['claude'] },
        { protocol: 'openai-realtime', transport: 'websocket', url: 'wss://aiplex.example.com/ap/ide-team/v1/realtime', label: 'OpenAI Realtime', query: ['model'], models: ['voice'] },
      ],
    })
    wizard()
    fireEvent.click(await screen.findByRole('button', { name: 'Show connection details' }))
    const details = await screen.findByLabelText('Connection details')
    expect(within(details).getByText('https://aiplex.example.com/ap/ide-team/model/{model}/converse')).toBeInTheDocument()
    expect(within(details).getByText('wss://aiplex.example.com/ap/ide-team/v1/realtime')).toBeInTheDocument()
    expect(details).toHaveTextContent('requires ?model=')
  })

  it('still offers generic when profiles cannot be loaded', async () => {
    vi.mocked(api.listClientProfiles).mockRejectedValue(apiError('boom', 500))
    vi.mocked(api.getConnect).mockResolvedValue({ ...connect, client: 'generic' })
    wizard()
    expect(await screen.findByText(/Could not load client profiles/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Show connection details' }))
    expect(await screen.findByLabelText('Connection details')).toBeInTheDocument()
    expect(api.getConnect).toHaveBeenCalledWith('ide-team', 'generic')
  })
})
