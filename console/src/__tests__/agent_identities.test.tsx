import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentIdentities from '../components/AgentIdentities'
import Agents from '../pages/Agents'
import * as api from '../api/agentIdentity'
import { listAgents } from '../api/client'

vi.mock('../api/client', () => ({ listAgents: vi.fn(), listInstances: vi.fn(), registerAgent: vi.fn(), deleteAgent: vi.fn() }))
vi.mock('../api/agentIdentity', async (original) => ({
  ...(await original<typeof import('../api/agentIdentity')>()),
  listIdentityPresets: vi.fn(), listAgentIdentities: vi.fn(), putAgentIdentity: vi.fn(), deleteAgentIdentity: vi.fn(), planAgentIdentity: vi.fn(), verifyAgentIdentityToken: vi.fn(),
}))

const SECRET = 'eyJhbGciOi.PAYLOADPART.SECRETSIGNATURE'
const catalog: api.IdentityPresetCatalog = {
  live_authentication: false,
  default_audience: 'https://aiplex.example.com',
  presets: [
    {
      id: 'github-actions', display_name: 'GitHub Actions', cloud: 'github', description: 'Workflows in a repository', subject_shape: 'repo:OWNER/REPO:ref:REF', issuer_shape: 'https://token.actions.githubusercontent.com',
      allows_required_claims: true, algorithms: ['RS256'],
      inputs: [
        { name: 'repository', label: 'Repository', description: 'owner/name', required: true, example: 'acme/tutor' },
        { name: 'ref', label: 'Git ref', description: '', required: true, example: 'refs/heads/main' },
        { name: 'audience', label: 'Audience', description: '', required: true, default: 'https://aiplex.example.com' },
      ],
    },
    {
      id: 'azure-managed-identity', display_name: 'Azure Managed Identity', cloud: 'azure', description: 'VM identity', subject_shape: 'OBJECT_ID', issuer_shape: 'https://sts.windows.net/TENANT/',
      allows_required_claims: false, algorithms: ['RS256'],
      inputs: [{ name: 'token_version', label: 'Token version', description: '', required: true, enum: ['1.0', '2.0'] }],
    },
  ],
}
const binding: api.IdentityBinding = {
  id: 'gh-ci', client_id: 'tutor-agent', preset: 'github-actions', inputs: { repository: 'acme/tutor' }, display_name: 'CI', disabled: true,
  issuer: 'https://token.actions.githubusercontent.com', jwks_url: 'https://j', audience: 'https://aiplex.example.com', subject_claim: 'sub', subject: 'repo:acme/tutor:ref:refs/heads/main', revision: 4, principal_id: 'p',
}
const plan: api.IdentityPlan = {
  binding: { client_id: 'tutor-agent', id: 'gh-ci', preset: 'github-actions' }, cloud: 'github', issuer: binding.issuer, audience: binding.audience, subject: binding.subject,
  steps: [{ title: 'Add the workflow', description: 'Commit it', language: 'yaml', content: 'permissions:\n  id-token: write' }],
  token_snippet: { title: 'Acquire a token', language: 'bash', content: 'TOKEN=$(curl x)' }, aiplex_steps: [{ title: 'Dry-run a token' }], notes: ['Mind the ref'],
}

function show(agent = 'tutor-agent') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><AgentIdentities agentId={agent} /></QueryClientProvider>)
}
const apiError = (message: string, code: string) => Object.assign(new Error(message), { code, status: 409 })

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.listIdentityPresets).mockResolvedValue(catalog)
  vi.mocked(api.listAgentIdentities).mockResolvedValue([binding])
  vi.mocked(api.planAgentIdentity).mockResolvedValue(plan)
  vi.mocked(api.putAgentIdentity).mockResolvedValue({ ...binding, id: 'new-id', revision: 1 })
  localStorage.clear(); sessionStorage.clear()
})

