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

describe('ClientPlex asynchronous editor and plan ownership', () => {
  it.each([
    { kind: 'existing', id: 'alpha', initialRevision: 1 },
    { kind: 'new', id: 'developer', initialRevision: 0 },
  ])('saves newer edits to $kind registrations using the acknowledged revision', async ({ id, initialRevision }) => {
    const pending = deferred<ReturnType<typeof registration>>()
    let stored = initialRevision ? registration(id) : undefined
    let writes = 0
    request.mockImplementation(async (path, init) => {
      if (path.endsWith('/profiles')) return []
      if (init?.method === 'PUT') {
        const currentRevision = stored?.revision ?? 0
        const expectedRevision = new Headers(init.headers).get('If-Match')
        if (expectedRevision !== (currentRevision ? `"${currentRevision}"` : null)) throw new Error('REVISION_CONFLICT')
        const spec = JSON.parse(init.body as string) as ReturnType<typeof registration>['spec']
        stored = { id: spec.id, revision: currentRevision + 1, spec }
        writes++
        return writes === 1 ? pending.promise : stored
      }
      return stored ? [stored] : []
    })
    show()
    if (initialRevision) {
      await screen.findByText(id)
      fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    }
    const editor = screen.getByLabelText(/Use credential references/) as HTMLTextAreaElement
    // The starter still has placeholders, which the page refuses to save, so a new registration starts from real values.
    if (!initialRevision) fireEvent.change(editor, { target: { value: JSON.stringify({ id, profile_id: 'codex', mode: 'gateway', protocol: 'openai-responses', principal_binding: 'principal:user', destination_ref: 'gcp-gateway', auth_mode: 'gateway-key', credential_env: 'CODEX_KEY', credential_ref: 'secret:codex' }, null, 2) } })
    fireEvent.click(screen.getByRole('button', { name: 'Save registration' }))
    await waitFor(() => expect(writes).toBe(1))

    // The server has committed, but the acknowledgement is still in flight.
    const draft = JSON.stringify({ ...JSON.parse(editor.value), principal_binding: 'principal:newer-edit' }, null, 2)
    fireEvent.change(editor, { target: { value: draft } })
    await act(async () => pending.resolve(stored!))
    expect(await screen.findByRole('heading', { name: `Edit ${id}, revision ${initialRevision + 1}` })).toBeInTheDocument()
    expect(editor).toHaveValue(draft)

    fireEvent.click(screen.getByRole('button', { name: 'Save registration' }))
    expect(await screen.findByRole('heading', { name: `Edit ${id}, revision ${initialRevision + 2}` })).toBeInTheDocument()
    const saves = request.mock.calls.filter(([, init]) => init?.method === 'PUT')
    expect(saves).toHaveLength(2)
    expect(saves[1][1]?.headers).toEqual({ 'If-Match': `"${initialRevision + 1}"` })
    expect(JSON.parse(saves[1][1]?.body as string)).toEqual(JSON.parse(draft))
    expect(stored?.spec.principal_binding).toBe('principal:newer-edit')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

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

  it('does not apply a save acknowledgement to another registration', async () => {
    const pending = deferred<ReturnType<typeof registration>>()
    request.mockImplementation(async (path, init) => path.endsWith('/profiles') ? [] : init?.method === 'PUT' ? pending.promise : [registration('alpha'), registration('beta')])
    show()
    await screen.findByText('alpha')
    fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0])
    fireEvent.click(screen.getByRole('button', { name: 'Save registration' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/clients/registrations/alpha', expect.objectContaining({ method: 'PUT' })))
    fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[1])
    const editor = screen.getByLabelText(/Use credential references/) as HTMLTextAreaElement
    const draft = JSON.stringify({ ...registration('beta').spec, principal_binding: 'principal:newer-edit' }, null, 2)
    fireEvent.change(editor, { target: { value: draft } })
    await act(async () => pending.resolve({ ...registration('alpha'), revision: 2 }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save registration' })).toBeEnabled())
    expect(screen.getByRole('heading', { name: 'Edit beta, revision 1' })).toBeInTheDocument()
    expect(editor).toHaveValue(draft)
  })

  it('preserves text entered while the current registration is being deleted', async () => {
    const pending = deferred<void>()
    request.mockImplementation(async (path, init) => path.endsWith('/profiles') ? [] : init?.method === 'DELETE' ? pending.promise : [registration('alpha')])
    show()
    await screen.findByText('alpha')
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/clients/registrations/alpha', expect.objectContaining({ method: 'DELETE' })))
    const editor = screen.getByLabelText(/Use credential references/) as HTMLTextAreaElement
    const draft = `${editor.value}\n `
    fireEvent.change(editor, { target: { value: draft } })
    await act(async () => pending.resolve())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled())
    expect(editor).toHaveValue(draft)
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
