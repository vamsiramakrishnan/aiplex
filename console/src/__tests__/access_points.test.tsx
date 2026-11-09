import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccessPoints from '../pages/AccessPoints'
import * as api from '../api/accessPoints'
import * as apUsage from '../api/accessPointUsage'
import { endpointGroups, fieldForError } from '../api/accessPoints'
import { listInstances } from '../api/client'
import * as overview from '../api/overview'

vi.mock('../api/client', async (original) => ({ ...(await original<typeof import('../api/client')>()), listInstances: vi.fn(), listAgents: vi.fn() }))
vi.mock('../api/accessPoints', async (original) => ({
  ...(await original<typeof import('../api/accessPoints')>()),
  listAccessPoints: vi.fn(), getAccessPoint: vi.fn(), putAccessPoint: vi.fn(), deleteAccessPoint: vi.fn(), getQuotaPlan: vi.fn(),
  listModelSurfaces: vi.fn(), listClientRegistrations: vi.fn(), listToolConnections: vi.fn(), listAgents: vi.fn(),
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
const apiError = (message: string, status: number, code?: string) => Object.assign(new Error(message), { status, code })
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
  vi.mocked(api.putAccessPoint).mockImplementation(async (s, revision) => ({ id: s.id, revision: revision + 1, spec: s }))
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true })
})

describe('access point list', () => {
  it('shows name, caller kinds, resource counts, QoS class and base URL', async () => {
    show('/access-points')
    const card = await screen.findByRole('article', { name: 'Access point IDE team' })
    expect(within(card).getByText('user, client')).toBeInTheDocument()
    expect(within(card).getByText('interactive')).toBeInTheDocument()
    expect(within(card).getByText(`${window.location.origin}/ap/ide-team`)).toBeInTheDocument()
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
    vi.mocked(api.putAccessPoint).mockRejectedValue(apiError('model surface "ghost" does not resolve in this tenant', 400, 'INVALID_SPEC'))
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
  it('shows endpoint URLs only for protocols a configured model speaks, with copy buttons', async () => {
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    const root = `${window.location.origin}/ap/ide-team`
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

  it('shows every model protocol with a note when surface protocols cannot be determined', async () => {
    vi.mocked(api.listModelSurfaces).mockRejectedValue(apiError('boom', 500))
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    await waitFor(() => expect(within(endpoints).getByRole('group', { name: 'Anthropic Messages' })).toBeInTheDocument())
    expect(within(endpoints).getByRole('group', { name: 'OpenAI Chat Completions' })).toHaveTextContent(/Could not determine/)
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
    expect(cost).toHaveTextContent(/1 records have unknown cost/)
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
  it('maps server messages to the field they name', () => {
    expect(fieldForError('model surface "x" does not resolve in this tenant')).toBe('models')
    expect(fieldForError('tools[search].expose: bad pattern')).toBe('tools')
    expect(fieldForError('qos.class must match [a-z]')).toBe('qos.class')
    expect(fieldForError('qos.budget_refs "b" is not a budget')).toBe('qos.budget_refs')
    expect(fieldForError('client registration "c" does not resolve in this tenant')).toBe('subjects.client_registrations')
    expect(fieldForError('an access point exposes at least one model, tool connection or skill')).toBe('resources')
    expect(fieldForError('boom')).toBeUndefined()
  })

  it('derives protocols from surfaces', () => {
    const groups = endpointGroups(spec, 'https://a.example/', surfaces)
    expect(groups.map(g => g.id)).toEqual(['google-genai', 'models', 'mcp', 'skills'])
    expect(groups[0].urls[0].url).toBe('https://a.example/ap/ide-team/v1beta/models/{model}:{op}')
  })
})

describe('surface picker with non-Google providers', () => {
  const bedrock: api.ModelSurfaceRecord = { id: 'bedrock-us', revision: 1, spec: { surface: { id: 'bedrock-us', protocol: 'bedrock-converse', models: ['claude-x'], provider: { id: 'bedrock' } } } }
  const openai: api.ModelSurfaceRecord = { id: 'oai', revision: 1, spec: { surface: { id: 'oai', protocol: 'openai-chat', models: ['gpt-5'], provider: { id: 'openai' } } } }

  it('shows provider and protocol and disables surfaces access points cannot route, with the reason', async () => {
    vi.mocked(api.listModelSurfaces).mockResolvedValue([...surfaces, openai, bedrock])
    show('/access-points/new')
    fireEvent.click(await screen.findByRole('button', { name: 'Add model' }))
    const surface = await screen.findByLabelText('Model surface')
    await waitFor(() => expect(within(surface).getAllByRole('option').length).toBe(5))
    const options = within(surface).getAllByRole('option') as HTMLOptionElement[]
    const byText = (t: RegExp) => options.find(o => t.test(o.textContent ?? ''))!
    expect(byText(/^oai/).textContent).toBe('oai · openai · openai-chat')
    expect(byText(/^oai/).disabled).toBe(false)
    expect(byText(/^bedrock-us/).textContent).toBe('bedrock-us · bedrock · bedrock-converse (unavailable: access points do not route bedrock-converse yet)')
    expect(byText(/^bedrock-us/).disabled).toBe(true)
  })

  it('blocks saving and explains when an existing model uses an unroutable surface', async () => {
    vi.mocked(api.listModelSurfaces).mockResolvedValue([bedrock])
    vi.mocked(api.getAccessPoint).mockResolvedValue({ ...record, spec: { ...spec, models: [{ name: 'c', surface_ref: 'bedrock-us', native_model: 'c' }] } })
    show('/access-points/ide-team/edit')
    expect(await screen.findByText(/Surface bedrock-us is unavailable for access points: access points do not route bedrock-converse yet/)).toBeInTheDocument()
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

  it('falls back to browser-derived URLs with a note naming why', async () => {
    show('/access-points/ide-team')
    const endpoints = await screen.findByRole('region', { name: 'Endpoints' })
    const note = await within(endpoints).findByRole('note')
    expect(note).toHaveTextContent(/derived from this console's address/)
    expect(note).toHaveTextContent(/no public URL configured/)
    expect(within(endpoints).getByText(`${window.location.origin}/ap/ide-team/mcp`)).toBeInTheDocument()
  })

  it('launches the connect wizard from the detail view', async () => {
    vi.mocked(overview.listClientProfiles).mockResolvedValue([])
    show('/access-points/ide-team')
    fireEvent.click(await screen.findByRole('button', { name: 'Connect a client' }))
    expect(await screen.findByRole('dialog', { name: 'Connect a client to ide-team' })).toBeInTheDocument()
  })
})
