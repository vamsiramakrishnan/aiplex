import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccessPoints from '../pages/AccessPoints'
import * as api from '../api/accessPoints'
import * as apUsage from '../api/accessPointUsage'
import { fieldIssue, parseFieldPath } from '../api/accessPoints'
import { listInstances } from '../api/client'
import * as overview from '../api/overview'
import { accessFrom } from '../hooks/useAccess'

// The signed-in caller's roles; tests change them to check what a non-admin sees.
const who = vi.hoisted(() => ({ roles: ['admin'] as string[] }))
vi.mock('../hooks/useAccess', async (original) => ({ ...(await original<typeof import('../hooks/useAccess')>()), useAccess: () => accessFrom(who.roles) }))

vi.mock('../api/client', async (original) => ({ ...(await original<typeof import('../api/client')>()), listInstances: vi.fn(), listAgents: vi.fn() }))
vi.mock('../api/accessPoints', async (original) => ({
  ...(await original<typeof import('../api/accessPoints')>()),
  listAccessPoints: vi.fn(), getAccessPoint: vi.fn(), putAccessPoint: vi.fn(), deleteAccessPoint: vi.fn(), getQuotaPlan: vi.fn(),
  listModelSurfaces: vi.fn(), listClientRegistrations: vi.fn(), listToolConnections: vi.fn(), listAgents: vi.fn(),
  getAccessPointRouting: vi.fn(), purgeAccessPointCache: vi.fn(),
}))
vi.mock('../api/overview', async (original) => ({ ...(await original<typeof import('../api/overview')>()), getConnect: vi.fn(), listClientProfiles: vi.fn() }))
vi.mock('../api/accessPointUsage', async (original) => ({ ...(await original<typeof import('../api/accessPointUsage')>()), accessPointUsageSummary: vi.fn() }))

const spec: api.AccessPointSpec = {
  id: 'ide-team', display_name: 'IDE team', caller_kinds: ['user', 'client'],
  subjects: { groups: [{ source: 'okta', id: 'platform-eng' }], client_registrations: ['cursor'] },
  models: [{ name: 'gemini-pro', surface_ref: 'vertex-global', native_model: 'gemini-2.5-pro' }],
  tools: [{ connection_ref: 'github' }, { connection_ref: 'search', expose: ['search*'] }],
  skills: [{ instance_ref: 'runbooks' }],
  qos: { class: 'interactive', weight: 4, limits: { requests_per_minute: 120 } },
}
const record: api.AccessPointRecord = { id: 'ide-team', revision: 3, spec }
const surfaces: api.ModelSurfaceRecord[] = [
  { id: 'vertex-global', revision: 1, spec: { surface: { id: 'vertex-global', protocol: 'google-genai', models: ['gemini-2.5-pro', 'gemini-2.5-flash'] } } },
  { id: 'anthropic-us', revision: 1, spec: { surface: { id: 'anthropic-us', protocol: 'anthropic-messages', models: ['claude-sonnet-4-5'] } } },
]
const routing: api.AccessPointRouting = {
  access_point_id: 'ide-team', process_local: true,
  models: [{
    name: 'gemini-pro', strategy: 'priority',
    backends: [
      { surface_ref: 'vertex-global', native_model: 'gemini-2.5-pro', weight: 0, priority: 0, breaker: { state: 'open', consecutive_failures: 3, opened_at: '2026-11-14T10:30:00Z', half_open_at: '2026-11-14T10:31:00Z', recent: [{ at: '2026-11-14T10:30:00Z', outcome: 'upstream_error', upstream_status: 503 }] } },
      { surface_ref: 'anthropic-us', native_model: 'claude-sonnet-4-5', weight: 2, priority: 1, breaker: { state: 'closed', consecutive_failures: 0, recent: [] } },
    ],
  }],
  cache_store: { entries: 4, bytes: 2048, max_bytes: 1048576 },
}
const exec = { id: 'x', endpoint: 'https://t.example/mcp' }
const connections = [
  { id: 'github', revision: 1, spec: { id: 'github', execution: exec } },
  { id: 'search', revision: 1, spec: { id: 'search', execution: exec } },
  { id: 'registration-only', revision: 1, spec: { id: 'registration-only' } },
] as unknown as Awaited<ReturnType<typeof api.listToolConnections>>

function show(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}><Routes><Route path="/access-points/*" element={<AccessPoints />} /></Routes></MemoryRouter>
    </QueryClientProvider>,
  )
}
const apiError = (message: string, status: number, code?: string, field?: string) => Object.assign(new Error(message), { status, code, ...(field ? { details: { field } } : {}) })
const group = (n: number): apUsage.APUsageSummary['groups'][number] => ({
  key: {}, records: n, requests: n, errors: 0, denied: 0, unknown_status: 0, tokens: {}, tokens_unknown: {}, unknown_usage_records: 0, cost: [], unknown_cost_records: 0,
})

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.listAccessPoints).mockResolvedValue([record])
  vi.mocked(api.getAccessPoint).mockResolvedValue(record)
  vi.mocked(api.listModelSurfaces).mockResolvedValue(surfaces)
  vi.mocked(api.listToolConnections).mockResolvedValue(connections)
  vi.mocked(api.listClientRegistrations).mockResolvedValue([{ id: 'cursor', revision: 1, spec: { id: 'cursor', profile_id: 'cursor-ide' } }])
  vi.mocked(api.listAgents).mockResolvedValue([{ client_id: 'tutor-agent', display_name: 'Tutor' } as api.Agent])
  vi.mocked(listInstances).mockResolvedValue([{ id: 'runbooks', display_name: 'Runbooks' } as never])
  vi.mocked(apUsage.accessPointUsageSummary).mockResolvedValue({ group_by: [], groups: [], records_scanned: 0, truncated: false })
  vi.mocked(overview.getConnect).mockRejectedValue(Object.assign(new Error('public URL not set'), { status: 409, code: 'PUBLIC_URL_REQUIRED' }))
  who.roles = ['admin']
  vi.mocked(api.getAccessPointRouting).mockResolvedValue(routing)
  vi.mocked(api.purgeAccessPointCache).mockResolvedValue({ access_point_id: 'ide-team', model: '', purged: 7, process_local: true })
  vi.mocked(api.putAccessPoint).mockImplementation(async (s, revision) => ({ id: s.id, revision: revision + 1, spec: s }))
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true })
})

