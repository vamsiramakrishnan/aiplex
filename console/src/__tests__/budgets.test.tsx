import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Budgets, { QUOTAPLEX_OFF } from '../pages/Budgets'
import { accessFrom } from '../hooks/useAccess'
import * as api from '../api/budgets'
import { authenticatedRequest } from '../api/client'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), listInstances: vi.fn().mockResolvedValue([]) }))
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: null }), useOptionalAuth: () => null }))
let access = accessFrom(['admin'])
vi.mock('../hooks/useAccess', async (orig) => ({ ...(await orig<typeof import('../hooks/useAccess')>()), useAccess: () => access }))
vi.mock('../api/budgets', async (orig) => {
  const real = await orig<typeof import('../api/budgets')>()
  return { ...real, listBudgets: vi.fn(), getBudgetStatus: vi.fn(), putBudget: vi.fn(), deleteBudget: vi.fn(), previewBudgets: vi.fn() }
})

const err = (status: number, code: string, message: string, details?: unknown) => Object.assign(new Error(message), { status, code, details })
const team: api.BudgetRecord = {
  id: 'team-a', revision: 3, spec: {
    id: 'team-a', display_name: 'Team A', limit: { unit: 'cost', currency: 'USD', amount: '250.00' },
    window: { kind: 'calendar', unit: 'month', time_zone: 'Europe/Berlin' }, members: { principals: ['alice@example.com'], groups: [{ source: 'idp', id: 'eng' }] },
    on_unknown_cost: 'allow_and_flag', include_tool_calls: true, thresholds: [50, 90],
  },
}
const tokens: api.BudgetRecord = { id: 'tok', revision: 1, spec: { id: 'tok', limit: { unit: 'tokens', amount: '1000' }, window: { kind: 'tumbling', hours: 24 }, members: { projects: ['p1'] } } }
const status = (over: Partial<api.BudgetStatus> = {}): api.BudgetStatus => ({
  id: 'team-a', unit: 'cost', currency: 'USD', limit: '250.00', spent: '12.50', reserved: '1.00', remaining: '236.50',
  window: { start: '2026-11-01T00:00:00Z', end: '2026-12-01T00:00:00Z' }, unknown_cost_requests: 0, thresholds: [50, 90], ...over,
})

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><Budgets /></QueryClientProvider>)
}
const row = (id: string) => screen.getByLabelText(`Edit ${id}`).closest('tr')!
beforeEach(() => {
  access = accessFrom(['admin'])
  vi.resetAllMocks()
  vi.mocked(api.listBudgets).mockResolvedValue([team])
  vi.mocked(api.getBudgetStatus).mockResolvedValue(status())
})

