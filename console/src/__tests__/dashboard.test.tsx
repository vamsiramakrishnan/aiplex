import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Dashboard from '../pages/Dashboard'
import { authenticatedRequest } from '../api/client'
import { usageSummary } from '../api/usage'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn() }))
vi.mock('../hooks/useSSE', () => ({ useSSE: () => null }))
vi.mock('../api/usage', async (original) => ({ ...(await original<typeof import('../api/usage')>()), usageSummary: vi.fn() }))

const zeroStats = { total_instances: 0, running_instances: 0, registered_agents: 0, active_planes: 0, mcplex_instances: 0, a2aplex_instances: 0, llmplex_instances: 0, skillsplex_instances: 0, daily_cost_usd: null, daily_tokens: null, daily_requests: null, tool_calls_24h: null, a2a_delegations_24h: 0, policy_denials_24h: 0 }

function route(stats: () => Promise<unknown>, denials: () => Promise<unknown>) {
  vi.mocked(authenticatedRequest).mockImplementation((path: string) => (path.endsWith('/stats') ? stats() : denials()) as never)
}
function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><Dashboard /></QueryClientProvider>)
  return client
}
const card = (label: string) => screen.getByText(label).parentElement as HTMLElement

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(usageSummary).mockResolvedValue({ groups: [] } as never)
})

describe('Dashboard: unknown is never zero', () => {
  it('shows an error with a retry when the stats fetch fails, and unknown instead of 0', async () => {
    let calls = 0
    route(() => (++calls === 1 ? Promise.reject(new Error('stats backend down')) : Promise.resolve({ ...zeroStats, total_instances: 4 })), () => Promise.resolve([]))
    show()
    const alert = await screen.findByText(/Could not load dashboard stats: stats backend down/)
    expect(alert).toBeInTheDocument()
    expect(within(card('Total Instances')).getByText('unknown')).toBeInTheDocument()
    expect(within(card('Registered Agents')).queryByText('0')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(within(card('Total Instances')).getByText('4')).toBeInTheDocument())
    expect(screen.queryByText(/Could not load dashboard stats/)).not.toBeInTheDocument()
  })

  it('shows unknown for a field the server did not send', async () => {
    route(() => Promise.resolve({ total_instances: 3 }), () => Promise.resolve([]))
    show()
    await waitFor(() => expect(within(card('Total Instances')).getByText('3')).toBeInTheDocument())
    expect(within(card('Running')).getByText('unknown')).toBeInTheDocument()
    expect(within(card('Active Planes')).getByText('unknown')).toBeInTheDocument()
  })

  it('shows unknown for a null field', async () => {
    route(() => Promise.resolve({ ...zeroStats, registered_agents: null }), () => Promise.resolve([]))
    show()
    await waitFor(() => expect(within(card('Total Instances')).getByText('0')).toBeInTheDocument())
    expect(within(card('Registered Agents')).getByText('unknown')).toBeInTheDocument()
  })

  it('still shows a real zero as 0', async () => {
    route(() => Promise.resolve(zeroStats), () => Promise.resolve([]))
    show()
    await waitFor(() => expect(within(card('Total Instances')).getByText('0')).toBeInTheDocument())
    expect(within(card('Running')).getByText('0')).toBeInTheDocument()
    expect(within(card('Registered Agents')).getByText('0')).toBeInTheDocument()
    expect(within(card('Active Planes')).getByText('0')).toBeInTheDocument()
    expect(screen.queryByText('unknown')).not.toBeInTheDocument()
    expect(await screen.findByText('No policy denials recorded.')).toBeInTheDocument()
  })

  it('shows an error, not the empty-state text, when the denials fetch fails', async () => {
    route(() => Promise.resolve(zeroStats), () => Promise.reject(new Error('denials unavailable')))
    show()
    expect(await screen.findByText(/Could not load policy denials: denials unavailable/)).toBeInTheDocument()
    expect(screen.queryByText('No policy denials recorded.')).not.toBeInTheDocument()
  })

  it('shows unknown requests and spend when the usage ledger fails', async () => {
    vi.mocked(usageSummary).mockRejectedValue(new Error('ledger down'))
    route(() => Promise.resolve({ ...zeroStats, policy_denials_24h: null }), () => Promise.resolve([]))
    show()
    await screen.findByText(/usage ledger unavailable/)
    expect(within(card('Requests')).getByText('unknown')).toBeInTheDocument()
    expect(within(card('Denied requests')).getByText('unknown')).toBeInTheDocument()
  })

  it('shows a real zero for requests when the ledger loaded and holds no records', async () => {
    route(() => Promise.resolve(zeroStats), () => Promise.resolve([]))
    show()
    await waitFor(() => expect(within(card('Requests')).getByText('0')).toBeInTheDocument())
    expect(within(card('Denied requests')).getByText('0')).toBeInTheDocument()
  })
})

