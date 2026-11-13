import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import InstanceDetail from '../pages/InstanceDetail'
import { getInstance, getInstanceHistory, undeployInstance } from '../api/client'

vi.mock('../api/client', () => ({ getInstance: vi.fn(), getInstanceHistory: vi.fn(), undeployInstance: vi.fn() }))
vi.mock('../components/Toast', () => ({ useToast: () => ({ success: vi.fn(), error: vi.fn() }) }))

const base = { id: 'inst-1', plane: 'mcplex', template_id: 'tpl', owner: 'o', namespace: 'ns', status: 'running', deployed_by: 'me', deployed_at: '2026-01-02T03:04:05Z', updated_at: '2026-01-02T03:04:05Z', scopes: ['mcp:tool:x'], replicas: 2 }

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter initialEntries={['/instances/inst-1']}><QueryClientProvider client={client}><Routes><Route path="/instances/:id" element={<InstanceDetail />} /></Routes></QueryClientProvider></MemoryRouter>)
}
const row = (label: string) => screen.getByText(label).parentElement as HTMLElement

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getInstanceHistory).mockResolvedValue([])
  vi.mocked(undeployInstance).mockResolvedValue(undefined as never)
})

describe('InstanceDetail: unknown is never zero', () => {
  it('shows unknown for replicas, dates, scopes and health numbers the server did not send', async () => {
    vi.mocked(getInstance).mockResolvedValue({ ...base, replicas: null, deployed_at: undefined, updated_at: null, scopes: null, health: { status: 'ok', last_check: undefined, latency_ms: null } } as never)
    show()
    await screen.findByRole('heading', { name: 'inst-1' })
    expect(within(row('Replicas')).getByText('unknown')).toBeInTheDocument()
    expect(within(row('Deployed At')).getByText('unknown')).toBeInTheDocument()
    expect(within(row('Updated At')).getByText('unknown')).toBeInTheDocument()
    expect(within(row('Latency')).getByText('unknown')).toBeInTheDocument()
    expect(within(row('Last Check')).getByText('unknown')).toBeInTheDocument()
    expect(screen.getByText('Scopes not reported.')).toBeInTheDocument()
    expect(screen.queryByText('No scopes registered.')).not.toBeInTheDocument()
    expect(screen.queryByText(/Invalid Date/)).not.toBeInTheDocument()
  })

  it('shows a real zero replica count, zero latency and an empty scope list as such', async () => {
    vi.mocked(getInstance).mockResolvedValue({ ...base, replicas: 0, scopes: [], health: { status: 'ok', last_check: '2026-01-02T03:04:05Z', latency_ms: 0 } } as never)
    show()
    await screen.findByRole('heading', { name: 'inst-1' })
    expect(within(row('Replicas')).getByText('0')).toBeInTheDocument()
    expect(within(row('Latency')).getByText('0ms')).toBeInTheDocument()
    expect(screen.getByText('No scopes registered.')).toBeInTheDocument()
    expect(screen.queryByText('Scopes not reported.')).not.toBeInTheDocument()
  })
})

describe('InstanceDetail: undeploy asks first', () => {
  beforeEach(() => { vi.mocked(getInstance).mockResolvedValue(base as never) })

  it('does not undeploy on the first click, and does after confirmation', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Undeploy' }))
    expect(undeployInstance).not.toHaveBeenCalled()
    expect(screen.getByText(/Undeploy inst-1\?/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm undeploy' }))
    await waitFor(() => expect(undeployInstance).toHaveBeenCalledWith('inst-1'))
  })

  it('cancel leaves the instance deployed', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Undeploy' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(undeployInstance).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Undeploy' })).toBeInTheDocument()
  })
})

describe('InstanceDetail: history', () => {
  it('shows unknown for a missing timestamp or details, and keeps real values', async () => {
    vi.mocked(getInstance).mockResolvedValue(base as never)
    vi.mocked(getInstanceHistory).mockResolvedValue([
      { action: 'deploy', actor: 'alice', details: 'first' },
      { action: 'scale', actor: 'bob', timestamp: '2026-01-02T03:04:05Z', details: '0 replicas' },
      { action: 'noop', actor: 'carol', timestamp: '2026-01-02T03:04:05Z' },
    ] as never)
    show()
    const rowOf = async (action: string) => (await screen.findByText(action)).closest('tr') as HTMLElement
    const missing = await rowOf('deploy')
    expect(missing.textContent).not.toMatch(/Invalid Date/)
    expect(within(missing).getAllByText('unknown')).toHaveLength(1)
    expect(missing.textContent).toContain('first')
    const full = await rowOf('scale')
    expect(within(full).queryByText('unknown')).not.toBeInTheDocument()
    expect(full.textContent).toContain('0 replicas')
    expect(within(await rowOf('noop')).getAllByText('unknown')).toHaveLength(1)
  })
})
