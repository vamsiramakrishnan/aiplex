import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Agents from '../pages/Agents'
import { listAgents, listInstances, registerAgent } from '../api/client'
vi.mock('../api/client', () => ({ listAgents: vi.fn(), listInstances: vi.fn(), registerAgent: vi.fn(), deleteAgent: vi.fn() }))
function show() { const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }); return render(<QueryClientProvider client={client}><Agents /></QueryClientProvider>) }
function identity() { fireEvent.click(screen.getByText('Register Agent')); fireEvent.change(screen.getByPlaceholderText('tutor-agent'), { target: { value: 'my-agent' } }); fireEvent.change(screen.getByPlaceholderText('Tutor Agent'), { target: { value: 'My agent' } }); fireEvent.click(screen.getByText('Next')) }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(listAgents).mockResolvedValue([]); vi.mocked(listInstances).mockResolvedValue([{ id: 'current-instance', scopes: ['llm:model:current-model'] }] as never); vi.mocked(registerAgent).mockResolvedValue({ client_id: 'my-agent', display_name: 'My agent', auth_method: 'client_credentials', grant_types: ['client_credentials'], allowed_scopes: [], status: 'active', registered_at: new Date().toISOString(), client_secret: 'issued-once-fixture' }) })
describe('OAuth agent registration', () => {
  it('requires authorization-code callbacks and sends the selected real scope ceiling', async () => {
    show(); identity(); fireEvent.click(screen.getByText('Authorization Code + PKCE')); expect(screen.getByText('Next')).toBeDisabled(); fireEvent.change(screen.getByLabelText('OAuth callback URIs'), { target: { value: 'https://ide.example/callback' } }); fireEvent.click(screen.getByText('Next')); await screen.findByText('current-model'); expect(screen.queryByText('search_curriculum')).toBeNull(); fireEvent.click(screen.getByRole('checkbox')); fireEvent.click(screen.getByRole('button', { name: 'Review' })); fireEvent.click(screen.getByText('Register Agent'))
    await waitFor(() => expect(registerAgent).toHaveBeenCalledWith(expect.objectContaining({ auth_method: 'authorization_code', redirect_uris: ['https://ide.example/callback'], allowed_scopes: ['llm:model:current-model'], grant_types: ['authorization_code'] }), expect.anything()))
  })
  it('preserves the issued-once credential only until explicitly dismissed', async () => {
    show(); identity(); fireEvent.click(screen.getByText('Next')); fireEvent.click(screen.getByRole('button', { name: 'Review' })); fireEvent.click(screen.getByText('Register Agent')); const field = await screen.findByLabelText('Issued client secret'); expect(field).toHaveValue('issued-once-fixture'); expect(field).toHaveAttribute('type', 'password'); fireEvent.click(screen.getByText('Dismiss credential')); expect(screen.queryByLabelText('Issued client secret')).not.toBeInTheDocument()
  })
  it('does not misreport a failed list request as an empty registry', async () => {
    vi.mocked(listAgents).mockRejectedValue(new Error('Registry unavailable')); show(); expect(await screen.findByRole('alert')).toHaveTextContent('Registry unavailable'); expect(screen.queryByText(/No agents match/)).toBeNull()
  })
})
