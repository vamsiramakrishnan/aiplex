import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import Deploy from '../pages/Deploy'
import { deployInstance, getCatalog } from '../api/client'

vi.mock('../api/client', () => ({ getCatalog: vi.fn(), deployInstance: vi.fn(), applyManifest: vi.fn() }))
vi.mock('../hooks/useModules', () => ({ useModules: () => ({ enabled: [{ name: 'llmplex' }], loading: false, error: null }) }))

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getCatalog).mockResolvedValue({ templates: [{ id: 't1', name: 'Template one', plane: 'llmplex', source: 's', description: 'd' }], total: 1, page: 0, page_size: 20 } as never)
  vi.mocked(deployInstance).mockResolvedValue({ id: 'new inst/1', status: 'provisioning' } as never)
})

it('links the deployed instance with the router, not a full page load', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<MemoryRouter initialEntries={['/deploy?plane=llmplex&template=t1']}><QueryClientProvider client={client}><Routes>
    <Route path="/deploy" element={<Deploy />} />
    <Route path="/instances/:id" element={<p>Instance detail page</p>} />
  </Routes></QueryClientProvider></MemoryRouter>)
  fireEvent.click(await screen.findByRole('button', { name: 'Review' }))
  fireEvent.click(await screen.findByRole('button', { name: 'Deploy' }))
  const link = await screen.findByRole('link', { name: 'new inst/1' })
  expect(link).toHaveAttribute('href', '/instances/new%20inst%2F1')
  const notPrevented = fireEvent.click(link)
  expect(notPrevented).toBe(false) // the router handled the click (preventDefault), so the page does not reload
  expect(await screen.findByText('Instance detail page')).toBeInTheDocument()
})

async function reviewWith(pricing: object) {
  vi.mocked(getCatalog).mockResolvedValue({ templates: [{ id: 't1', name: 'Template one', plane: 'llmplex', source: 's', description: 'd', pricing }], total: 1, page: 0, page_size: 20 } as never)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MemoryRouter initialEntries={['/deploy?plane=llmplex&template=t1']}><QueryClientProvider client={client}><Deploy /></QueryClientProvider></MemoryRouter>)
  return (await screen.findByText('Pricing')).parentElement as HTMLElement
}
it('shows unknown for a missing template price', async () => {
  const box = await reviewWith({ output: 3 })
  expect(box.textContent).toContain('unknown/M input tokens')
  expect(box.textContent).toContain('$3/M output tokens')
})
it('shows $0 for a real zero template price', async () => {
  const box = await reviewWith({ input: 0, output: 0 })
  expect(box.textContent).toContain('$0/M input tokens')
  expect(box.textContent).toContain('$0/M output tokens')
  expect(box.textContent).not.toContain('unknown')
})