describe('Budgets tab', () => {
  it('lists budgets with limit, window, members, used, remaining, thresholds reached and revision', async () => {
    vi.mocked(api.getBudgetStatus).mockResolvedValue(status({ threshold_events: [{ percent: 50 }] }))
    show()
    expect(screen.getByText('Loading budgets…')).toBeInTheDocument()
    await screen.findByText('Team A')
    const r = row('team-a')
    await within(r).findByText('12.50 USD')
    for (const t of ['250.00 USD', 'month, Europe/Berlin', '1 group, 1 principal', '236.50 USD', '50%', '3']) expect(within(r).getByText(t)).toBeInTheDocument()
    expect(within(r).getByText('includes tool calls')).toBeInTheDocument()
  })

  it('shows an empty list as empty', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([])
    show()
    expect(await screen.findByText(/No budgets/)).toBeInTheDocument()
  })

  it('shows QuotaPlex not enabled as exactly that, not as an empty list or a generic error', async () => {
    vi.mocked(api.listBudgets).mockRejectedValue(err(409, 'QUOTAPLEX_REQUIRED', 'budgets are enforced by QuotaPlex'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent(QUOTAPLEX_OFF)
    expect(QUOTAPLEX_OFF).toMatch(/^QuotaPlex is not enabled on this server/)
    expect(screen.queryByText(/No budgets/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Could not load/)).not.toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByText('Add budget')).toBeDisabled()
  })

  it('shows a different 409 as an error, not as QuotaPlex being off', async () => {
    vi.mocked(api.listBudgets).mockRejectedValue(err(409, 'BUDGETS_INVALID', 'tree broken'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load budgets: tree broken')
    expect(screen.queryByText(QUOTAPLEX_OFF)).not.toBeInTheDocument()
  })

  it('shows a failed fetch as an error, never as an empty list', async () => {
    vi.mocked(api.listBudgets).mockRejectedValue(err(503, 'X', 'store down'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load budgets: store down. This is an error, not an empty list.')
    expect(screen.queryByText(/No budgets/)).not.toBeInTheDocument()
  })

  it('marks rows as possibly out of date after a failed refetch, and blocks writes on them', async () => {
    show()
    await screen.findByText('Team A')
    vi.mocked(api.listBudgets).mockRejectedValue(err(500, 'X', 'boom'))
    fireEvent.click(screen.getByText('Reload'))
    expect(await screen.findByText(/may be out of date/)).toBeInTheDocument()
    expect(screen.getByText('Team A')).toBeInTheDocument()
    expect(screen.getByLabelText('Edit team-a')).toBeDisabled()
    expect(screen.getByLabelText('Delete team-a')).toBeDisabled()
  })

  it('says a status refresh failed and when the figures are from', async () => {
    show()
    await screen.findByText('12.50 USD')
    vi.mocked(api.getBudgetStatus).mockRejectedValue(err(500, 'X', 'counters down'))
    fireEvent.click(screen.getByText('Reload'))
    expect(await screen.findByText(/latest refresh of this status failed \(counters down\)\. Shown from the last successful refresh at /)).toBeInTheDocument()
    expect(screen.getByText('12.50 USD')).toBeInTheDocument()
  })

  it('shows status that never loaded as unknown, and QuotaPlex off per budget', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, tokens])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => { throw id === 'tok' ? err(409, 'QUOTAPLEX_REQUIRED', 'x') : err(503, 'X', 'counters down') })
    show()
    await screen.findByText('Team A')
    expect(await within(row('team-a')).findByText(/Status unavailable: counters down\. Used, remaining and thresholds are unknown\./)).toBeInTheDocument()
    expect(await within(row('tok')).findByText(/QuotaPlex is not enabled on this server/)).toBeInTheDocument()
    expect(within(row('team-a')).queryByText('0')).not.toBeInTheDocument()
  })

  it('shows values the API did not send as unknown, and a real zero as 0', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([{ id: 'a', spec: { id: 'a', limit: { unit: 'requests', amount: '10' }, window: { kind: 'tumbling' } } }, { ...team, id: 'z', spec: { ...team.spec, id: 'z' } }])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => (id === 'a'
      ? { id: 'a', unit: 'requests' } as api.BudgetStatus
      : status({ id: 'z', spent: '0', remaining: '250.00', unknown_cost_requests: undefined })))
    show()
    await screen.findByText('Team A')
    const a = row('a')
    await waitFor(() => expect(within(a).getAllByText('unknown').length).toBeGreaterThanOrEqual(4))
    expect(within(a).queryByText(/^0/)).not.toBeInTheDocument()
    const z = row('z')
    await within(z).findByText('0 USD')
    // a cost budget whose status carries no unknown-cost count says it is unknown, not 0
    expect(within(z).getByText(/Requests of unknown cost: unknown/)).toBeInTheDocument()
    expect(within(z).queryByText(/^0 requests of unknown cost/)).not.toBeInTheDocument()
  })

  it('says beside the used amount when requests of unknown cost are in the window, and that none is 0', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, { ...team, id: 'b', spec: { ...team.spec, id: 'b' } }])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => status({ id, unknown_cost_requests: id === 'b' ? 0 : 3 }))
    show()
    await screen.findAllByText('12.50 USD')
    expect(await within(row('team-a')).findByText(/3 requests of unknown cost in this window; their cost is not in this amount/)).toBeInTheDocument()
    expect(await within(row('b')).findByText('0 requests of unknown cost.')).toBeInTheDocument()
  })

  it('treats null lists like missing ones and an empty list as none', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([{ id: 'n', revision: 1, spec: { id: 'n', limit: { unit: 'requests', amount: '1' }, window: { kind: 'tumbling', hours: 1 }, members: { groups: null, principals: [], projects: null } } }])
    vi.mocked(api.getBudgetStatus).mockResolvedValue({ id: 'n', unit: 'requests', limit: '1', spent: '0', reserved: '0', remaining: '1', thresholds: null, threshold_events: null })
    show()
    await screen.findByLabelText('Edit n')
    expect(within(row('n')).getByText('none')).toBeInTheDocument()
    await waitFor(() => expect(within(row('n')).getByText('unknown')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('Edit n'))
    expect(screen.getByLabelText('Principals')).toHaveValue('')
  })

  it('shows details for the chosen budget only, keyed by id', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, tokens])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => (id === 'tok'
      ? { id: 'tok', unit: 'tokens', limit: '1000', spent: '5', reserved: '0', remaining: '995', notes: ['tok note'] } as api.BudgetStatus
      : status({ notes: ['team note'], children: [{ id: 'kid', unit: 'cost', currency: 'USD', limit: '10.00', spent: '1.00', reserved: '0', remaining: '9.00' }], top_spenders: [{ principal_id: 'alice', requests: 4, unknown_cost_records: 2 }] })))
    show()
    await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Show details of team-a'))
    expect(await screen.findByText('team note')).toBeInTheDocument()
    expect(screen.getByText(/kid: 1.00 USD used of 10.00 USD/)).toBeInTheDocument()
    expect(screen.getByText(/alice: cost unknown, 2 records of unknown cost, 4 requests/)).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Show details of tok'))
    expect(await screen.findByText('tok note')).toBeInTheDocument()
    expect(screen.queryByText('team note')).not.toBeInTheDocument()
    expect(screen.queryByText(/kid:/)).not.toBeInTheDocument()
  })
})

