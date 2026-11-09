import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Permissions from '../pages/Permissions'
import { authenticatedRequest, listInstances } from '../api/client'
vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), listInstances: vi.fn() }))
const principal = 'principal-' + 'a'.repeat(64)
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: { sub: 'principal-' + 'a'.repeat(64) } }) }))
function show() { const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }); return render(<QueryClientProvider client={client}><Permissions /></QueryClientProvider>) }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(listInstances).mockResolvedValue([]); vi.mocked(authenticatedRequest).mockResolvedValue({ user_id: principal, scopes: ['llm:model:current-model'], by_plane: {} }) })
describe('Canonical user permissions', () => {
  it('rejects email keys and saves manual grants only for a loaded canonical identity', async () => {
    show(); fireEvent.change(screen.getByLabelText('Canonical principal ID'), { target: { value: 'someone@example.com' } }); expect(screen.getByText('Load scopes')).toBeDisabled(); fireEvent.click(screen.getByText('Use my principal')); fireEvent.click(screen.getByText('Load scopes')); await screen.findByText('current-model')
    fireEvent.click(screen.getByText('Save permissions')); await waitFor(() => expect(authenticatedRequest).toHaveBeenCalledWith(`/auth/users/${principal}/scopes`, { method: 'PUT', body: JSON.stringify({ scopes: ['llm:model:current-model'] }) }))
    fireEvent.change(screen.getByLabelText('Canonical principal ID'), { target: { value: 'principal-' + 'b'.repeat(64) } }); expect(screen.queryByText('Save permissions')).not.toBeInTheDocument()
  })
  it('shows load failures instead of exposing another principal’s scopes', async () => {
    vi.mocked(authenticatedRequest).mockRejectedValue(new Error('Admin access required')); show(); fireEvent.click(screen.getByText('Use my principal')); fireEvent.click(screen.getByText('Load scopes')); expect(await screen.findByRole('alert')).toHaveTextContent('Admin access required'); expect(screen.queryByText('Save permissions')).not.toBeInTheDocument()
  })
})
