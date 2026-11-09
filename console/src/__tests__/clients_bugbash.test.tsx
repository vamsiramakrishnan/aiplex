import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ClientPlex from '../pages/ClientPlex'
import { authenticatedRequest } from '../api/client'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn() }))
const request = vi.mocked(authenticatedRequest)
const registration = (id: string) => ({ id, revision: 1, spec: { id, profile_id: 'codex', mode: 'direct', protocol: 'openai-responses', principal_binding: 'principal:user' } })
const connectionPlan = (id: string) => ({ registration_id: id, support_status: 'configured-unverified', artifacts: [], services: [], warnings: [`plan for ${id}`] })
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}
function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><ClientPlex /></QueryClientProvider>)
}
beforeEach(() => { request.mockReset() })

describe('ClientPlex asynchronous plan ownership', () => {
  it('keeps the newest requested plan when responses arrive out of order', async () => {
    const first = deferred<ReturnType<typeof connectionPlan>>()
    const second = deferred<ReturnType<typeof connectionPlan>>()
    request.mockImplementation(async (path) => {
      if (path.endsWith('/profiles')) return []
      if (path.endsWith('/alpha/plan')) return first.promise
      if (path.endsWith('/beta/plan')) return second.promise
      return [registration('alpha'), registration('beta')]
    })
    show()
    await screen.findByText('alpha')
    const buttons = screen.getAllByRole('button', { name: 'Plan' })
    fireEvent.click(buttons[0]); fireEvent.click(buttons[1])
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/clients/registrations/beta/plan', expect.anything()))
    await act(async () => second.resolve(connectionPlan('beta')))
    expect(await screen.findByText('plan for beta')).toBeInTheDocument()
    await act(async () => first.resolve(connectionPlan('alpha')))
    expect(screen.queryByText('plan for alpha')).not.toBeInTheDocument()
    expect(screen.getByText('plan for beta')).toBeInTheDocument()
  })

  it('does not restore an old plan after starting a new registration', async () => {
    const pending = deferred<ReturnType<typeof connectionPlan>>()
    request.mockImplementation(async (path) => path.endsWith('/profiles') ? [] : path.endsWith('/plan') ? pending.promise : [registration('alpha')])
    show()
    await screen.findByText('alpha')
    fireEvent.click(screen.getByRole('button', { name: 'Plan' }))
    fireEvent.click(screen.getByRole('button', { name: 'New' }))
    await act(async () => pending.resolve(connectionPlan('alpha')))
    expect(screen.queryByText('plan for alpha')).not.toBeInTheDocument()
  })

  it('does not select a completed save after starting a new registration', async () => {
    const pending = deferred<ReturnType<typeof registration>>()
    request.mockImplementation(async (path, init) => path.endsWith('/profiles') ? [] : init?.method === 'PUT' ? pending.promise : [registration('alpha')])
    show()
    await screen.findByText('alpha')
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save registration' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/clients/registrations/alpha', expect.objectContaining({ method: 'PUT' })))
    fireEvent.click(screen.getByRole('button', { name: 'New' }))
    await act(async () => pending.resolve({ ...registration('alpha'), revision: 2 }))
    expect(screen.getByRole('heading', { name: 'New registration' })).toBeInTheDocument()
    expect((screen.getByLabelText(/Use credential references/) as HTMLTextAreaElement).value).toContain('developer')
  })

  it('keeps the current editor when another registration is deleted', async () => {
    request.mockImplementation(async (path, init) => path.endsWith('/profiles') ? [] : init?.method === 'DELETE' ? undefined : [registration('alpha'), registration('beta')])
    show()
    await screen.findByText('alpha')
    fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[1])
    fireEvent.click(screen.getAllByRole('button', { name: 'Delete' })[0])
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/clients/registrations/alpha', expect.objectContaining({ method: 'DELETE' })))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save registration' })).toBeEnabled())
    expect(screen.getByRole('heading', { name: 'Edit beta, revision 1' })).toBeInTheDocument()
  })
})