describe('writing budgets', () => {
  it('creates with revision 0 and sends only what the form states', async () => {
    vi.mocked(api.putBudget).mockResolvedValue(team)
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByText('Add budget'))
    fireEvent.change(screen.getByLabelText('Budget id'), { target: { value: 'new-one' } })
    fireEvent.change(screen.getByLabelText('Currency'), { target: { value: 'USD' } })
    fireEvent.change(screen.getByLabelText('Limit amount'), { target: { value: '100.00' } })
    fireEvent.change(screen.getByLabelText('Principals'), { target: { value: ' bob \n\ncarol ' } })
    fireEvent.click(screen.getByText('Add member group'))
    fireEvent.change(screen.getByLabelText('Member group source 1'), { target: { value: 'idp' } })
    fireEvent.change(screen.getByLabelText('Member group id 1'), { target: { value: 'ops' } })
    fireEvent.click(screen.getByLabelText(/Include tool calls/))
    fireEvent.change(screen.getByLabelText('Thresholds (percent), optional'), { target: { value: '25, 75' } })
    fireEvent.click(screen.getByText('Create budget'))
    await waitFor(() => expect(api.putBudget).toHaveBeenCalledTimes(1))
    const [spec, rev] = vi.mocked(api.putBudget).mock.calls[0]!
    expect(rev).toBe(0)
    expect(spec).toEqual({
      id: 'new-one', limit: { unit: 'cost', currency: 'USD', amount: '100.00' }, window: { kind: 'calendar', unit: 'month', time_zone: 'UTC' },
      members: { principals: ['bob', 'carol'], groups: [{ source: 'idp', id: 'ops' }] }, include_tool_calls: true, thresholds: [25, 75],
    })
  })

  it('does not send cost-only fields for a tokens budget', async () => {
    vi.mocked(api.putBudget).mockResolvedValue(tokens)
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByText('Add budget'))
    fireEvent.click(screen.getByLabelText(/Include tool calls/))
    fireEvent.change(screen.getByLabelText('Currency'), { target: { value: 'USD' } })
    fireEvent.change(screen.getByLabelText('Limit unit'), { target: { value: 'tokens' } })
    expect(screen.queryByLabelText(/Include tool calls/)).not.toBeInTheDocument()
    expect(screen.queryByLabelText('On unknown cost')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Budget id'), { target: { value: 't' } })
    fireEvent.change(screen.getByLabelText('Limit amount'), { target: { value: '5' } })
    fireEvent.click(screen.getByText('Create budget'))
    await waitFor(() => expect(api.putBudget).toHaveBeenCalled())
    const spec = vi.mocked(api.putBudget).mock.calls[0]![0]
    expect(spec.limit).toEqual({ unit: 'tokens', amount: '5' })
    expect('include_tool_calls' in spec).toBe(false)
    expect('on_unknown_cost' in spec).toBe(false)
  })

  it('will not save an incomplete form', async () => {
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByText('Add budget'))
    expect(screen.getByText('Create budget')).toBeDisabled()
    expect(screen.getByText('Enter an id.')).toBeInTheDocument()
  })

  it('reports an id that already exists on create', async () => {
    vi.mocked(api.putBudget).mockRejectedValue(err(409, 'REVISION_CONFLICT', 'resource revision changed'))
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByText('Add budget'))
    fireEvent.change(screen.getByLabelText('Budget id'), { target: { value: 'team-a' } })
    fireEvent.change(screen.getByLabelText('Currency'), { target: { value: 'USD' } })
    fireEvent.change(screen.getByLabelText('Limit amount'), { target: { value: '1' } })
    fireEvent.click(screen.getByText('Create budget'))
    const alerts = await screen.findAllByRole('alert')
    expect(alerts).toHaveLength(1)
    expect(alerts[0]).toHaveTextContent('A budget with this id already exists. Edit that one instead.')
    expect(document.body).not.toHaveTextContent('changed since this page read it')
    expect(screen.getByLabelText('Budget id')).toBeEnabled()
    expect(screen.getByLabelText('Budget id')).toHaveValue('team-a')
    expect(screen.getByLabelText('Limit amount')).toHaveValue('1')
    expect(screen.getByText('Create budget')).toBeEnabled()
  })

  it('shows a rejected specification with the field the server named', async () => {
    vi.mocked(api.putBudget).mockRejectedValue(err(400, 'INVALID_SPEC', 'bad amount', { field: 'limit/amount' }))
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not save: bad amount (field limit/amount)')
  })

  it('shows QuotaPlex not enabled when a save is refused for it', async () => {
    vi.mocked(api.putBudget).mockRejectedValue(err(409, 'QUOTAPLEX_REQUIRED', 'enable the quotaplex module first'))
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent(QUOTAPLEX_OFF)
  })

  it('edits with the revision read, and an edit that changes nothing preserves fields the form does not model', async () => {
    const withExtras: api.BudgetRecord = { ...team, spec: { ...team.spec, future_field: { a: [1, 2] }, limit: { ...team.spec.limit, rounding: 'up' }, window: { ...team.spec.window, anchor: 'x' }, members: { ...team.spec.members, tenants: ['t'] } } }
    vi.mocked(api.listBudgets).mockResolvedValue([withExtras])
    vi.mocked(api.putBudget).mockResolvedValue({ ...withExtras, revision: 4 })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    expect(screen.getByLabelText('Budget id')).toBeDisabled()
    fireEvent.change(screen.getByLabelText('Display name, optional'), { target: { value: 'Team Alpha' } })
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.putBudget).toHaveBeenCalledTimes(1))
    const [spec, rev] = vi.mocked(api.putBudget).mock.calls[0]!
    expect(rev).toBe(3)
    expect(spec).toEqual({ ...withExtras.spec, display_name: 'Team Alpha' })
  })

  it('removes a field the user cleared, but keeps the rest', async () => {
    vi.mocked(api.putBudget).mockResolvedValue(team)
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.change(screen.getByLabelText('Display name, optional'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Principals'), { target: { value: '' } })
    fireEvent.click(screen.getByLabelText(/Include tool calls/))
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.putBudget).toHaveBeenCalled())
    const spec = vi.mocked(api.putBudget).mock.calls[0]![0]
    expect('display_name' in spec).toBe(false)
    expect('include_tool_calls' in spec).toBe(false)
    expect(spec.members).toEqual({ groups: [{ source: 'idp', id: 'eng' }] })
    expect(spec.thresholds).toEqual([50, 90])
  })

  it('shows an edit conflict with a way to reload, and locks the form', async () => {
    vi.mocked(api.putBudget).mockRejectedValue(err(409, 'REVISION_CONFLICT', 'resource revision changed'))
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Save changes'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('changed since this page read it. Nothing was saved')
    expect(screen.getByText('Save changes')).toBeDisabled()
    vi.mocked(api.listBudgets).mockResolvedValue([{ ...team, revision: 4 }])
    fireEvent.click(screen.getByText('Reload latest version'))
    await waitFor(() => expect(screen.queryByText('Save changes')).not.toBeInTheDocument())
    await waitFor(() => expect(within(row('team-a')).getByText('4')).toBeInTheDocument())
  })

  it('reports saving an edit of a budget that is gone', async () => {
    vi.mocked(api.putBudget).mockRejectedValue(err(404, 'NOT_FOUND', 'budget not found'))
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer exists. Nothing was saved.')
  })

  it('does not carry form state from one budget to the next', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, tokens])
    vi.mocked(api.previewBudgets).mockResolvedValue({ draft: { valid: true }, draws: [], skipped: [], notes: [] })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.change(screen.getByLabelText('Display name, optional'), { target: { value: 'typed' } })
    fireEvent.click(screen.getByText('Preview'))
    expect(await screen.findByText('The draft is valid.')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Edit tok'))
    expect(screen.getByLabelText('Budget id')).toHaveValue('tok')
    expect(screen.getByLabelText('Display name, optional')).toHaveValue('')
    expect(screen.getByLabelText('Limit unit')).toHaveValue('tokens')
    expect(screen.queryByText('The draft is valid.')).not.toBeInTheDocument()
    expect(screen.queryByLabelText(/Include tool calls/)).not.toBeInTheDocument()
  })

  it('asks before deleting, and sends the revision read', async () => {
    vi.mocked(api.deleteBudget).mockResolvedValue(undefined)
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Delete team-a'))
    expect(api.deleteBudget).not.toHaveBeenCalled()
    expect(screen.getByText(/Delete the budget/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Cancel'))
    expect(api.deleteBudget).not.toHaveBeenCalled()
    fireEvent.click(screen.getByLabelText('Delete team-a'))
    fireEvent.click(screen.getByText('Confirm delete'))
    await waitFor(() => expect(api.deleteBudget).toHaveBeenCalledWith('team-a', 3))
  })

  it('shows a delete conflict with a way to reload, and a refused delete with the server reason', async () => {
    vi.mocked(api.deleteBudget).mockRejectedValue(err(409, 'REVISION_CONFLICT', 'x'))
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Delete team-a'))
    fireEvent.click(screen.getByText('Confirm delete'))
    expect(await screen.findByRole('alert')).toHaveTextContent('changed since this page read it. Nothing was saved or deleted.')
    expect(screen.getByText('Reload latest version')).toBeInTheDocument()
    vi.mocked(api.deleteBudget).mockRejectedValue(err(409, 'BUDGET_HAS_CHILDREN', 'budget "team-a" is the parent of "kid"'))
    fireEvent.click(screen.getByText('Reload latest version'))
    await screen.findByLabelText('Delete team-a')
    fireEvent.click(screen.getByLabelText('Delete team-a'))
    fireEvent.click(screen.getByText('Confirm delete'))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/Could not delete: .*is the parent of "kid"/))
  })

  it('blocks editing and deleting a budget whose revision is unknown', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([{ id: 'r', spec: { id: 'r', limit: { unit: 'requests', amount: '1' }, window: { kind: 'tumbling', hours: 1 } } }])
    show()
    await screen.findByLabelText('Edit r')
    expect(within(row('r')).getByText('unknown', { selector: 'td' })).toBeInTheDocument()
    expect(screen.getByLabelText('Edit r')).toBeDisabled()
    expect(screen.getByLabelText('Delete r')).toBeDisabled()
  })
})