describe('access point list', () => {
  it('shows name, caller kinds, resource counts and QoS class, and no URL the server did not provide', async () => {
    show('/access-points')
    const card = await screen.findByRole('article', { name: 'Access point IDE team' })
    expect(within(card).getByText('user, client')).toBeInTheDocument()
    expect(within(card).getByText('interactive')).toBeInTheDocument()
    expect(within(card).queryByText(`${window.location.origin}/ap/ide-team`)).toBeNull()
    expect(within(card).getByText('Tools').nextSibling).toHaveTextContent('2')
  })

  it('explains an empty list and a forbidden list', async () => {
    vi.mocked(api.listAccessPoints).mockResolvedValue([])
    const { unmount } = show('/access-points')
    expect(await screen.findByText(/No access points yet/)).toBeInTheDocument()
    unmount()
    vi.mocked(api.listAccessPoints).mockRejectedValue(apiError('forbidden', 403))
    show('/access-points')
    expect(await screen.findByText(/do not have access to access points/)).toBeInTheDocument()
  })
})

describe('create form', () => {
  it('populates pickers from the reference APIs and saves a new access point', async () => {
    show('/access-points/new')
    fireEvent.change(await screen.findByLabelText(/Access point ID/), { target: { value: 'new-ap' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add model' }))
    const surface = await screen.findByLabelText('Model surface')
    await waitFor(() => expect(within(surface).getAllByRole('option').map(o => o.textContent)).toEqual(['Choose a surface…', 'vertex-global · google-cloud · google-genai', 'anthropic-us · google-cloud · anthropic-messages']))
    fireEvent.change(surface, { target: { value: 'vertex-global' } })
    fireEvent.change(screen.getByLabelText('Native model'), { target: { value: 'gemini-2.5-flash' } })
    // name follows the native model until edited
    expect(screen.getByLabelText('Name clients use')).toHaveValue('gemini-2.5-flash')

    fireEvent.click(screen.getByRole('button', { name: 'Add tool connection' }))
    const conn = screen.getByLabelText('Tool connection')
    // only managed-execution connections are offered
    expect(within(conn).getAllByRole('option').map(o => o.textContent)).toEqual(['Choose a connection…', 'github', 'search'])
    fireEvent.change(conn, { target: { value: 'search' } })
    fireEvent.change(screen.getByLabelText(/Expose patterns/), { target: { value: 'search*\nfind_*' } })

    // agents, registrations and skills come from pickers too
    expect(await screen.findByLabelText('Tutor (tutor-agent)')).toBeInTheDocument()
    fireEvent.click(await screen.findByLabelText('cursor (cursor-ide)'))
    fireEvent.click(await screen.findByLabelText('Runbooks (runbooks)'))

    fireEvent.click(screen.getByRole('button', { name: 'Create access point' }))
    await waitFor(() => expect(api.putAccessPoint).toHaveBeenCalled())
    const [saved, revision] = vi.mocked(api.putAccessPoint).mock.calls[0]
    expect(revision).toBe(0)
    expect(saved).toMatchObject({
      id: 'new-ap', caller_kinds: ['user'],
      subjects: { client_registrations: ['cursor'] },
      models: [{ name: 'gemini-2.5-flash', surface_ref: 'vertex-global', native_model: 'gemini-2.5-flash' }],
      tools: [{ connection_ref: 'search', expose: ['search*', 'find_*'] }],
      skills: [{ instance_ref: 'runbooks' }],
    })
    expect(saved.qos).toBeUndefined()
  })

  it('explains that identity constraints are OR-ed together but AND-ed with client registrations', async () => {
    show('/access-points/new')
    const subjects = await screen.findByRole('group', { name: /Who may call/ })
    expect(within(subjects).getByText(/one identity constraint/)).toBeInTheDocument()
    expect(within(subjects).getAllByText(/AND-ed/).length).toBeGreaterThan(0)
  })

  it('requires a caller kind, a valid id and at least one resource before saving', async () => {
    show('/access-points/new')
    const save = await screen.findByRole('button', { name: 'Create access point' })
    expect(save).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/Access point ID/), { target: { value: 'bad id!' } })
    expect(await screen.findByText(/Use letters, digits/)).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('user'))
    expect(screen.getByText('Choose at least one caller kind.')).toBeInTheDocument()
  })

  it('shows a server validation error next to the field it names', async () => {
    vi.mocked(api.putAccessPoint).mockRejectedValue(apiError('model surface "ghost" does not resolve in this tenant', 400, 'INVALID_SPEC', 'models/0/surface_ref'))
    show('/access-points/new')
    fireEvent.change(await screen.findByLabelText(/Access point ID/), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add model' }))
    fireEvent.change(await screen.findByLabelText('Model surface'), { target: { value: 'vertex-global' } })
    fireEvent.change(screen.getByLabelText('Native model'), { target: { value: 'gemini-2.5-pro' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create access point' }))
    const models = await screen.findByRole('group', { name: 'Models' })
    expect(await within(models).findByRole('alert')).toHaveTextContent('model surface "ghost" does not resolve')
    expect(within(screen.getByRole('group', { name: 'Tools' })).queryByRole('alert')).toBeNull()
  })

  it('shows an unrecognised server error at form level', async () => {
    vi.mocked(api.putAccessPoint).mockRejectedValue(apiError('something unexpected', 500))
    show('/access-points/new')
    fireEvent.change(await screen.findByLabelText(/Access point ID/), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add model' }))
    fireEvent.change(await screen.findByLabelText('Model surface'), { target: { value: 'vertex-global' } })
    fireEvent.change(screen.getByLabelText('Native model'), { target: { value: 'gemini-2.5-pro' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create access point' }))
    expect(await screen.findByText('something unexpected')).toBeInTheDocument()
  })

  it('reports an id collision on create as a conflict without offering to overwrite', async () => {
    vi.mocked(api.putAccessPoint).mockRejectedValue(apiError('resource revision changed', 409, 'REVISION_CONFLICT'))
    show('/access-points/new')
    fireEvent.change(await screen.findByLabelText(/Access point ID/), { target: { value: 'ide-team' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add model' }))
    fireEvent.change(await screen.findByLabelText('Model surface'), { target: { value: 'vertex-global' } })
    fireEvent.change(screen.getByLabelText('Native model'), { target: { value: 'gemini-2.5-pro' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create access point' }))
    expect(await screen.findByText(/already exists/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Reload latest/ })).toBeNull()
  })
})

describe('edit form', () => {
  it('sends the current revision and preserves fields the form does not know', async () => {
    vi.mocked(api.getAccessPoint).mockResolvedValue({ ...record, spec: { ...spec, future_field: { keep: true } } as api.AccessPointSpec })
    show('/access-points/ide-team/edit')
    const name = await screen.findByLabelText('Display name')
    expect(screen.getByLabelText(/Access point ID/)).toHaveAttribute('readonly')
    fireEvent.change(name, { target: { value: 'IDE platform' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(api.putAccessPoint).toHaveBeenCalled())
    const [saved, revision] = vi.mocked(api.putAccessPoint).mock.calls[0]
    expect(revision).toBe(3)
    expect(saved).toMatchObject({ display_name: 'IDE platform', future_field: { keep: true }, qos: { class: 'interactive', weight: 4, limits: { requests_per_minute: 120 } } })
  })

  it('on a revision conflict shows it, blocks saving, and only replaces edits after an explicit reload', async () => {
    vi.mocked(api.putAccessPoint).mockRejectedValueOnce(apiError('resource revision changed', 409, 'REVISION_CONFLICT'))
    show('/access-points/ide-team/edit')
    fireEvent.change(await screen.findByLabelText('Display name'), { target: { value: 'My edit' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/changed by someone else/)
    expect(alert).toHaveTextContent(/nothing was overwritten/)
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
    expect(screen.getByLabelText('Display name')).toHaveValue('My edit')
    expect(api.putAccessPoint).toHaveBeenCalledTimes(1)

    vi.mocked(api.getAccessPoint).mockResolvedValue({ id: 'ide-team', revision: 4, spec: { ...spec, display_name: 'Theirs' } })
    fireEvent.click(screen.getByRole('button', { name: 'Reload latest version' }))
    await waitFor(() => expect(screen.getByLabelText('Display name')).toHaveValue('Theirs'))
    expect(screen.queryByText(/changed by someone else/)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(api.putAccessPoint).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.putAccessPoint).mock.calls[1][1]).toBe(4)
  })

  it('enables and renders the QoS section with limits', async () => {
    show('/access-points/ide-team/edit')
    expect(await screen.findByLabelText(/Class/)).toHaveValue('interactive')
    expect(screen.getByLabelText('Requests per minute')).toHaveValue(120)
    expect(screen.getByLabelText('Weight (1-1000)')).toHaveValue(4)
  })
})

describe('detail', () => {
  it('shows endpoint URLs the server built, only for wires it lists, with copy buttons', async () => {
    vi.mocked(overview.getConnect).mockResolvedValue({
      access_point_id: 'ide-team', client: 'generic', base_urls: { genai: 'https://public.example.com/ap/ide-team' }, models: [{ name: 'gemini-pro', protocols: ['google-genai'] }],
      mcp_url: 'https://public.example.com/ap/ide-team/mcp', mcp_catalog_url: 'https://public.example.com/ap/ide-team/v0.1/servers', skills_url: 'https://public.example.com/ap/ide-team/skills',
      auth: { scheme: 'Bearer', header: 'Authorization', token_env: 'AIPLEX_TOKEN', caller_kinds: [], steps: [] }, snippets: [], notes: [],
    })
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    const root = 'https://public.example.com/ap/ide-team'
    await waitFor(() => expect(within(endpoints).getByRole('group', { name: 'Google GenAI' })).toBeInTheDocument())
    expect(within(endpoints).getByText(`${root}/v1beta/models/{model}:{op}`)).toBeInTheDocument()
    expect(within(endpoints).queryByRole('group', { name: 'Anthropic Messages' })).toBeNull()
    expect(within(endpoints).queryByRole('group', { name: 'OpenAI Chat Completions' })).toBeNull()
    expect(within(endpoints).getByText(`${root}/mcp`)).toBeInTheDocument()
    expect(within(endpoints).getByText(`${root}/mcp/search`)).toBeInTheDocument()
    expect(within(endpoints).getByText(`${root}/v0.1/servers`)).toBeInTheDocument()
    expect(within(endpoints).getByText(`${root}/skills`)).toBeInTheDocument()
    fireEvent.click(within(endpoints).getByRole('button', { name: `Copy Endpoint URL for Google GenAI` }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith(`${root}/v1beta/models/{model}:{op}`))
  })

  it('lists subject constraints as a table', async () => {
    show('/access-points/ide-team')
    const table = await screen.findByRole('table', { name: 'Subject constraints' })
    expect(within(table).getByText('platform-eng')).toBeInTheDocument()
    expect(within(table).getByText('cursor')).toBeInTheDocument()
  })

  it('confirms before deleting and sends the revision', async () => {
    vi.mocked(api.deleteAccessPoint).mockResolvedValue(undefined)
    show('/access-points/ide-team')
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    expect(api.deleteAccessPoint).not.toHaveBeenCalled()
    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(api.deleteAccessPoint).toHaveBeenCalledWith('ide-team', 3))
  })

  it('cancelling the confirmation deletes nothing; a delete conflict offers reload', async () => {
    vi.mocked(api.deleteAccessPoint).mockRejectedValue(apiError('resource revision changed', 409, 'REVISION_CONFLICT'))
    show('/access-points/ide-team')
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(api.deleteAccessPoint).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText(/not deleted/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reload latest version' })).toBeInTheDocument()
  })

  it('shows a not-found state', async () => {
    vi.mocked(api.getAccessPoint).mockRejectedValue(apiError('nope', 404))
    show('/access-points/ghost')
    expect(await screen.findByText(/not found/i)).toBeInTheDocument()
  })
})

describe('quota plan panel', () => {
  const plan: api.QuotaPlan = {
    access_point_id: 'ide-team', tenant_id: 't', applied: false,
    fragment: { rates: [{ id: 'ap-ide-team-rpm', rate: 120 }] },
    validation: { valid: true, validator: 'compile' },
    warnings: ['no quota policy is active for this tenant; rendered a standalone policy'],
  }

  it('warns that requests are rejected without a quota binding, then shows the fragment read-only', async () => {
    vi.mocked(api.getQuotaPlan).mockResolvedValue(plan)
    show('/access-points/ide-team')
    const panel = await screen.findByRole('region', { name: 'Quota plan' })
    expect(within(panel).getByRole('note')).toHaveTextContent(/rejected until a matching quota binding is active/)
    expect(within(panel).getByText('Nothing is applied')).toBeInTheDocument()
    expect(api.getQuotaPlan).not.toHaveBeenCalled()
    fireEvent.click(within(panel).getByRole('button', { name: 'Render quota plan' }))
    const rendered = await within(panel).findByLabelText('Rendered quota plan')
    expect(api.getQuotaPlan).toHaveBeenCalledWith('ide-team')
    expect(within(rendered).getByText('Not applied. Read-only preview.')).toBeInTheDocument()
    expect(within(rendered).getByLabelText('Quota policy fragment')).toHaveTextContent('ap-ide-team-rpm')
    expect(within(rendered).getByText(/standalone policy/)).toBeInTheDocument()
    expect(within(rendered).queryByRole('textbox')).toBeNull()
  })

  it('shows no warning or button when there is no QoS block', async () => {
    vi.mocked(api.getAccessPoint).mockResolvedValue({ ...record, spec: { ...spec, qos: undefined } })
    show('/access-points/ide-team')
    const panel = await screen.findByRole('region', { name: 'Quota plan' })
    expect(within(panel).getByText(/no QoS block/)).toBeInTheDocument()
    expect(within(panel).queryByRole('button')).toBeNull()
    expect(within(panel).queryByRole('note')).toBeNull()
  })

  it('reports a forbidden plan', async () => {
    vi.mocked(api.getQuotaPlan).mockRejectedValue(apiError('forbidden', 403))
    show('/access-points/ide-team')
    fireEvent.click(await screen.findByRole('button', { name: 'Render quota plan' }))
    expect(await screen.findByText(/administrator role required/)).toBeInTheDocument()
  })
})

describe('usage panel', () => {
  it('never shows unknown tokens or cost as zero, and does not add currencies', async () => {
    vi.mocked(apUsage.accessPointUsageSummary).mockImplementation(async (_id, groupBy) => {
      if (groupBy[0] === 'caller_kind') {
        return {
          group_by: ['caller_kind'], records_scanned: 5, truncated: false,
          groups: [
            { ...group(3), key: { caller_kind: 'user' }, tokens: { input_tokens: 0, output_tokens: 10 }, tokens_unknown: {}, cost: [{ currency: 'USD', basis: 'settled', amount: '1500', scale: 6, records: 3 }] },
            { ...group(2), key: { caller_kind: 'client' }, tokens: {}, tokens_unknown: {}, unknown_usage_records: 2, cost: [{ currency: 'EUR', basis: 'estimated', amount: '20', scale: 2, records: 1 }], unknown_cost_records: 1 },
          ],
        }
      }
      return { group_by: groupBy as never, records_scanned: 0, truncated: false, groups: [] }
    })
    show('/access-points/ide-team')
    const totals = await screen.findByLabelText('Access point usage totals')
    const tokens = within(totals).getByRole('group', { name: 'Tokens' })
    expect(tokens).toHaveTextContent('Input 0')          // reported zero stays 0
    expect(tokens).toHaveTextContent('Output 10')
    const cost = within(totals).getByRole('group', { name: 'Cost' })
    expect(cost).toHaveTextContent('USD 0.001500')
    expect(cost).toHaveTextContent('EUR 0.20')
    expect(cost).not.toHaveTextContent(/USD 0\.2/)       // no cross-currency sum
    expect(cost).toHaveTextContent(/1 record has unknown cost/)
    const table = screen.getByRole('table', { name: 'Usage by Caller kind' })
    const clientRow = within(table).getByText('client').closest('tr')!
    expect(clientRow).toHaveTextContent('unknown')       // no tokens reported at all
    expect(clientRow).not.toHaveTextContent(/\b0\b.*\b0\b.*EUR/)
    expect(within(clientRow).getAllByText('unknown').length).toBeGreaterThanOrEqual(2)
    expect(within(clientRow).getByText('EUR 0.20')).toBeInTheDocument()
  })

  it('shows unknown cost when a group has no priced records and empty states for model/tool', async () => {
    vi.mocked(apUsage.accessPointUsageSummary).mockImplementation(async (_id, groupBy) => groupBy[0] === 'caller_kind'
      ? { group_by: ['caller_kind'], records_scanned: 1, truncated: true, groups: [{ ...group(1), key: { caller_kind: 'agent' } }] }
      : { group_by: groupBy as never, records_scanned: 0, truncated: false, groups: [] })
    show('/access-points/ide-team')
    const totals = await screen.findByLabelText('Access point usage totals')
    expect(within(totals).getByRole('group', { name: 'Cost' })).toHaveTextContent('unknown')
    expect(within(totals).getByRole('group', { name: 'Tokens' })).toHaveTextContent('Input unknown')
    expect(screen.getByText(/Truncated/)).toBeInTheDocument()
    expect(await screen.findByText(/No model usage recorded/)).toBeInTheDocument()
    expect(screen.getByText(/No tool usage recorded/)).toBeInTheDocument()
  })

  it('shows a forbidden usage state', async () => {
    vi.mocked(apUsage.accessPointUsageSummary).mockRejectedValue(apiError('forbidden', 403))
    show('/access-points/ide-team')
    expect((await screen.findAllByText(/do not have access to usage for this access point/)).length).toBeGreaterThan(0)
  })

  it('filters by this access point and groups by caller kind, model and tool', async () => {
    show('/access-points/ide-team')
    await waitFor(() => expect(apUsage.accessPointUsageSummary).toHaveBeenCalledTimes(3))
    const calls = vi.mocked(apUsage.accessPointUsageSummary).mock.calls
    expect(calls.map(c => c[0])).toEqual(['ide-team', 'ide-team', 'ide-team'])
    expect(calls.map(c => c[1][0]).sort()).toEqual(['caller_kind', 'model_id', 'tool_name'])
  })
})

describe('pure helpers', () => {
  it('reads the field path the server names, never the message text', () => {
    expect(fieldIssue(apiError('model surface "x" does not resolve', 400, 'INVALID_SPEC', 'models/1/surface_ref'))).toEqual({ message: 'model surface "x" does not resolve', field: 'models/1/surface_ref' })
    expect(fieldIssue(apiError('qos.class must match [a-z]', 400, 'INVALID_SPEC'))).toEqual({ message: 'qos.class must match [a-z]' })
    expect(parseFieldPath('models/1/surface_ref')).toEqual({ section: 'models', index: 1, leaf: 'surface_ref' })
    expect(parseFieldPath('qos/limits')).toEqual({ section: 'qos', leaf: 'limits' })
    expect(parseFieldPath('caller_kinds')).toEqual({ section: 'caller_kinds' })
    expect(parseFieldPath('')).toBeUndefined()
  })
})

describe('surface picker with non-Google providers', () => {
  const bedrock: api.ModelSurfaceRecord = { id: 'bedrock-us', revision: 1, spec: { surface: { id: 'bedrock-us', protocol: 'bedrock-converse', models: ['anthropic.claude-3:0'], provider: { id: 'bedrock' } } } }
  const exotic: api.ModelSurfaceRecord = { id: 'exotic', revision: 1, spec: { surface: { id: 'exotic', protocol: 'grpc-inference', models: ['m'], provider: { id: 'acme' } } } }
  const openai: api.ModelSurfaceRecord = { id: 'oai', revision: 1, spec: { surface: { id: 'oai', protocol: 'openai-chat', models: ['gpt-5'], provider: { id: 'openai' } } } }

  it('shows provider and protocol and disables surfaces access points cannot route, with the reason', async () => {
    vi.mocked(api.listModelSurfaces).mockResolvedValue([...surfaces, openai, bedrock, exotic])
    show('/access-points/new')
    fireEvent.click(await screen.findByRole('button', { name: 'Add model' }))
    const surface = await screen.findByLabelText('Model surface')
    await waitFor(() => expect(within(surface).getAllByRole('option').length).toBe(6))
    const options = within(surface).getAllByRole('option') as HTMLOptionElement[]
    const byText = (t: RegExp) => options.find(o => t.test(o.textContent ?? ''))!
    expect(byText(/^oai/).textContent).toBe('oai · openai · openai-chat')
    expect(byText(/^oai/).disabled).toBe(false)
    // Bedrock is routable now
    expect(byText(/^bedrock-us/).textContent).toBe('bedrock-us · bedrock · bedrock-converse')
    expect(byText(/^bedrock-us/).disabled).toBe(false)
    expect(byText(/^exotic/).textContent).toBe('exotic · acme · grpc-inference (unavailable: access points have no client protocol for grpc-inference)')
    expect(byText(/^exotic/).disabled).toBe(true)
  })

  it('blocks saving and explains when an existing model uses an unroutable surface', async () => {
    vi.mocked(api.listModelSurfaces).mockResolvedValue([exotic])
    vi.mocked(api.getAccessPoint).mockResolvedValue({ ...record, spec: { ...spec, models: [{ name: 'c', surface_ref: 'exotic', native_model: 'c' }] } })
    show('/access-points/ide-team/edit')
    expect(await screen.findByText(/Surface exotic is unavailable for access points: access points have no client protocol for grpc-inference/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  })
})

describe('detail endpoint URLs from the server', () => {
  const connectInfo: overview.ConnectInfo = {
    access_point_id: 'ide-team', client: 'generic',
    base_urls: { genai: 'https://public.example.com/ap/ide-team' }, models: [{ name: 'gemini-pro', protocols: ['google-genai'] }],
    mcp_url: 'https://public.example.com/ap/ide-team/mcp', mcp_catalog_url: 'https://public.example.com/ap/ide-team/v0.1/servers', skills_url: 'https://public.example.com/ap/ide-team/skills',
    auth: { scheme: 'Bearer', header: 'Authorization', token_env: 'AIPLEX_TOKEN', caller_kinds: [], steps: [] }, snippets: [], notes: [],
  }

  it('uses the server-built URLs when available', async () => {
    vi.mocked(overview.getConnect).mockResolvedValue(connectInfo)
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    await waitFor(() => expect(within(endpoints).getByText('https://public.example.com/ap/ide-team/v1beta/models/{model}:{op}')).toBeInTheDocument())
    expect(within(endpoints).getByText('https://public.example.com/ap/ide-team/mcp/search')).toBeInTheDocument()
    expect(within(endpoints).getByText('https://public.example.com/ap/ide-team/skills')).toBeInTheDocument()
    expect(within(endpoints).queryByText(window.location.origin + '/ap/ide-team/mcp')).toBeNull()
    expect(within(endpoints).getByText(/built by the server/)).toBeInTheDocument()
  })

  it('never derives URLs from the browser address when the server cannot provide them', async () => {
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    const note = await within(endpoints).findByRole('note')
    expect(note).toHaveTextContent(/could not provide endpoint URLs/)
    expect(note).toHaveTextContent(/no public URL configured/)
    expect(within(endpoints).queryAllByRole('group')).toHaveLength(0)
  })

  it('shows Bedrock, WebSocket and Azure endpoints exactly as the connect response gives them', async () => {
    vi.mocked(overview.getConnect).mockResolvedValue({
      ...connectInfo, base_urls: { openai: 'https://srv.example.com/ap/ide-team/v1' },
      models: [{ name: 'gemini-pro', protocols: ['openai-chat-completions', 'bedrock-converse', 'openai-realtime'] }],
      endpoints: [
        { protocol: 'bedrock-converse', transport: 'http', url: 'https://srv.example.com/ap/ide-team/model/{model}/converse', label: 'Converse', models: ['gemini-pro'] },
        { protocol: 'openai-realtime', transport: 'websocket', url: 'wss://srv.example.com/ap/ide-team/v1/realtime', label: 'OpenAI Realtime', query: ['model'], models: ['gemini-pro'] },
        { protocol: 'azure-openai-chat-completions', transport: 'http', url: 'https://srv.example.com/ap/ide-team/openai/deployments/{model}/chat/completions', label: 'Azure OpenAI deployment shape', models: ['gemini-pro'] },
      ],
    })
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    await waitFor(() => expect(within(endpoints).getByText('https://srv.example.com/ap/ide-team/model/{model}/converse')).toBeInTheDocument())
    expect(within(endpoints).getByText('wss://srv.example.com/ap/ide-team/v1/realtime?model={model}')).toBeInTheDocument()
    expect(within(endpoints).getByText('https://srv.example.com/ap/ide-team/openai/deployments/{model}/chat/completions')).toBeInTheDocument()
    expect(within(endpoints).getByRole('group', { name: 'Azure OpenAI deployment shape' })).toBeInTheDocument()
    expect(within(endpoints).queryByRole('group', { name: 'Amazon Bedrock InvokeModel' })).toBeNull()
  })

  it('launches the connect wizard from the detail view', async () => {
    vi.mocked(overview.listClientProfiles).mockResolvedValue([])
    show('/access-points/ide-team')
    fireEvent.click(await screen.findByRole('button', { name: 'Connect a client' }))
    expect(await screen.findByRole('dialog', { name: 'Connect a client to ide-team' })).toBeInTheDocument()
  })
})

describe('routing panel: missing and empty values', () => {
  const panelOf = async () => screen.findByRole('region', { name: 'Routing' })
  const withRouting = (r: unknown) => vi.mocked(api.getAccessPointRouting).mockResolvedValue(r as api.AccessPointRouting)

  it('treats a null or missing model list as unknown and an empty one as none', async () => {
    withRouting({ access_point_id: 'ide-team', process_local: true, models: null })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText('Routed models: unknown')).toBeInTheDocument()
    cleanup()

    withRouting({ access_point_id: 'ide-team', process_local: true })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText('Routed models: unknown')).toBeInTheDocument()
    cleanup()

    withRouting({ access_point_id: 'ide-team', process_local: true, models: [] })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText(/No routed models/)).toBeInTheDocument()
  })

  it('treats a null or missing backend list as unknown and an empty one as none', async () => {
    withRouting({ access_point_id: 'ide-team', process_local: true, models: [{ name: 'gemini-pro', strategy: 'priority', backends: null }] })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText('Backends: unknown')).toBeInTheDocument()
    cleanup()

    withRouting({ access_point_id: 'ide-team', process_local: true, models: [{ name: 'gemini-pro', strategy: 'priority', backends: [] }] })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText('No backends.')).toBeInTheDocument()
  })

  it('shows when the breaker opened when the API sends it, and nothing extra when it does not', async () => {
    show('/access-points/ide-team')
    const table = await within(await panelOf()).findByRole('table', { name: 'Backends of gemini-pro' })
    const rows = within(table).getAllByRole('row').slice(1)
    expect(within(rows[0]).getAllByRole('cell')[4].textContent).toMatch(/^open, opened at .+, retry at .+$/)
    expect(within(rows[1]).getAllByRole('cell')[4].textContent).toBe('closed')
  })

  it('states the process scope only when the API says the state is process-local, and says so when it does not send it', async () => {
    withRouting({ ...routing, process_local: true })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText(/State is per API process/)).toBeInTheDocument()
    cleanup()

    withRouting({ ...routing, process_local: false })
    show('/access-points/ide-team')
    const off = await panelOf()
    await within(off).findByRole('table', { name: 'Backends of gemini-pro' })
    expect(within(off).queryByText(/per API process/)).toBeNull()
    expect(within(off).queryByText(/Scope of this state: unknown/)).toBeNull()
    cleanup()

    withRouting({ ...routing, process_local: undefined })
    show('/access-points/ide-team')
    expect(await within(await panelOf()).findByText('Scope of this state: unknown.')).toBeInTheDocument()
  })
})

describe('routing panel', () => {
  const panelOf = async () => screen.findByRole('region', { name: 'Routing' })

  /** Like show(), with a control outside the routes that moves the same mounted tree to another access point. */
  function showSwitchable(path: string) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    // ap-2's record is already cached (as after a visit), so the detail stays mounted while the user switches; a cold load would remount it.
    client.setQueryData(['access-point', 'ap-2'], { id: 'ap-2', revision: 1, spec: { ...spec, id: 'ap-2', display_name: 'ap-2' } })
    function GoTo({ to }: { to: string }) {
      const navigate = useNavigate()
      return <button type="button" onClick={() => navigate(to)}>Go to {to}</button>
    }
    return render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[path]}>
          <GoTo to="/access-points/ap-2" />
          <Routes><Route path="/access-points/*" element={<AccessPoints />} /></Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
  }
  const otherPoint = (id: string) => ({ ...record, id, spec: { ...spec, id, display_name: id } })

  it('forgets a purge result when the user moves to another access point in the same view', async () => {
    vi.mocked(api.getAccessPoint).mockImplementation(async (id: string) => otherPoint(id))
    vi.mocked(api.getAccessPointRouting).mockImplementation(async (id: string) => ({ ...routing, access_point_id: id }))
    showSwitchable('/access-points/ap-1')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    fireEvent.click(within(panel).getByRole('button', { name: 'Purge cache' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm purge' }))
    expect(await within(panel).findByText('Removed 7 cached answers of access point ap-1 (this API process).')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Go to /access-points/ap-2' }))
    await screen.findByRole('heading', { level: 1, name: 'ap-2' })
    const next = screen.getByRole('region', { name: 'Routing' })
    await within(next).findByRole('table', { name: 'Backends of gemini-pro' })
    expect(within(next).queryByText(/Removed 7 cached answers/)).toBeNull()
    expect(api.purgeAccessPointCache).toHaveBeenCalledTimes(1)
    expect(api.purgeAccessPointCache).toHaveBeenCalledWith('ap-1')
  })

  it('does not carry an open purge confirmation over to another access point', async () => {
    vi.mocked(api.getAccessPoint).mockImplementation(async (id: string) => otherPoint(id))
    vi.mocked(api.getAccessPointRouting).mockImplementation(async (id: string) => ({ ...routing, access_point_id: id }))
    showSwitchable('/access-points/ap-1')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    fireEvent.click(within(panel).getByRole('button', { name: 'Purge cache' }))
    expect(within(panel).getByRole('group', { name: 'Confirm purge' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Go to /access-points/ap-2' }))
    await screen.findByRole('heading', { level: 1, name: 'ap-2' })
    const next = screen.getByRole('region', { name: 'Routing' })
    await within(next).findByRole('table', { name: 'Backends of gemini-pro' })
    expect(within(next).queryByRole('group', { name: 'Confirm purge' })).toBeNull()
    expect(within(next).getByRole('button', { name: 'Purge cache' })).toBeInTheDocument()
    expect(api.purgeAccessPointCache).toHaveBeenCalledTimes(0)
  })
  const cellsOf = (row: HTMLElement) => within(row).getAllByRole('cell').map(c => c.textContent)

  it('shows each backend in routing order with breaker state, weight, priority and recent outcomes, and real zeros as 0', async () => {
    show('/access-points/ide-team')
    const panel = await panelOf()
    const table = await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    expect(within(panel).getByText('gemini-pro')).toBeInTheDocument()
    expect(within(panel).getByText('priority')).toBeInTheDocument()
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    // order, backend, weight, priority, breaker, consecutive failures, recent outcomes
    expect(cellsOf(rows[0])[0]).toBe('1')
    expect(cellsOf(rows[0])[1]).toContain('vertex-global/gemini-2.5-pro')
    expect(cellsOf(rows[0])[2]).toBe('0')
    expect(cellsOf(rows[0])[3]).toBe('0')
    expect(cellsOf(rows[0])[4]).toContain('open')
    expect(cellsOf(rows[0])[5]).toBe('3')
    expect(cellsOf(rows[0])[6]).toBe('upstream_error(503)')
    expect(cellsOf(rows[1])[0]).toBe('2')
    expect(cellsOf(rows[1])[1]).toContain('anthropic-us/claude-sonnet-4-5')
    expect(cellsOf(rows[1])[2]).toBe('2')
    expect(cellsOf(rows[1])[4]).toBe('closed')
    expect(cellsOf(rows[1])[5]).toBe('0')
    expect(cellsOf(rows[1])[6]).toBe('none recorded')
    expect(within(panel).getByText(/State is per API process/)).toBeInTheDocument()
    expect(within(panel).getByText(/4 entries/)).toBeInTheDocument()
  })

  it('shows unknown for every value the API did not send, never 0, blank or healthy', async () => {
    const partial = {
      access_point_id: 'ide-team', process_local: true,
      models: [{ name: 'gemini-pro', backends: [{ surface_ref: 'vertex-global', native_model: 'gemini-2.5-pro', breaker: { consecutive_failures: 1 } }] }],
    } as unknown as api.AccessPointRouting
    vi.mocked(api.getAccessPointRouting).mockResolvedValue(partial)
    show('/access-points/ide-team')
    const panel = await panelOf()
    const table = await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    const row = within(table).getAllByRole('row')[1]
    const cells = cellsOf(row)
    expect(cells[2]).toBe('unknown') // weight
    expect(cells[3]).toBe('unknown') // priority
    expect(cells[4]).toBe('unknown') // breaker state
    expect(cells[6]).toBe('unknown') // recent outcomes
    expect(within(panel).getByText(/^Strategy:/)).toHaveTextContent('Strategy: unknown')
    expect(panel.textContent).not.toMatch(/healthy/i)
    expect(cells[4]).not.toMatch(/closed/)
  })

  it('shows an error, not an empty panel, when the routing state cannot be read', async () => {
    vi.mocked(api.getAccessPointRouting).mockRejectedValue(apiError('routing backend unavailable', 500))
    show('/access-points/ide-team')
    const panel = await panelOf()
    expect(await within(panel).findByRole('alert')).toHaveTextContent('Could not load routing state: routing backend unavailable')
    expect(within(panel).queryByRole('table')).toBeNull()
    expect(within(panel).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('keeps the last good figures and labels them stale when a later refresh fails', async () => {
    show('/access-points/ide-team')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    vi.mocked(api.getAccessPointRouting).mockRejectedValueOnce(apiError('connection reset', 503))
    fireEvent.click(within(panel).getByRole('button', { name: 'Refresh routing state' }))
    const alert = await within(panel).findByRole('alert')
    expect(alert).toHaveTextContent(/latest refresh of routing state failed \(connection reset\)/)
    expect(alert).toHaveTextContent(/from the last successful refresh at/)
    expect(within(panel).getByRole('table', { name: 'Backends of gemini-pro' })).toBeInTheDocument()
  })

  it('hides the purge action from a caller who is not an admin', async () => {
    who.roles = ['deployer']
    show('/access-points/ide-team')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    expect(within(panel).queryByRole('button', { name: 'Purge cache' })).toBeNull()
    expect(api.purgeAccessPointCache).not.toHaveBeenCalled()
  })

  it('asks for confirmation before purging; no call is made until the admin confirms', async () => {
    show('/access-points/ide-team')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    fireEvent.click(within(panel).getByRole('button', { name: 'Purge cache' }))
    expect(api.purgeAccessPointCache).toHaveBeenCalledTimes(0)
    expect(within(panel).getByRole('group', { name: 'Confirm purge' })).toHaveTextContent(/Remove the cached answers of ide-team/)
    fireEvent.click(within(panel).getByRole('button', { name: 'Cancel' }))
    expect(api.purgeAccessPointCache).toHaveBeenCalledTimes(0)
    fireEvent.click(within(panel).getByRole('button', { name: 'Purge cache' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm purge' }))
    expect(await within(panel).findByText('Removed 7 cached answers of access point ide-team (this API process).')).toBeInTheDocument()
    expect(api.purgeAccessPointCache).toHaveBeenCalledTimes(1)
    expect(api.purgeAccessPointCache).toHaveBeenCalledWith('ide-team')
  })

  it('reports a purge result the API did not count as unknown', async () => {
    vi.mocked(api.purgeAccessPointCache).mockResolvedValue({ access_point_id: 'ide-team', model: '', process_local: true } as unknown as api.AccessPointCachePurge)
    show('/access-points/ide-team')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    fireEvent.click(within(panel).getByRole('button', { name: 'Purge cache' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm purge' }))
    expect(await within(panel).findByText(/did not say how many cached answers were removed/)).toBeInTheDocument()
  })

  it('shows a purge failure with the server message', async () => {
    vi.mocked(api.purgeAccessPointCache).mockRejectedValue(apiError('cache store busy', 500))
    show('/access-points/ide-team')
    const panel = await panelOf()
    await within(panel).findByRole('table', { name: 'Backends of gemini-pro' })
    fireEvent.click(within(panel).getByRole('button', { name: 'Purge cache' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm purge' }))
    expect(await within(panel).findByRole('alert')).toHaveTextContent('Could not purge the cache: cache store busy')
  })
})

