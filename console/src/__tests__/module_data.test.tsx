import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'
import A2APlex from '../pages/A2APlex'
import MCPlex from '../pages/MCPlex'
import { authenticatedRequest, getCatalog, listInstances } from '../api/client'
vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), getCatalog: vi.fn(), listInstances: vi.fn() }))
vi.mock('../components/ToolConnectionsPanel', () => ({ default: () => null }))
it('distinguishes a failed deployed-agent request from an empty deployment list', async () => {
  vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
  vi.mocked(authenticatedRequest).mockRejectedValue(new Error('Native server unavailable'))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MemoryRouter><QueryClientProvider client={client}><A2APlex /></QueryClientProvider></MemoryRouter>)
  expect(await screen.findByRole('alert')).toHaveTextContent('Native server unavailable')
  expect(screen.queryByText('No A2A agents deployed yet.')).not.toBeInTheDocument()
})

const mount = (ui: React.ReactElement) => render(<MemoryRouter><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{ui}</QueryClientProvider></MemoryRouter>)
const inst = (id: string, scopes: string[] | null) => ({ id, display_name: id, template_id: 'tpl', status: 'running', scopes })

describe('MCPlex: unknown is never zero', () => {
  it('shows unknown tools for a null scope list, a real 0 tools for an empty one, and links each instance', async () => {
    vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
    vi.mocked(listInstances).mockResolvedValue([inst('no-field', null), inst('empty', []), inst('two', ['a', 'b'])] as never)
    mount(<MCPlex />)
    const rowOf = async (name: string) => (await screen.findByRole('link', { name })).closest('tr') as HTMLElement
    expect((await rowOf('no-field')).textContent).toContain('unknown tools')
    expect((await rowOf('empty')).textContent).toContain('0 tools')
    expect((await rowOf('two')).textContent).toContain('2 tools')
    expect(screen.getByRole('link', { name: 'two' })).toHaveAttribute('href', '/instances/two')
  })

  it('omits tab counts until the data has loaded, then shows real counts including 0', async () => {
    vi.mocked(getCatalog).mockReturnValue(new Promise(() => {}))
    vi.mocked(listInstances).mockReturnValue(new Promise(() => {}))
    const { unmount } = mount(<MCPlex />)
    expect(screen.getByRole('button', { name: 'Instances' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Catalog' })).toBeInTheDocument()
    unmount()
    vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
    vi.mocked(listInstances).mockResolvedValue([])
    mount(<MCPlex />)
    expect(await screen.findByRole('button', { name: 'Instances (0)' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Catalog (0)' })).toBeInTheDocument()
  })

  it('omits the tab counts when the requests fail', async () => {
    vi.mocked(getCatalog).mockRejectedValue(new Error('down'))
    vi.mocked(listInstances).mockRejectedValue(new Error('down'))
    mount(<MCPlex />)
    await screen.findByRole('alert')
    expect(screen.getByRole('button', { name: 'Instances' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /\(0\)/ })).not.toBeInTheDocument()
  })
})

describe('A2APlex: delegation duration', () => {
  it('shows a dash when the duration is absent and 0ms for a real zero', async () => {
    vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
    const d = (id: string, duration_ms?: number) => ({ id, caller_agent_id: 'caller-' + id, callee_agent_id: 'callee', task_type: 't', status: 'ok', user_id: 'u', started_at: '2026-01-01T00:00:00Z', duration_ms, ...(id === 'zero' ? { trace_id: 'abcdef123456' } : {}) })
    vi.mocked(authenticatedRequest).mockImplementation((path: string) => (path.endsWith('/delegations') ? Promise.resolve([d('absent'), d('zero', 0)]) : Promise.resolve([])) as never)
    mount(<A2APlex />)
    fireEvent.click(await screen.findByRole('button', { name: /Delegations/ }))
    const row = async (id: string) => (await screen.findByText('caller-' + id)).closest('tr') as HTMLElement
    expect((await row('absent')).textContent).not.toMatch(/\dms/)
    expect((await row('absent')).textContent).toContain('unknown')
    expect((await row('zero')).textContent).toContain('abcdef12')
    expect((await row('zero')).textContent).toContain('0ms')
  })
})

describe('A2APlex: tab counts', () => {
  it('omits counts while loading and after a failure', async () => {
    vi.mocked(getCatalog).mockRejectedValue(new Error('down'))
    vi.mocked(authenticatedRequest).mockRejectedValue(new Error('down'))
    mount(<A2APlex />)
    await screen.findByRole('alert')
    for (const name of ['Agents', 'Catalog', 'Delegations']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /\(0\)/ })).not.toBeInTheDocument()
  })

  it('shows real counts, including a real 0', async () => {
    vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
    vi.mocked(authenticatedRequest).mockImplementation((path: string) => (path.endsWith('/agents') ? Promise.resolve([{ instance_id: 'a', name: 'a', url: 'u', task_types: [], status: 'running' }]) : Promise.resolve([])) as never)
    mount(<A2APlex />)
    expect(await screen.findByRole('button', { name: 'Agents (1)' })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Delegations (0)' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Catalog (0)' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Delegations (0)' }))
    expect(await screen.findByText('No delegations recorded.')).toBeInTheDocument()
  })
})