describe('preview', () => {
  it('sends the draft with the caller and shows draws, skipped budgets and notes', async () => {
    vi.mocked(api.previewBudgets).mockResolvedValue({
      hypothetical: true, plane: 'mcplex', access_point: 'ide', project: 'proj',
      draft: { valid: true },
      draws: [{ id: 'team-a', display_name: 'Team A', direct: true, reasons: ['principal'], unit: 'cost', currency: 'USD', limit: '250.00', window: team.spec.window, draft: true, include_tool_calls: true }],
      skipped: [{ id: 'old', reason: 'this cost budget counts model traffic only; set include_tool_calls to count tool calls' }],
      notes: ['The subject is hypothetical; nothing in it is verified.'],
    })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.change(screen.getByLabelText('Preview principal'), { target: { value: 'alice@example.com' } })
    fireEvent.change(screen.getByLabelText('Preview plane'), { target: { value: 'mcplex' } })
    fireEvent.change(screen.getByLabelText('Preview access point'), { target: { value: 'ide' } })
    fireEvent.click(screen.getByText('Add preview group'))
    fireEvent.change(screen.getByLabelText('Preview group source 1'), { target: { value: 'idp' } })
    fireEvent.change(screen.getByLabelText('Preview group id 1'), { target: { value: 'eng' } })
    fireEvent.click(screen.getByText('Preview'))
    const result = await screen.findByRole('region', { name: 'Preview result' })
    expect(result).toHaveTextContent('The draft is valid.')
    expect(result).toHaveTextContent('Team A (this draft): 250.00 USD')
    expect(result).toHaveTextContent('counts tool calls')
    expect(result).toHaveTextContent('old: this cost budget counts model traffic only')
    expect(result).toHaveTextContent('nothing in it is verified')
    const req = vi.mocked(api.previewBudgets).mock.calls[0]![0]
    expect(req).toMatchObject({ subject: { principal: 'alice@example.com', groups: [{ source: 'idp', id: 'eng' }] }, access_point: 'ide', plane: 'mcplex', draft: { id: 'team-a', include_tool_calls: true } })
  })

  it('shows an invalid draft with its field, and an empty draw list as nothing counted', async () => {
    vi.mocked(api.previewBudgets).mockResolvedValue({ draft: { valid: false, field: 'limit/amount', error: 'bad amount' }, draws: [], skipped: [], notes: [] })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Preview'))
    expect(await screen.findByText(/The draft is not valid \(field limit\/amount\): bad amount/)).toBeInTheDocument()
    expect(screen.getByText('No budget would count this request.')).toBeInTheDocument()
  })

  it('shows lists the server did not send as unknown, not as none', async () => {
    vi.mocked(api.previewBudgets).mockResolvedValue({ draft: { valid: true }, draws: null, skipped: null, notes: null })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Preview'))
    expect(await screen.findByText(/unknown: the server did not list the budgets/)).toBeInTheDocument()
    expect(screen.queryByText('No budget would count this request.')).not.toBeInTheDocument()
  })

  it('shows QuotaPlex not enabled, and marks a preview stale once the form changes', async () => {
    vi.mocked(api.previewBudgets).mockRejectedValueOnce(err(409, 'QUOTAPLEX_REQUIRED', 'x')).mockResolvedValue({ draft: { valid: true }, draws: [], skipped: [], notes: [] })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Edit team-a'))
    fireEvent.click(screen.getByText('Preview'))
    expect(await screen.findByText(QUOTAPLEX_OFF)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Preview'))
    await screen.findByText('The draft is valid.')
    fireEvent.change(screen.getByLabelText('Limit amount'), { target: { value: '999' } })
    expect(screen.getByText(/The form changed since this preview/)).toBeInTheDocument()
    expect(screen.queryByText('The draft is valid.')).not.toBeInTheDocument()
  })

  it('is not offered to non-admins', async () => {
    access = accessFrom(['deployer'])
    show(); await screen.findByText('Team A')
    expect(screen.queryByText('Preview')).not.toBeInTheDocument()
  })
})

describe('non-admins', () => {
  it('read budgets but see no write controls', async () => {
    access = accessFrom(['deployer'])
    show()
    await screen.findByText('Team A')
    await screen.findByText('12.50 USD')
    expect(screen.queryByText('Add budget')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Edit team-a')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Delete team-a')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Show details of team-a')).toBeInTheDocument()
    expect(screen.getByText(/You need the admin role for this/)).toBeInTheDocument()
  })
})

describe('budgets api', () => {
  const real = async () => await vi.importActual<typeof import('../api/budgets')>('../api/budgets')
  beforeEach(() => { vi.mocked(authenticatedRequest).mockResolvedValue({}) })

  it('URL-encodes the id in every path and sends the revision', async () => {
    const m = await real()
    await m.getBudgetStatus('a/b c')
    expect(authenticatedRequest).toHaveBeenLastCalledWith('/api/v1/budgets/a%2Fb%20c/status')
    await m.deleteBudget('a/b c', 7)
    expect(authenticatedRequest).toHaveBeenLastCalledWith('/api/v1/budgets/a%2Fb%20c', { method: 'DELETE', headers: { 'If-Match': '7' } })
    await m.putBudget({ id: 'x/y', limit: { unit: 'requests', amount: '1' }, window: { kind: 'tumbling', hours: 1 } }, 0)
    expect(vi.mocked(authenticatedRequest).mock.lastCall![0]).toBe('/api/v1/budgets/x%2Fy')
    expect(vi.mocked(authenticatedRequest).mock.lastCall![1]).toMatchObject({ method: 'PUT', headers: { 'If-Match': '0' } })
  })

  it('reads a null list as empty and refuses a malformed one', async () => {
    const m = await real()
    vi.mocked(authenticatedRequest).mockResolvedValueOnce(null)
    await expect(m.listBudgets()).resolves.toEqual([])
    vi.mocked(authenticatedRequest).mockResolvedValueOnce({ not: 'a list' })
    await expect(m.listBudgets()).rejects.toThrow('Invalid budget list response')
  })

  it('tells QuotaPlex being off from a revision conflict, both 409', async () => {
    const m = await real()
    expect(m.isQuotaplexRequired(err(409, 'QUOTAPLEX_REQUIRED', ''))).toBe(true)
    expect(m.isQuotaplexRequired(err(409, 'REVISION_CONFLICT', ''))).toBe(false)
    expect(m.isRevisionConflict(err(409, 'REVISION_CONFLICT', ''))).toBe(true)
    expect(m.isRevisionConflict(err(409, 'QUOTAPLEX_REQUIRED', ''))).toBe(false)
  })
})

const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }
const open = async () => { show(); await screen.findByText('Team A'); fireEvent.click(screen.getByLabelText('Edit team-a')) }

