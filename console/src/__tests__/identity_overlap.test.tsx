import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AgentIdentities from '../components/AgentIdentities'
import UserIdentity from '../pages/UserIdentity'
import * as agentApi from '../api/agentIdentity'
import * as userApi from '../api/userIdentity'

vi.mock('../api/agentIdentity', async (original) => ({
  ...(await original<typeof import('../api/agentIdentity')>()),
  listIdentityPresets: vi.fn(), listAgentIdentities: vi.fn(), putAgentIdentity: vi.fn(), deleteAgentIdentity: vi.fn(), planAgentIdentity: vi.fn(), verifyAgentIdentityToken: vi.fn(),
}))
vi.mock('../api/userIdentity', async (original) => ({
  ...(await original<typeof import('../api/userIdentity')>()),
  listUserProviderPresets: vi.fn(), listUserProviders: vi.fn(), putUserProvider: vi.fn(), deleteUserProvider: vi.fn(), planUserProvider: vi.fn(), verifyUserToken: vi.fn(), invalidateMembership: vi.fn(),
}))

const conflict = (code: string, message: string) => Object.assign(new Error(message), { status: 409, code, details: { conflict: { kind: code === 'IDENTITY_OVERLAPS_USER_PROVIDER' ? 'user identity provider' : 'agent identity binding', id: 'other-1', issuer: 'https://issuer.example', audience: 'aiplex' }, acknowledge_with: 'acknowledge_overlap' } })

function wrap(ui: React.ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>{ui}</QueryClientProvider>)
}

describe('agent identity form: overlap with a user provider', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(agentApi.listIdentityPresets).mockResolvedValue({
      live_authentication: true,
      presets: [{ id: 'github-actions', display_name: 'GitHub Actions', cloud: 'github', description: 'd', subject_shape: 's', issuer_shape: 'i', allows_required_claims: false, algorithms: ['RS256'], inputs: [{ name: 'repository', label: 'Repository', description: '', required: true }] }],
    })
    vi.mocked(agentApi.listAgentIdentities).mockResolvedValue([])
  })

  const fill = async () => {
    wrap(<AgentIdentities agentId="tutor" />)
    const add = await screen.findByRole('button', { name: 'Add identity' })
    await waitFor(() => expect(add).toBeEnabled())
    fireEvent.click(add)
    fireEvent.change(await screen.findByLabelText('Identity preset'), { target: { value: 'github-actions' } })
    fireEvent.change(screen.getByLabelText(/Binding ID/), { target: { value: 'ci' } })
    fireEvent.change(screen.getByLabelText(/Repository/), { target: { value: 'acme/app' } })
  }

  it('names the conflict, requires an explicit second step, and sends acknowledge_overlap only then', async () => {
    vi.mocked(agentApi.putAgentIdentity).mockRejectedValueOnce(conflict('IDENTITY_OVERLAPS_USER_PROVIDER', 'issuer and audience are also trusted by user identity provider "other-1"'))
      .mockResolvedValueOnce({ id: 'ci', client_id: 'tutor', warnings: ['acknowledged overlap: other-1'] } as agentApi.IdentityBinding)
    await fill()
    fireEvent.click(screen.getByRole('button', { name: 'Save identity' }))
    const alert = await screen.findByText(/also trusted by user identity provider "other-1"/)
    const box = alert.closest('[role="alert"]') as HTMLElement
    expect(within(box).getByText('other-1')).toBeInTheDocument()
    expect(agentApi.putAgentIdentity).toHaveBeenCalledTimes(1)
    expect(vi.mocked(agentApi.putAgentIdentity).mock.calls[0][2]).not.toHaveProperty('acknowledge_overlap')

    fireEvent.click(within(box).getByRole('button', { name: /Save anyway/ }))
    await waitFor(() => expect(agentApi.putAgentIdentity).toHaveBeenCalledTimes(2))
    expect(vi.mocked(agentApi.putAgentIdentity).mock.calls[1][2]).toMatchObject({ preset: 'github-actions', inputs: { repository: 'acme/app' }, acknowledge_overlap: true })
    expect(await screen.findByText(/Added identity ci\. Warning: acknowledged overlap: other-1/)).toBeInTheDocument()
  })

  it('a fresh submit does not carry the acknowledgement', async () => {
    vi.mocked(agentApi.putAgentIdentity).mockRejectedValue(conflict('IDENTITY_OVERLAPS_USER_PROVIDER', 'overlap'))
    await fill()
    fireEvent.click(screen.getByRole('button', { name: 'Save identity' }))
    fireEvent.click(await screen.findByRole('button', { name: /Save anyway/ }))
    await waitFor(() => expect(agentApi.putAgentIdentity).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: 'Save identity' }))
    await waitFor(() => expect(agentApi.putAgentIdentity).toHaveBeenCalledTimes(3))
    expect(vi.mocked(agentApi.putAgentIdentity).mock.calls[2][2]).not.toHaveProperty('acknowledge_overlap')
  })
})