describe('Agent identities section', () => {
  it('lists bindings with preset, cloud, issuer, subject and disabled state, and warns that live auth is off', async () => {
    show()
    const row = (await screen.findByText('CI (gh-ci)')).closest('tr')!
    expect(within(row).getByText('GitHub Actions')).toBeInTheDocument()
    expect(within(row).getByText('github')).toBeInTheDocument()
    expect(within(row).getByText(binding.issuer)).toBeInTheDocument()
    expect(within(row).getByText(binding.subject)).toBeInTheDocument()
    expect(within(row).getByText('Disabled')).toBeInTheDocument()
    expect(await screen.findByText(/Live authentication is not enabled/)).toBeInTheDocument()
  })

  it('hides the live-auth notice when enabled and explains an empty list', async () => {
    vi.mocked(api.listIdentityPresets).mockResolvedValue({ ...catalog, live_authentication: true })
    vi.mocked(api.listAgentIdentities).mockResolvedValue([])
    show()
    expect(await screen.findByText(/No identities bound yet/)).toBeInTheDocument()
    expect(screen.queryByText(/Live authentication is not enabled/)).toBeNull()
  })

  it('shows loading and distinguishes load errors from an empty list', async () => {
    vi.mocked(api.listAgentIdentities).mockRejectedValue(new Error('Forbidden'))
    vi.mocked(api.listIdentityPresets).mockRejectedValue(new Error('Catalog down'))
    show()
    expect(screen.getByText('Loading identities…')).toBeInTheDocument()
    expect(await screen.findByText(/Could not load identities: Forbidden/)).toBeInTheDocument()
    expect(await screen.findByText(/Could not load identity presets: Catalog down/)).toBeInTheDocument()
    expect(screen.queryByText(/No identities bound yet/)).toBeNull()
    expect(screen.getByText('Add identity')).toBeDisabled()
  })

  it('renders preset inputs from the catalog with required markers, example placeholders and enum selects, then saves', async () => {
    show(); await screen.findByText('CI (gh-ci)')
    fireEvent.click(screen.getByText('Add identity'))
    fireEvent.change(screen.getByLabelText('Identity preset'), { target: { value: 'github-actions' } })
    const repo = screen.getByLabelText(/Repository/)
    expect(repo).toHaveAttribute('placeholder', 'acme/tutor')
    expect(repo).toHaveAttribute('aria-required', 'true')
    expect(screen.getByLabelText(/Audience/)).toHaveValue('https://aiplex.example.com')
    const save = screen.getByRole('button', { name: 'Save identity' })
    expect(save).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/Binding ID/), { target: { value: 'Bad ID' } })
    expect(screen.getByText(/Use lower-case letters/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/Binding ID/), { target: { value: 'new-id' } })
    fireEvent.change(repo, { target: { value: 'acme/tutor' } })
    expect(save).toBeDisabled()
    expect(screen.getByText(/Still required: Git ref/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/Git ref/), { target: { value: 'refs/heads/main' } })
    fireEvent.change(screen.getByLabelText(/Extra required claims/), { target: { value: 'job_workflow_ref=acme/x' } })
    fireEvent.click(save)
    await waitFor(() => expect(api.putAgentIdentity).toHaveBeenCalledWith('tutor-agent', 'new-id', {
      preset: 'github-actions',
      inputs: { audience: 'https://aiplex.example.com', repository: 'acme/tutor', ref: 'refs/heads/main' },
      required_claims: { job_workflow_ref: 'acme/x' },
    }))
    expect(await screen.findByText('Added identity new-id.')).toBeInTheDocument()

    fireEvent.click(screen.getByText('Add identity'))
    fireEvent.change(screen.getByLabelText('Identity preset'), { target: { value: 'azure-managed-identity' } })
    const select = screen.getByLabelText(/Token version/)
    expect(select.tagName).toBe('SELECT')
    expect(within(select).getAllByRole('option').map(o => o.textContent)).toEqual(['Select…', '1.0', '2.0'])
    expect(screen.queryByLabelText(/Extra required claims/)).toBeNull()
  })

  it('reports a bound-subject conflict and an agent-state error from the API', async () => {
    vi.mocked(api.putAgentIdentity).mockRejectedValueOnce(apiError('external subject is already bound to agent "other"', 'SUBJECT_ALREADY_BOUND'))
    show(); await screen.findByText('CI (gh-ci)')
    fireEvent.click(screen.getByText('Add identity'))
    fireEvent.change(screen.getByLabelText('Identity preset'), { target: { value: 'azure-managed-identity' } })
    fireEvent.change(screen.getByLabelText(/Binding ID/), { target: { value: 'az' } })
    fireEvent.change(screen.getByLabelText(/Token version/), { target: { value: '2.0' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save identity' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/already bound.*already bound to agent "other"/)
    vi.mocked(api.putAgentIdentity).mockRejectedValueOnce(apiError('x', 'REVISION_CONFLICT'))
    fireEvent.click(screen.getByRole('button', { name: 'Save identity' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/already exists or was changed/))
  })

  it('removes with the current revision and surfaces a revision conflict', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    vi.mocked(api.deleteAgentIdentity).mockRejectedValueOnce(apiError('stale', 'REVISION_CONFLICT'))
    show(); await screen.findByText('CI (gh-ci)')
    fireEvent.click(screen.getByRole('button', { name: 'Remove identity gh-ci' }))
    await waitFor(() => expect(api.deleteAgentIdentity).toHaveBeenCalledWith('tutor-agent', 'gh-ci', 4))
    expect(await screen.findByText(/already exists or was changed/)).toBeInTheDocument()
  })

  it('shows the setup plan with copyable code blocks', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    show(); await screen.findByText('CI (gh-ci)')
    fireEvent.click(screen.getByRole('button', { name: 'Show setup plan for gh-ci' }))
    const planView = await screen.findByRole('region', { name: 'Setup plan for gh-ci' })
    expect(within(planView).getByText('Add the workflow')).toBeInTheDocument()
    expect(within(planView).getByText('Getting a token')).toBeInTheDocument()
    expect(within(planView).getByText('Mind the ref')).toBeInTheDocument()
    fireEvent.click(within(planView).getByRole('button', { name: 'Copy code for Add the workflow' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('permissions:\n  id-token: write'))
    expect(await within(planView).findByText('Copied')).toBeInTheDocument()
    expect(within(planView).queryByRole('button', { name: 'Copy code for Dry-run a token' })).toBeNull()
  })

  it('renders each verify check with its reason and does not retain the token anywhere after submit', async () => {
    vi.mocked(api.verifyAgentIdentityToken).mockResolvedValue({
      valid: false, live_authentication: false,
      checks: [{ name: 'signature', status: 'pass', reason: 'signature verified' }, { name: 'audience', status: 'fail', reason: 'aud does not match' }, { name: 'binding', status: 'skipped', reason: 'earlier failure' }],
    })
    const consoleSpies = (['log', 'info', 'warn', 'error', 'debug'] as const).map(m => vi.spyOn(console, m).mockImplementation(() => {}))
    show(); await screen.findByText('CI (gh-ci)')
    fireEvent.click(screen.getByRole('button', { name: 'Test a token against gh-ci' }))
    const panel = screen.getByRole('region', { name: 'Test a token' })
    const box = within(panel).getByLabelText('Workload token') as HTMLTextAreaElement
    expect(box.tagName).toBe('TEXTAREA')
    expect(box).toHaveAttribute('autocomplete', 'off')
    expect(box).not.toHaveAttribute('name')
    expect(within(panel).getByLabelText('Check against')).toHaveValue('gh-ci')
    fireEvent.change(box, { target: { value: `  ${SECRET}\n` } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Test token' }))
    await within(panel).findByText('aud does not match')
    expect(api.verifyAgentIdentityToken).toHaveBeenCalledWith(SECRET, { client_id: 'tutor-agent', binding_id: 'gh-ci' })
    expect(within(panel).getByText('Token is not valid.')).toBeInTheDocument()
    expect(within(panel).getByText('Fail')).toBeInTheDocument()
    expect(within(panel).getByText('Pass')).toBeInTheDocument()
    expect(within(panel).getByText('Skipped')).toBeInTheDocument()
    expect(within(panel).getByText('earlier failure')).toBeInTheDocument()
    // not retained in DOM (text or form values), storage, URL or console
    expect(box.value).toBe('')
    expect(document.body.innerHTML).not.toContain('SECRETSIGNATURE')
    expect(document.body.textContent).not.toContain('SECRETSIGNATURE')
    expect(Array.from(document.querySelectorAll('textarea,input')).every(el => !(el as HTMLInputElement).value.includes('SECRETSIGNATURE'))).toBe(true)
    expect(JSON.stringify({ ...localStorage })).not.toContain('SECRETSIGNATURE')
    expect(JSON.stringify({ ...sessionStorage })).not.toContain('SECRETSIGNATURE')
    expect(window.location.href).not.toContain('SECRETSIGNATURE')
    for (const spy of consoleSpies) expect(JSON.stringify(spy.mock.calls)).not.toContain('SECRETSIGNATURE')
  })

  it('tests against any binding and clears the token even when the request fails', async () => {
    vi.mocked(api.verifyAgentIdentityToken).mockRejectedValue(new Error('Service unavailable'))
    show(); await screen.findByText('CI (gh-ci)')
    fireEvent.click(screen.getByText('Test a token'))
    const panel = screen.getByRole('region', { name: 'Test a token' })
    const box = within(panel).getByLabelText('Workload token') as HTMLTextAreaElement
    expect(within(panel).getByRole('button', { name: 'Test token' })).toBeDisabled()
    fireEvent.change(box, { target: { value: SECRET } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Test token' }))
    expect(await within(panel).findByRole('alert')).toHaveTextContent('Service unavailable')
    expect(api.verifyAgentIdentityToken).toHaveBeenCalledWith(SECRET, undefined)
    expect(box.value).toBe('')
    expect(document.body.innerHTML).not.toContain('SECRETSIGNATURE')
  })

  it('appears when an agent row is expanded on the Agents page', async () => {
    vi.mocked(listAgents).mockResolvedValue([{ client_id: 'tutor-agent', display_name: 'Tutor', auth_method: 'client_credentials', grant_types: [], allowed_scopes: [], status: 'active', registered_at: new Date().toISOString() }] as never)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><Agents /></QueryClientProvider>)
    expect(screen.queryByText('Identities')).toBeNull()
    fireEvent.click(await screen.findByText('Tutor'))
    expect(await screen.findByRole('region', { name: 'Identities for tutor-agent' })).toBeInTheDocument()
  })
})
