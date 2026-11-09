import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { expect, it, vi } from 'vitest'
import A2APlex from '../pages/A2APlex'
import { authenticatedRequest, getCatalog } from '../api/client'
vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), getCatalog: vi.fn() }))
it('distinguishes a failed deployed-agent request from an empty deployment list', async () => {
  vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 })
  vi.mocked(authenticatedRequest).mockRejectedValue(new Error('Native server unavailable'))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<MemoryRouter><QueryClientProvider client={client}><A2APlex /></QueryClientProvider></MemoryRouter>)
  expect(await screen.findByRole('alert')).toHaveTextContent('Native server unavailable')
  expect(screen.queryByText('No A2A agents deployed yet.')).not.toBeInTheDocument()
})