const okta: userApi.UserProviderPreset = {
  id: 'okta', display_name: 'Okta', vendor: 'okta', description: 'Okta', issuer_shape: 'https://ORG.okta.com', subject_shape: 'sub', group_sources: ['claim', 'membership-authority'],
  allows_required_claims: false, algorithms: ['RS256'], directory_kinds: ['okta'], client_claim_default: 'cid',
  inputs: [{ name: 'issuer', label: 'Issuer URL', description: '', required: true }, { name: 'groups_from', label: 'Groups from', description: '', required: true, enum: ['claim', 'membership-authority'] }],
}
const oidc: userApi.UserProviderPreset = { id: 'oidc', display_name: 'Generic OIDC', vendor: 'oidc', description: '', issuer_shape: '', subject_shape: '', group_sources: ['claim'], allows_required_claims: false, algorithms: [], inputs: [] }
const provider = (over: Partial<userApi.UserProvider> = {}): userApi.UserProvider => ({
  id: 'corp', preset: 'okta', inputs: { issuer: 'https://acme.okta.com', groups_from: 'claim' }, disabled: false, issuer: 'https://acme.okta.com', jwks_url: 'j', audience: 'a', subject_claim: 'sub', revision: 2, principal_pool: 'pool', ...over,
})

describe('user provider form: directory, client claim, overlap', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(userApi.listUserProviderPresets).mockResolvedValue({ presets: [okta, oidc], live_authentication: true })
    vi.mocked(userApi.listUserProviders).mockResolvedValue([])
  })

  const openAdd = async (preset = 'okta') => {
    wrap(<UserIdentity />)
    const add = await screen.findByRole('button', { name: 'Add provider' })
    await waitFor(() => expect(add).toBeEnabled())
    fireEvent.click(add)
    fireEvent.change(await screen.findByLabelText('Provider preset'), { target: { value: preset } })
  }

  it('limits directory kinds to the preset, labels credentials as references, and sends the directory block', async () => {
    vi.mocked(userApi.putUserProvider).mockResolvedValue(provider())
    await openAdd()
    fireEvent.change(screen.getByLabelText(/Provider ID/), { target: { value: 'corp' } })
    fireEvent.change(screen.getByLabelText(/Issuer URL/), { target: { value: 'https://acme.okta.com' } })
    fireEvent.change(screen.getByLabelText(/Groups from/), { target: { value: 'claim' } })
    fireEvent.click(screen.getByLabelText(/Resolve groups from the identity provider/))
    const kind = screen.getByLabelText('Directory kind')
    expect(within(kind).getAllByRole('option').map(o => o.textContent)).toEqual(['okta'])
    expect(screen.getByText(/Never type a secret value here/)).toBeInTheDocument()
    expect(screen.getByLabelText('API token reference')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Okta org URL'), { target: { value: 'https://acme.okta.com' } })
    fireEvent.change(screen.getByLabelText('Group identifier'), { target: { value: 'name' } })
    fireEvent.change(screen.getByLabelText('API token reference'), { target: { value: 'okta-token' } })
    fireEvent.change(screen.getByLabelText('Client claim'), { target: { value: 'custom' } })
    fireEvent.change(screen.getByLabelText('Claim name'), { target: { value: 'azp' } })
    fireEvent.change(screen.getByLabelText(/Allowed client IDs/), { target: { value: 'cursor\nvscode' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save provider' }))
    await waitFor(() => expect(userApi.putUserProvider).toHaveBeenCalled())
    expect(vi.mocked(userApi.putUserProvider).mock.calls[0][1]).toEqual({
      preset: 'okta', inputs: { issuer: 'https://acme.okta.com', groups_from: 'claim' }, disabled: false,
      directory: { kind: 'okta', mode: 'fallback', credential: { api_token_ref: 'okta-token' }, org_url: 'https://acme.okta.com', group_id: 'name' },
      client_claim: 'azp', client_ids: ['cursor', 'vscode'],
    })
  })

  it('forces always-mode when groups come from the membership authority, and hides the directory for presets without one', async () => {
    await openAdd()
    fireEvent.change(screen.getByLabelText(/Groups from/), { target: { value: 'membership-authority' } })
    fireEvent.click(screen.getByLabelText(/Resolve groups from the identity provider/))
    expect(screen.getByLabelText('Mode')).toBeDisabled()
    expect(screen.getByLabelText('Mode')).toHaveValue('always')
    fireEvent.change(screen.getByLabelText('Provider preset'), { target: { value: 'oidc' } })
    expect(screen.queryByLabelText('Directory kind')).toBeNull()
    expect(screen.queryByText('Directory groups')).toBeNull()
  })

  it('an existing provider without a client claim keeps "none" rather than reverting to the preset default', async () => {
    vi.mocked(userApi.listUserProviders).mockResolvedValue([provider()])
    vi.mocked(userApi.putUserProvider).mockResolvedValue(provider())
    wrap(<UserIdentity />)
    fireEvent.click(await screen.findByRole('button', { name: 'Edit provider corp' }))
    expect(await screen.findByLabelText('Client claim')).toHaveValue('none')
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(userApi.putUserProvider).toHaveBeenCalled())
    expect(vi.mocked(userApi.putUserProvider).mock.calls[0][1]).toMatchObject({ client_claim: 'none' })
  })

  it('shows the named conflict on an agent-binding overlap and acknowledges only on explicit request', async () => {
    vi.mocked(userApi.putUserProvider).mockRejectedValueOnce(conflict('IDENTITY_OVERLAPS_AGENT_BINDING', 'issuer and audience are also trusted by agent identity binding "tutor/ci"'))
      .mockResolvedValueOnce(provider({ warnings: ['acknowledged overlap: agent identity binding tutor/ci'] }))
    await openAdd('oidc')
    fireEvent.change(screen.getByLabelText(/Provider ID/), { target: { value: 'corp' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save provider' }))
    const text = await screen.findByText(/also trusted by agent identity binding "tutor\/ci"/)
    const box = text.closest('[role="alert"]') as HTMLElement
    expect(within(box).getByText('other-1')).toBeInTheDocument()
    expect(vi.mocked(userApi.putUserProvider).mock.calls[0][1]).not.toHaveProperty('acknowledge_overlap')
    fireEvent.click(within(box).getByRole('button', { name: /Save anyway/ }))
    await waitFor(() => expect(userApi.putUserProvider).toHaveBeenCalledTimes(2))
    expect(vi.mocked(userApi.putUserProvider).mock.calls[1][1]).toMatchObject({ acknowledge_overlap: true })
    // warnings from the response are shown, once
    const warnings = await screen.findByRole('list', { name: 'Warnings' })
    expect(warnings).toHaveTextContent('acknowledged overlap: agent identity binding tutor/ci')
  })

  it('never echoes response-only fields on a later edit (no stale acknowledgement or warnings)', async () => {
    vi.mocked(userApi.listUserProviders).mockResolvedValue([provider({ warnings: ['old warning'], client_claim: 'azp' })])
    vi.mocked(userApi.putUserProvider).mockResolvedValue(provider())
    wrap(<UserIdentity />)
    fireEvent.click(await screen.findByRole('button', { name: 'Edit provider corp' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(userApi.putUserProvider).toHaveBeenCalled())
    const body = vi.mocked(userApi.putUserProvider).mock.calls[0][1]
    expect(body).not.toHaveProperty('warnings')
    expect(body).not.toHaveProperty('acknowledge_overlap')
    expect(body).not.toHaveProperty('principal_pool')
  })
})

describe('refresh groups and token test additions', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(userApi.listUserProviderPresets).mockResolvedValue({ presets: [okta], live_authentication: true })
  })

  it('offers refresh only for providers with a directory, and states it is per replica', async () => {
    vi.mocked(userApi.listUserProviders).mockResolvedValue([
      provider({ id: 'with-dir', directory: { kind: 'okta', mode: 'always', credential: { api_token_ref: 't' } } }),
      provider({ id: 'no-dir', issuer: 'https://other', audience: 'b' }),
    ])
    vi.mocked(userApi.invalidateMembership).mockResolvedValue({ principal_id: 'user:pool:abc', not_before: '2026-11-09T10:00:00Z', scope: 'this server process' })
    wrap(<UserIdentity />)
    expect(await screen.findByRole('button', { name: 'Refresh groups for a user of with-dir' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Refresh groups for a user of no-dir' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Refresh groups for a user of with-dir' }))
    const panel = await screen.findByRole('region', { name: 'Refresh groups for a user of with-dir' })
    expect(panel).toHaveTextContent(/only to the server replica that receives this request/)
    expect(within(panel).getByRole('button', { name: 'Refresh groups' })).toBeDisabled()
    fireEvent.change(within(panel).getByLabelText(/User's subject/), { target: { value: '00u123' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Refresh groups' }))
    await waitFor(() => expect(userApi.invalidateMembership).toHaveBeenCalledWith('with-dir', '00u123'))
    expect(await within(panel).findByText(/user:pool:abc/)).toBeInTheDocument()
    expect(panel).toHaveTextContent('this server process')
  })

  it('shows the client and federation_overlap checks, the client application, and where groups came from', async () => {
    vi.mocked(userApi.listUserProviders).mockResolvedValue([provider()])
    vi.mocked(userApi.verifyUserToken).mockResolvedValue({
      valid: false, groups_available: false, groups_note: 'token invalid', groups_source: 'unavailable', is_workforce: false, live_authentication: true,
      checks: [
        { name: 'client', status: 'fail', reason: 'client application "x" is not in the provider\'s client_ids' },
        { name: 'federation_overlap', status: 'fail', reason: 'token is also claimed by agent identity binding "tutor/ci"' },
      ],
    })
    wrap(<UserIdentity />)
    fireEvent.click(await screen.findByRole('button', { name: 'Test a token against corp' }))
    fireEvent.change(await screen.findByLabelText('User token'), { target: { value: 'a.b.c' } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    const result = await screen.findByLabelText('Token test result')
    expect(within(result).getByText('client')).toBeInTheDocument()
    expect(within(result).getByText(/not in the provider's client_ids/)).toBeInTheDocument()
    expect(within(result).getByText('federation_overlap')).toBeInTheDocument()
    expect(within(result).getByText(/also claimed by agent identity binding/)).toBeInTheDocument()
  })

  it('reports directory groups source and client application on a valid token', async () => {
    vi.mocked(userApi.listUserProviders).mockResolvedValue([provider()])
    vi.mocked(userApi.verifyUserToken).mockResolvedValue({
      valid: true, groups_available: true, groups: ['eng'], groups_source: 'directory:okta', client_id: 'cursor-client', principal_id: 'user:p', checks: [{ name: 'client', status: 'pass', reason: 'issued to client application "cursor-client"' }], is_workforce: true, live_authentication: true,
    })
    wrap(<UserIdentity />)
    fireEvent.click(await screen.findByRole('button', { name: 'Test a token against corp' }))
    fireEvent.change(await screen.findByLabelText('User token'), { target: { value: 'a.b.c' } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    const result = await screen.findByLabelText('Token test result')
    expect(within(result).getByText('the directory (okta)')).toBeInTheDocument()
    expect(within(result).getByText('cursor-client')).toBeInTheDocument()
  })
})