describe('Dashboard: a failed refresh after a good load', () => {
  it('keeps the last figures, labels them with the time of the last good refresh, and does not call them unknown', async () => {
    let fail = false
    route(() => (fail ? Promise.reject(new Error('refresh failed')) : Promise.resolve({ ...zeroStats, total_instances: 4 })), () => Promise.resolve([]))
    const client = show()
    await waitFor(() => expect(within(card('Total Instances')).getByText('4')).toBeInTheDocument())
    fail = true
    await client.invalidateQueries({ queryKey: ['dashboard-stats'] })
    const alert = await screen.findByText(/The latest refresh of dashboard stats failed \(refresh failed\)\. The figures shown are from the last successful refresh at /)
    expect(alert.textContent).not.toMatch(/shown as unknown/)
    expect(within(card('Total Instances')).getByText('4')).toBeInTheDocument()
    expect(within(card('Total Instances')).queryByText('unknown')).not.toBeInTheDocument()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.queryByText(/latest refresh/)).not.toBeInTheDocument())
    expect(within(card('Total Instances')).getByText('4')).toBeInTheDocument()
  })

  it('does the same for the denials panel', async () => {
    let fail = false
    const denial = { id: 'd1', timestamp: '2026-01-01T00:00:00Z', plane: 'mcplex', agent_id: 'agent-x', action: 'call', scope: 's', reason: 'no scope' }
    route(() => Promise.resolve(zeroStats), () => (fail ? Promise.reject(new Error('denials broke')) : Promise.resolve([denial])))
    const client = show()
    await screen.findByText('agent-x')
    fail = true
    await client.invalidateQueries({ queryKey: ['dashboard-denials'] })
    expect(await screen.findByText(/The latest refresh of policy denials failed \(denials broke\)/)).toBeInTheDocument()
    expect(screen.getByText('agent-x')).toBeInTheDocument()
    expect(screen.queryByText('No policy denials recorded.')).not.toBeInTheDocument()
  })

  it('does the same for the usage ledger figures', async () => {
    let fail = false
    const group = { key: {}, requests: '7', errors: '0', denied: '0', unknown_usage_records: '0', unknown_cost_records: '0', cost: [], tokens: {}, tokens_unknown: {} }
    vi.mocked(usageSummary).mockImplementation((() => (fail ? Promise.reject(new Error('ledger broke')) : Promise.resolve({ groups: [group] }))) as never)
    route(() => Promise.resolve(zeroStats), () => Promise.resolve([]))
    const client = show()
    await waitFor(() => expect(within(card('Requests')).getByText('7')).toBeInTheDocument())
    fail = true
    await client.invalidateQueries({ queryKey: ['dashboard-ledger'] })
    expect(await screen.findByText(/The latest refresh of usage ledger figures failed \(ledger broke\)/)).toBeInTheDocument()
    expect(within(card('Requests')).getByText('7')).toBeInTheDocument()
  })
})

describe('Dashboard: every rendered figure', () => {
  const planes = ['MCPlex', 'A2APlex', 'LLMPlex', 'SkillsPlex']
  const plane = (label: string) => screen.getByText(label).parentElement as HTMLElement

  it('shows unknown for each plane row, A2A delegations and spend/tokens when nothing was loaded', async () => {
    route(() => Promise.reject(new Error('down')), () => Promise.resolve([]))
    vi.mocked(usageSummary).mockRejectedValue(new Error('ledger down'))
    show()
    await screen.findByText(/Could not load dashboard stats/)
    for (const p of planes) expect(within(plane(p)).getByText('unknown')).toBeInTheDocument()
    expect(within(card('A2A Delegations')).getByText('unknown')).toBeInTheDocument()
    await screen.findByText(/usage ledger unavailable/)
    expect(within(card('Total Spend')).getByText(/unknown/)).toBeInTheDocument()
    expect(within(card('Tokens')).getByText('unknown')).toBeInTheDocument()
  })

  it('shows 0 for each plane row, A2A delegations, spend and tokens when the server reports real zeros and an empty ledger', async () => {
    route(() => Promise.resolve(zeroStats), () => Promise.resolve([]))
    show()
    await waitFor(() => expect(within(plane('MCPlex')).getByText('0')).toBeInTheDocument())
    for (const p of planes) expect(within(plane(p)).getByText('0')).toBeInTheDocument()
    expect(within(card('A2A Delegations')).getByText('0')).toBeInTheDocument()
    await waitFor(() => expect(within(card('Total Spend')).getByText('0')).toBeInTheDocument())
    expect(within(card('Tokens')).getByText('0')).toBeInTheDocument()
  })

  it('shows unknown plane rows when the server omits them', async () => {
    route(() => Promise.resolve({ mcplex_instances: 2 }), () => Promise.resolve([]))
    show()
    await waitFor(() => expect(within(plane('MCPlex')).getByText('2')).toBeInTheDocument())
    for (const p of ['A2APlex', 'LLMPlex', 'SkillsPlex']) expect(within(plane(p)).getByText('unknown')).toBeInTheDocument()
  })

  it('shows unknown for a denial without a timestamp', async () => {
    route(() => Promise.resolve(zeroStats), () => Promise.resolve([{ id: 'd1', plane: 'mcplex', agent_id: 'agent-x', action: 'call', scope: 's', reason: 'r' }]))
    show()
    expect((await screen.findByText('agent-x')).closest('tr')!.textContent).toContain('unknown')
  })
})
