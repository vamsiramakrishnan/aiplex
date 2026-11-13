import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SkillsPlex from '../pages/SkillsPlex'
import { authenticatedRequest, getCatalog } from '../api/client'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), getCatalog: vi.fn() }))

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}><SkillsPlex /></QueryClientProvider></MemoryRouter>)
}
const server = { instance_id: 'skills-1', name: 'Docs skills', url: 'https://skills.example', skills: ['skill:invoke:summarise'], status: 'running' }
const empty = { templates: [], total: 0, page: 0, page_size: 20 }

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getCatalog).mockResolvedValue(empty)
})

describe('SkillsPlex tab counts', () => {
  it('omits counts while loading', () => {
    vi.mocked(getCatalog).mockReturnValue(new Promise(() => {}))
    vi.mocked(authenticatedRequest).mockReturnValue(new Promise(() => {}))
    show()
    for (const name of ['Servers', 'Catalog', 'Invocations']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /\(\d+\)/ })).not.toBeInTheDocument()
  })

  it('omits counts after a failure and shows the error, not an empty-state', async () => {
    vi.mocked(getCatalog).mockRejectedValue(new Error('catalog down'))
    vi.mocked(authenticatedRequest).mockRejectedValue(new Error('skills backend down'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('skills backend down')
    for (const name of ['Servers', 'Catalog', 'Invocations']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
    expect(screen.queryByText('No skill servers deployed yet.')).not.toBeInTheDocument()
  })

  it('shows real counts, including a real 0', async () => {
    vi.mocked(authenticatedRequest).mockImplementation((path: string) => (path.endsWith('/servers') ? Promise.resolve([server]) : Promise.resolve([])) as never)
    show()
    expect(await screen.findByRole('button', { name: 'Servers (1)' })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Invocations (0)' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Catalog (0)' })).toBeInTheDocument()
  })
})

describe('SkillsPlex tabs', () => {
  it('lists servers and shows the empty-state only for a real empty list', async () => {
    vi.mocked(authenticatedRequest).mockResolvedValue([])
    show()
    expect(await screen.findByText('No skill servers deployed yet.')).toBeInTheDocument()
  })

  it('shows a server with its skills', async () => {
    vi.mocked(authenticatedRequest).mockImplementation((path: string) => (path.endsWith('/servers') ? Promise.resolve([server]) : Promise.resolve([])) as never)
    show()
    expect(await screen.findByText('Docs skills')).toBeInTheDocument()
    expect(screen.getByText('summarise')).toBeInTheDocument()
  })

  it('shows unknown-safe duration and user in invocations: absent is a dash, a real 0ms is not hidden as absent', async () => {
    const inv = (id: string, duration?: number) => ({ id, agent_id: 'agent-' + id, instance_id: 'skills-1', skill_name: 'sk-' + id, status: 'ok', started_at: '2026-01-01T00:00:00Z', duration_ms: duration })
    vi.mocked(authenticatedRequest).mockImplementation((path: string) => (path.endsWith('/invocations') ? Promise.resolve([inv('absent'), inv('zero', 0), inv('some', 42)]) : Promise.resolve([])) as never)
    show()
    fireEvent.click(await screen.findByRole('button', { name: /Invocations/ }))
    const rowOf = async (skill: string) => (await screen.findByText(skill)).closest('tr') as HTMLElement
    expect((await rowOf('sk-absent')).textContent).not.toMatch(/\dms/)
    expect((await rowOf('sk-absent')).textContent).toMatch(/unknown.*unknown.*unknown/)
    expect((await rowOf('sk-some')).textContent).toContain('42ms')
    expect((await rowOf('sk-zero')).textContent).toContain('0ms')
  })
})