describe('review round 1', () => {
  it('1: an answer that arrives after the form changed is shown as out of date', async () => {
    const d = deferred<api.BudgetPreview>()
    vi.mocked(api.previewBudgets).mockReturnValue(d.promise)
    await open()
    fireEvent.click(screen.getByText('Preview'))
    fireEvent.change(screen.getByLabelText('Limit amount'), { target: { value: '999' } })
    d.resolve({ draft: { valid: true }, draws: [], skipped: [], notes: [] })
    expect(await screen.findByText(/The form changed since this preview/)).toBeInTheDocument()
    expect(screen.queryByText('The draft is valid.')).not.toBeInTheDocument()
  })

  it('1: with two previews in flight the older answer arriving last does not replace the newer', async () => {
    const first = deferred<api.BudgetPreview>(); const second = deferred<api.BudgetPreview>()
    vi.mocked(api.previewBudgets).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    await open()
    fireEvent.click(screen.getByText('Preview'))
    fireEvent.change(screen.getByLabelText('Limit amount'), { target: { value: '999' } })
    fireEvent.click(screen.getByText('Preview'))
    second.resolve({ draft: { valid: true }, draws: [], skipped: [], notes: ['newer answer'] })
    expect(await screen.findByText('newer answer')).toBeInTheDocument()
    first.resolve({ draft: { valid: true }, draws: [], skipped: [], notes: ['older answer'] })
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.getByText('newer answer')).toBeInTheDocument()
    expect(screen.queryByText('older answer')).not.toBeInTheDocument()
  })

  const withValue = (spec: Partial<api.BudgetSpec>, limit: Record<string, unknown> = {}): api.BudgetRecord => ({ ...team, spec: { ...team.spec, ...spec, limit: { ...team.spec.limit, ...limit } } as api.BudgetSpec })
  const saveOnlyName = async (rec: api.BudgetRecord, before: () => void = () => {}) => {
    vi.mocked(api.listBudgets).mockResolvedValue([rec])
    vi.mocked(api.putBudget).mockResolvedValue(rec)
    await open()
    before()
    fireEvent.change(screen.getByLabelText('Display name, optional'), { target: { value: 'Renamed' } })
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.putBudget).toHaveBeenCalledTimes(1))
    return vi.mocked(api.putBudget).mock.calls[0]![0]
  }

  it('3a: a window kind the form does not model is shown as stored and sent unchanged', async () => {
    const rec = withValue({ window: { kind: 'rolling', unit: 'hour', time_zone: 'UTC', span: 5 } })
    const spec = await saveOnlyName(rec, () => expect(screen.getByText(/"kind":"rolling"/)).toBeInTheDocument())
    expect(spec.window).toEqual({ kind: 'rolling', unit: 'hour', time_zone: 'UTC', span: 5 })
  })
  it('3b: an on_unknown_cost the form does not know is shown as stored and sent unchanged', async () => {
    const spec = await saveOnlyName(withValue({ on_unknown_cost: 'estimate' }), () => {
      expect(screen.getByLabelText('On unknown cost')).toHaveValue('estimate')
      expect(screen.getByText(/estimate \(not known to this Console\)/)).toBeInTheDocument()
    })
    expect(spec.on_unknown_cost).toBe('estimate')
  })
  it('3c: a limit unit the form does not know keeps its unit, currency and cost-only fields', async () => {
    const spec = await saveOnlyName(withValue({ include_tool_calls: true }, { unit: 'credits' }), () => expect(screen.getByText(/credits \(not known to this Console\)/)).toBeInTheDocument())
    expect(spec.limit).toEqual({ unit: 'credits', currency: 'USD', amount: '250.00' })
    expect(spec.include_tool_calls).toBe(true)
    expect(spec.on_unknown_cost).toBe('allow_and_flag')
  })
  it('3c: a calendar window unit the form does not know is kept', async () => {
    const spec = await saveOnlyName(withValue({ window: { kind: 'calendar', unit: 'quarter', time_zone: 'UTC' } }), () => expect(screen.getByText(/quarter \(not known to this Console\)/)).toBeInTheDocument())
    expect(spec.window).toEqual({ kind: 'calendar', unit: 'quarter', time_zone: 'UTC' })
  })
  it('3c: member entries of a kind the form does not model are kept', async () => {
    const members = { principals: [{ id: 'p', kind: 'user' }], agents: ['a1'], groups: [{ source: 'idp', id: 'eng', label: 'Engineering' }], client_registrations: 'oops' }
    const spec = await saveOnlyName(withValue({ members } as unknown as Partial<api.BudgetSpec>))
    expect(spec.members).toEqual(members)
  })
  it('3c: a group with fields of its own keeps them when the group list is edited', async () => {
    const members = { groups: [{ source: 'idp', id: 'eng', label: 'Engineering' }] }
    const spec = await saveOnlyName(withValue({ members }))
    expect(spec.members).toEqual(members)
  })
  it('3c: thresholds the form cannot read are kept', async () => {
    const spec = await saveOnlyName(withValue({ thresholds: ['half'] as unknown as number[] }))
    expect(spec.thresholds).toEqual(['half'])
  })

  const openDetail = async () => {
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Show details of team-a'))
    const region = await screen.findByRole('region', { name: 'Budget team-a' })
    await waitFor(() => expect(region).toHaveTextContent('Child budgets'))
    return region
  }
  const section = (region: HTMLElement, h: string) => within(region).getByText(h).parentElement!

  it('4: in the full view absent events and children are none, and absent top spenders are unknown', async () => {
    const region = await openDetail()
    expect(section(region, 'Threshold events')).toHaveTextContent('none')
    expect(section(region, 'Child budgets')).toHaveTextContent('none')
    expect(section(region, 'Top spenders')).toHaveTextContent('unknown')
  })
  it('4: children are unknown when a child counter is unavailable', async () => {
    vi.mocked(api.getBudgetStatus).mockResolvedValue(status({ notes: ['child "kid" counters are unavailable'] }))
    const region = await openDetail()
    expect(section(region, 'Child budgets')).toHaveTextContent('unknown: some child counters are unavailable')
  })
  it('4: a view without the operator block says unknown, not none', async () => {
    vi.mocked(api.getBudgetStatus).mockResolvedValue({ id: 'team-a', unit: 'cost', currency: 'USD', limit: '250.00', spent: '1', reserved: '0', remaining: '249' })
    const region = await openDetail()
    expect(section(region, 'Threshold events')).toHaveTextContent('unknown: this view does not include them')
    expect(section(region, 'Child budgets')).toHaveTextContent('unknown: this view does not include them')
  })

  it('5: a top spender is shown in the budget unit, with unknown distinct from 0', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, tokens, { id: 'req', revision: 1, spec: { id: 'req', limit: { unit: 'requests', amount: '9' }, window: { kind: 'tumbling', hours: 1 } } }])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => {
      if (id === 'tok') return { id, unit: 'tokens', limit: '1000', spent: '5', reserved: '0', remaining: '995', thresholds: [50], top_spenders: [{ principal_id: 'u1', requests: 2, tokens: { input_tokens: 100, output_tokens: 20, cache_read_tokens: 40, total_tokens: 120 } }, { principal_id: 'u2', requests: 1 }, { principal_id: 'u4', requests: 1, tokens: {} }] }
      if (id === 'req') return { id, unit: 'requests', limit: '9', spent: '2', reserved: '0', remaining: '7', thresholds: [50], top_spenders: [{ principal_id: 'u3', requests: 2 }] }
      return status({ top_spenders: [
        { principal_id: 'c1', requests: 1, cost: [{ currency: 'USD', basis: 'settled', amount: '3', amount_decimal: '' }], unknown_cost_records: 0 },
        { principal_id: 'c2', requests: 1, cost: [{ currency: 'USD', basis: 'settled', amount: '4', amount_decimal: '4.00' }] },
        { principal_id: 'c3', requests: 1, unknown_cost_records: 2 }] })
    })
    show(); await screen.findByText('Team A')
    fireEvent.click(screen.getByLabelText('Show details of team-a'))
    expect(await screen.findByText(/c1: 3 USD \(settled\), 0 records of unknown cost, 1 requests/)).toBeInTheDocument()
    expect(screen.getByText(/c2: 4.00 USD \(settled\), records of unknown cost: unknown/)).toBeInTheDocument()
    expect(screen.getByText(/c3: cost unknown, 2 records of unknown cost/)).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Show details of tok'))
    // The ledger's counters overlap (the total covers input and output; cache reads can be part of input): shown side by side, never summed.
    const u1 = await screen.findByText(/u1: tokens: total 120, input 100, output 20, cache read 40, 2 requests/)
    expect(u1).toBeInTheDocument()
    expect(u1.textContent).not.toMatch(/280|240|160/)
    expect(screen.getByText(/u2: tokens unknown, 1 requests/)).toBeInTheDocument()
    expect(screen.getByText(/u4: tokens unknown, 1 requests/)).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Show details of req'))
    expect(await screen.findByText(/u3: 2 requests/)).toBeInTheDocument()
  })

  it('7: one failing status leaves the others intact and shows no zeros for it', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, tokens])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => { if (id === 'tok') throw err(500, 'X', 'one down'); return status() })
    show(); await screen.findByText('Team A')
    await within(row('team-a')).findByText('12.50 USD')
    expect(await within(row('tok')).findByText(/Status unavailable: one down/)).toBeInTheDocument()
    expect(within(row('tok')).queryByText(/^0/)).not.toBeInTheDocument()
    expect(within(row('team-a')).getByText('236.50 USD')).toBeInTheDocument()
  })

  it('8: only 409 REVISION_CONFLICT is a revision conflict; the server never sends 412', async () => {
    const m = await vi.importActual<typeof import('../api/budgets')>('../api/budgets')
    expect(m.isRevisionConflict(err(412, 'REVISION_CONFLICT', ''))).toBe(false)
    expect(m.isRevisionConflict(err(412, 'X', ''))).toBe(false)
    expect(m.isRevisionConflict(err(409, 'REVISION_CONFLICT', ''))).toBe(true)
  })

  it('9: reserved absent is unknown', async () => {
    vi.mocked(api.getBudgetStatus).mockResolvedValue(status({ reserved: undefined }))
    const region = await openDetail()
    expect(region).toHaveTextContent('Reserved: unknown')
    expect(region).not.toHaveTextContent('Reserved: 0')
  })
  it('9: a real zero reserved is 0', async () => {
    vi.mocked(api.getBudgetStatus).mockResolvedValue(status({ reserved: '0' }))
    const region = await openDetail()
    expect(region).toHaveTextContent('Reserved: 0 USD')
  })
  it('9: thresholds reached: events, none, and absent', async () => {
    vi.mocked(api.listBudgets).mockResolvedValue([team, tokens, { ...tokens, id: 'bare', spec: { ...tokens.spec, id: 'bare' } }])
    vi.mocked(api.getBudgetStatus).mockImplementation(async (id) => (id === 'team-a' ? status({ threshold_events: [{ percent: 50 }, { percent: 90 }] })
      : id === 'tok' ? status({ id, unit: 'tokens', threshold_events: undefined, thresholds: [50] })
      : { id, unit: 'tokens', limit: '1', spent: '0', reserved: '0', remaining: '1' }))
    show(); await screen.findByText('Team A')
    await within(row('team-a')).findByText('50%, 90%')
    await waitFor(() => expect(within(row('tok')).getAllByText('none').length).toBeGreaterThan(0))
    await waitFor(() => expect(within(row('bare')).getAllByText('unknown').length).toBeGreaterThan(0))
  })
  it('9: an id with ? and # is encoded in the path', async () => {
    const m = await vi.importActual<typeof import('../api/budgets')>('../api/budgets')
    vi.mocked(authenticatedRequest).mockResolvedValue({})
    await m.getBudgetStatus('a?b#c')
    expect(authenticatedRequest).toHaveBeenLastCalledWith('/api/v1/budgets/a%3Fb%23c/status')
  })

  it('10: a preview draw shows its parent', async () => {
    vi.mocked(api.previewBudgets).mockResolvedValue({ draft: { valid: true }, draws: [{ id: 'kid', parent: 'team-a', direct: true, unit: 'requests', limit: '5' }], skipped: [], notes: [] })
    await open()
    fireEvent.click(screen.getByText('Preview'))
    expect(await screen.findByText(/under team-a/)).toBeInTheDocument()
  })
})
