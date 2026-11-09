import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ClientPlex from '../pages/ClientPlex'
import { authenticatedRequest } from '../api/client'
vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn() }))
const request = vi.mocked(authenticatedRequest)
const record = { id: 'dev', revision: 4, spec: { id: 'dev', profile_id: 'codex', mode: 'direct', protocol: 'openai-responses', principal_binding: 'principal:user', project_id: 'example-project', location: 'global', model_id: 'enabled-model', auth_mode: 'adc' } }
function show() { const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }); return render(<QueryClientProvider client={client}><ClientPlex /></QueryClientProvider>) }
beforeEach(() => { request.mockReset(); request.mockImplementation(async (path, init) => path.endsWith('/profiles') ? [] : init?.method === 'PUT' ? record : [record]) })
describe('ClientPlex', () => {
  it('sends current revision while editing', async () => {
    show(); await screen.findByText('dev'); fireEvent.click(screen.getByText('Edit')); fireEvent.click(screen.getByText('Save registration'))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/api/v1/clients/registrations/dev', expect.objectContaining({ method: 'PUT', headers: { 'If-Match': '"4"' } })))
  })
  it('shows a server plan without applying workstation files', async () => {
    request.mockImplementation(async (path) => path.endsWith('/profiles') ? [] : path.endsWith('/plan') ? { support_status: 'configured-unverified', artifacts: [{ target: '~/.codex/config.toml', format: 'toml', merge_strategy: 'reviewed-fragment', content: 'model_provider = "gcp"' }], services: [], warnings: ['No workstation files were modified.'] } : [record])
    show(); await screen.findByText('dev'); fireEvent.click(screen.getByText('Plan')); expect(await screen.findByText('No workstation files were modified.')).toBeInTheDocument(); expect(screen.getByText('model_provider = "gcp"')).toBeInTheDocument()
  })
})
