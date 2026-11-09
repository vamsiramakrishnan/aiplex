import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { enabledConsoleModules } from '../modules'
import { ModuleProvider, RequireModule } from '../hooks/useModules'
import { listModules, getCatalog } from '../api/client'
import Deploy from '../pages/Deploy'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/client', () => ({ listModules: vi.fn(), getCatalog: vi.fn(), deployInstance: vi.fn(), applyManifest: vi.fn() }))
let session = { token: 'first', isAuthenticated: true }
vi.mock('../hooks/useAuth', () => ({ useAuth: () => session }))

const llm = { name: 'llmplex', version: '1.0.0', contract_version: 'v1', navigation: [{ path: '/llmplex', title: 'LLMPlex' }] }

describe('Module discovery', () => {
  beforeEach(() => { session = { token: 'first', isAuthenticated: true }; vi.mocked(listModules).mockReset() })
  afterEach(() => { vi.clearAllMocks() })

  it('executes only locally registered components with supported contracts and matching paths', () => {
    expect(enabledConsoleModules([llm]).map((m) => m.name)).toEqual(['llmplex'])
    expect(enabledConsoleModules([{ ...llm, contract_version: 'v2' }])).toEqual([])
    expect(enabledConsoleModules([{ ...llm, navigation: [{ path: 'https://evil.invalid', title: 'LLMPlex' }] }])).toEqual([])
    expect(enabledConsoleModules([{ ...llm, name: 'untrusted-extension' }])).toEqual([])
  })

  it('blocks disabled routes and failed discovery', async () => {
    vi.mocked(listModules).mockResolvedValue([])
    const { unmount } = render(<ModuleProvider><RequireModule name="llmplex"><p>Protected module</p></RequireModule></ModuleProvider>)
    await screen.findByText('This module is not enabled on this Aiplex server.')
    expect(screen.queryByText('Protected module')).toBeNull()
    unmount()
    vi.mocked(listModules).mockRejectedValue(new Error('unavailable'))
    render(<ModuleProvider><RequireModule name="llmplex"><p>Protected module</p></RequireModule></ModuleProvider>)
    await screen.findByText('Module discovery unavailable: unavailable')
    expect(screen.queryByText('Protected module')).toBeNull()
  })

  it('drops the previous identity snapshot while a new session is loading', async () => {
    vi.mocked(listModules).mockResolvedValueOnce([llm])
    const content = <ModuleProvider><RequireModule name="llmplex"><p>Protected module</p></RequireModule></ModuleProvider>
    const { rerender } = render(content)
    await screen.findByText('Protected module')
    vi.mocked(listModules).mockImplementationOnce(() => new Promise(() => {}))
    session = { token: 'second', isAuthenticated: true }
    rerender(<ModuleProvider><RequireModule name="llmplex"><p>Protected module</p></RequireModule></ModuleProvider>)
    await waitFor(() => expect(screen.queryByText('Protected module')).toBeNull())
    expect(screen.getByRole('status').textContent).toContain('Loading modules')
  })

  it('hides disabled deployment choices and rejects a disabled URL preselection', async () => {
    vi.mocked(listModules).mockResolvedValue([llm])
    vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<MemoryRouter initialEntries={['/deploy?plane=mcplex&template=secret']}><QueryClientProvider client={client}><ModuleProvider><Deploy /></ModuleProvider></QueryClientProvider></MemoryRouter>)
    await screen.findByRole('button', { name: /^LLMPlex/ })
    expect(screen.queryByRole('button', { name: /^MCPlex/ })).toBeNull()
    await waitFor(() => expect(getCatalog).toHaveBeenCalledWith('llmplex'))
    expect(vi.mocked(getCatalog).mock.calls.every(([plane]) => plane === 'llmplex')).toBe(true)
    client.clear()
  })

  it('keeps client configuration modules out of the deployment plane picker', async () => {
    vi.mocked(listModules).mockResolvedValue([{ name: 'clientplex', version: '1.0.0', contract_version: 'v1', navigation: [{ path: '/clientplex', title: 'ClientPlex' }] }])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<MemoryRouter initialEntries={['/deploy?plane=clientplex']}><QueryClientProvider client={client}><ModuleProvider><Deploy /></ModuleProvider></QueryClientProvider></MemoryRouter>)
    await screen.findByText('No deployable modules are enabled.')
    expect(screen.queryByRole('button', { name: /^ClientPlex/ })).toBeNull()
    client.clear()
  })
})
