import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import UserIdentity from '../pages/UserIdentity'
import * as api from '../api/userIdentity'
import { overlapConflict, providerRequest, unknownProviderFields } from '../api/userIdentity'

vi.mock('../api/userIdentity', async (original) => ({
  ...(await original<typeof import('../api/userIdentity')>()),
  listUserProviderPresets: vi.fn(), listUserProviders: vi.fn(), putUserProvider: vi.fn(), deleteUserProvider: vi.fn(), planUserProvider: vi.fn(), verifyUserToken: vi.fn(),
}))

const SECRET = 'eyJhbGciOi.USERPAYLOAD.USERSIGNATURE'
const catalog: api.UserPresetCatalog = {
  live_authentication: false,
  presets: [
    {
      id: 'okta', display_name: 'Okta', vendor: 'okta', description: 'Okta org authorization server', issuer_shape: 'https://ORG.okta.com', subject_shape: 'sub',
      group_sources: ['claim', 'membership-authority'], allows_required_claims: true, algorithms: ['RS256'],
      directory_kinds: ['okta'], client_claim_default: 'cid (Okta access tokens)',
      inputs: [
        { name: 'issuer', label: 'Issuer URL', description: 'Your Okta issuer', required: true, example: 'https://acme.okta.com' },
        { name: 'audience', label: 'Audience', description: '', required: true },
        { name: 'groups_from', label: 'Groups from', description: '', required: true, enum: ['claim', 'membership-authority'] },
        { name: 'future_input', label: 'Brand new input', description: 'Added by a later server', required: false },
      ],
    },
    { id: 'oidc', display_name: 'Generic OIDC', vendor: 'oidc', description: 'Any issuer', issuer_shape: 'any', subject_shape: 'sub', group_sources: ['claim'], allows_required_claims: false, algorithms: ['RS256'], inputs: [] },
  ],
}
const provider: api.UserProvider = {
  id: 'corp-okta', preset: 'okta', inputs: { issuer: 'https://acme.okta.com', audience: 'aiplex', groups_from: 'claim' }, display_name: 'Corp Okta', disabled: false,
  issuer: 'https://acme.okta.com', jwks_url: 'https://acme.okta.com/keys', audience: 'aiplex', subject_claim: 'sub', extra_claims: { hd: 'acme.com' }, required_claims: { hd: 'acme.com', tid: 'x' },
  revision: 7, principal_pool: 'pool-acme',
  groups_from: 'claim', group_claim: 'groups',
  directory: { kind: 'okta', mode: 'fallback', credential: { api_token_ref: 'okta-token' }, org_url: 'https://acme.okta.com', group_id: 'name' },
  client_claim: 'azp', client_ids: ['cursor-client'], warnings: ['stale response warning'], future_flag: { keep: true },
}
const plan: api.UserPlan = {
  provider: { id: 'corp-okta', preset: 'okta' }, vendor: 'okta', issuer: provider.issuer, audience: 'aiplex', subject_claim: 'sub', groups_from: 'claim', group_claim: 'groups', principal_pool: 'pool-acme',
  steps: [{ title: 'Create an app', description: 'In the Okta admin console', language: 'bash', content: 'okta apps create' }],
  token_snippet: { title: 'Acquire a token', language: 'bash', content: 'curl token' }, aiplex_steps: [{ title: 'Test a token' }], notes: ['Pin the audience'],
}
const checks = [
  { name: 'signature', status: 'pass' as const, reason: 'verified against pinned keys' },
  { name: 'audience', status: 'fail' as const, reason: 'audience does not match' },
  { name: 'groups', status: 'skipped' as const, reason: 'token invalid' },
]

let client: QueryClient
function show() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><UserIdentity /></QueryClientProvider>)
}
const apiError = (message: string, status: number, code?: string) => Object.assign(new Error(message), { status, code })

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.listUserProviderPresets).mockResolvedValue(catalog)
  vi.mocked(api.listUserProviders).mockResolvedValue([provider])
  vi.mocked(api.planUserProvider).mockResolvedValue(plan)
  vi.mocked(api.putUserProvider).mockImplementation(async (id, body, revision) => ({ ...provider, ...body, id, revision: (revision ?? 0) + 1 } as api.UserProvider))
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: vi.fn().mockResolvedValue(undefined) }, configurable: true })
  localStorage.clear(); sessionStorage.clear()
})

describe('user providers list', () => {
  it('lists providers and warns when live authentication is disabled', async () => {
    show()
    const row = (await screen.findByText('Corp Okta (corp-okta)')).closest('tr')!
    expect(within(row).getByText('Okta')).toBeInTheDocument()
    expect(within(row).getByText(provider.issuer)).toBeInTheDocument()
    expect(within(row).getByText('Enabled')).toBeInTheDocument()
    expect(await screen.findByText(/Live authentication is not enabled/)).toBeInTheDocument()
  })

  it('hides the notice when live authentication is enabled and explains an empty list', async () => {
    vi.mocked(api.listUserProviderPresets).mockResolvedValue({ ...catalog, live_authentication: true })
    vi.mocked(api.listUserProviders).mockResolvedValue([])
    show()
    expect(await screen.findByText(/No user identity providers yet/)).toBeInTheDocument()
    expect(screen.queryByText(/Live authentication is not enabled/)).toBeNull()
  })

  it('shows a forbidden state', async () => {
    vi.mocked(api.listUserProviders).mockRejectedValue(apiError('forbidden', 403))
    show()
    expect(await screen.findByText(/do not have access to user identity providers/)).toBeInTheDocument()
  })
})

async function openAdd() {
  const button = await screen.findByRole('button', { name: 'Add provider' })
  await waitFor(() => expect(button).toBeEnabled())
  fireEvent.click(button)
}

describe('add a provider', () => {
  it('renders the form from the preset catalog, including inputs this console has never seen', async () => {
    show()
    await openAdd()
    fireEvent.change(await screen.findByLabelText('Provider preset'), { target: { value: 'okta' } })
    expect(screen.getByLabelText(/Issuer URL/)).toBeInTheDocument()
    expect(screen.getByLabelText('Brand new input')).toBeInTheDocument()
    const save = screen.getByRole('button', { name: 'Save provider' })
    expect(save).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/Provider ID/), { target: { value: 'new-okta' } })
    fireEvent.change(screen.getByLabelText(/Issuer URL/), { target: { value: 'https://new.okta.com' } })
    fireEvent.change(screen.getByLabelText(/^Audience/), { target: { value: 'aiplex' } })
    fireEvent.change(screen.getByLabelText(/Groups from/), { target: { value: 'claim' } })
    fireEvent.change(screen.getByLabelText(/Extra required claims/), { target: { value: 'hd=acme.com' } })
    fireEvent.click(save)
    await waitFor(() => expect(api.putUserProvider).toHaveBeenCalled())
    expect(vi.mocked(api.putUserProvider).mock.calls[0]).toEqual([
      'new-okta',
      { preset: 'okta', inputs: { issuer: 'https://new.okta.com', audience: 'aiplex', groups_from: 'claim' }, disabled: false, required_claims: { hd: 'acme.com' } },
      undefined,
    ])
    expect(await screen.findByText('Saved provider new-okta.')).toBeInTheDocument()
  })

  it('rejects an invalid provider id and surfaces a duplicate-source error', async () => {
    vi.mocked(api.putUserProvider).mockRejectedValue(apiError('already trusted by corp-okta', 409, 'SOURCE_ALREADY_CONFIGURED'))
    show()
    await openAdd()
    fireEvent.change(await screen.findByLabelText('Provider preset'), { target: { value: 'oidc' } })
    fireEvent.change(screen.getByLabelText(/Provider ID/), { target: { value: 'Bad_ID' } })
    expect(screen.getByText(/lower-case letters/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save provider' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/Provider ID/), { target: { value: 'dup' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save provider' }))
    expect(await screen.findByText(/another provider already trusts that issuer and audience/)).toBeInTheDocument()
  })
})

describe('edit a provider keeps unknown fields', () => {
  it('sends fields the console does not render and omits server-derived ones', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit provider corp-okta' }))
    expect(await screen.findByText(/additional settings this console does not edit \(future_flag\)/)).toBeInTheDocument()
    expect(screen.getByLabelText('Provider preset')).toBeDisabled()
    expect(screen.getByLabelText(/Provider ID/)).toHaveAttribute('readonly')
    fireEvent.change(screen.getByLabelText('Display name (optional)'), { target: { value: 'Corp Okta 2' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await waitFor(() => expect(api.putUserProvider).toHaveBeenCalled())
    const [id, body, revision] = vi.mocked(api.putUserProvider).mock.calls[0]
    expect(id).toBe('corp-okta')
    expect(revision).toBe(7)
    expect(body).toEqual({
      preset: 'okta',
      inputs: provider.inputs,
      required_claims: { hd: 'acme.com' },      // operator extras only, not the derived tid
      display_name: 'Corp Okta 2',
      disabled: false,
      directory: provider.directory,
      client_claim: 'azp',
      client_ids: ['cursor-client'],
      future_flag: { keep: true },
    })
    // response-only and derived fields are never echoed back
    for (const derived of ['issuer', 'jwks_url', 'audience', 'subject_claim', 'revision', 'principal_pool', 'id', 'extra_claims', 'warnings', 'groups_from', 'group_claim', 'acknowledge_overlap']) expect(body).not.toHaveProperty(derived)
  })

  it('pure helpers agree', () => {
    expect(Object.keys(unknownProviderFields(provider))).toEqual(['future_flag'])
    expect(providerRequest('okta', { inputs: {}, display_name: '', disabled: true, required_claims: {}, client_ids: [] }, provider)).toEqual({
      preset: 'okta', inputs: {}, disabled: true, future_flag: { keep: true },
    })
  })

  it('reports a revision conflict on edit', async () => {
    vi.mocked(api.putUserProvider).mockRejectedValue(apiError('changed', 409, 'REVISION_CONFLICT'))
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit provider corp-okta' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText(/changed by someone else/)).toBeInTheDocument()
  })
})

describe('setup plan', () => {
  it('shows copyable steps', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Show setup plan for corp-okta' }))
    const section = await screen.findByRole('region', { name: 'Setup plan for corp-okta' })
    expect(within(section).getByText('Create an app')).toBeInTheDocument()
    expect(within(section).getByText('pool-acme')).toBeInTheDocument()
    fireEvent.click(within(section).getByRole('button', { name: 'Copy code for Create an app' }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith('okta apps create'))
    expect(within(section).getByText('Pin the audience')).toBeInTheDocument()
  })
})

describe('delete', () => {
  it('asks for confirmation and sends the revision', async () => {
    vi.mocked(api.deleteUserProvider).mockResolvedValue(undefined)
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Remove provider corp-okta' }))
    expect(api.deleteUserProvider).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm remove' }))
    await waitFor(() => expect(api.deleteUserProvider).toHaveBeenCalledWith('corp-okta', 7))
  })
})

describe('test a token', () => {
  const open = async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Test a token against corp-okta' }))
    return screen.findByLabelText('User token')
  }

  it('sends the token once, clears the box, and never retains it', async () => {
    vi.mocked(api.verifyUserToken).mockResolvedValue({ valid: false, checks, groups_available: false, groups_note: 'token not valid', is_workforce: false, live_authentication: false })
    const box = await open()
    expect(screen.getByLabelText('Check against')).toHaveValue('corp-okta')
    fireEvent.change(box, { target: { value: `  ${SECRET}\n` } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    expect((box as HTMLTextAreaElement).value).toBe('')                 // cleared on submit, before the response
    await waitFor(() => expect(api.verifyUserToken).toHaveBeenCalledWith(SECRET, 'corp-okta'))
    const result = await screen.findByLabelText('Token test result')
    expect(within(result).getByText('Token is not valid.')).toBeInTheDocument()
    expect(within(result).getByText('audience does not match')).toBeInTheDocument()
    expect(within(result).getByText('Fail')).toBeInTheDocument()
    expect(within(result).getByText('Skipped')).toBeInTheDocument()

    expect(document.body.innerHTML).not.toContain('USERSIGNATURE')
    expect(JSON.stringify({ ...localStorage })).not.toContain('USERSIGNATURE')
    expect(JSON.stringify({ ...sessionStorage })).not.toContain('USERSIGNATURE')
    expect(window.location.href).not.toContain('USERSIGNATURE')
    // react-query keeps mutation variables; the token must not be among them
    expect(JSON.stringify(client.getMutationCache().getAll().map(m => m.state.variables))).not.toContain('USERSIGNATURE')
    expect(JSON.stringify(client.getQueryCache().getAll().map(q => q.queryKey))).not.toContain('USERSIGNATURE')
  })

  it('does not log the token', async () => {
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map(m => vi.spyOn(console, m).mockImplementation(() => {}))
    vi.mocked(api.verifyUserToken).mockRejectedValue(apiError('invalid request', 400))
    const box = await open()
    fireEvent.change(box, { target: { value: SECRET } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    expect(await screen.findByText(/Could not test the token: invalid request/)).toBeInTheDocument()
    expect(JSON.stringify(spies.flatMap(s => s.mock.calls))).not.toContain('USERSIGNATURE')
    spies.forEach(s => s.mockRestore())
  })

  it('shows the principal and the verified groups', async () => {
    vi.mocked(api.verifyUserToken).mockResolvedValue({ valid: true, checks: [checks[0]], groups_available: true, groups: ['eng', 'admins'], principal_id: 'user:pool-acme:abc', is_workforce: true, live_authentication: true })
    const box = await open()
    fireEvent.change(box, { target: { value: SECRET } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    const result = await screen.findByLabelText('Token test result')
    expect(within(result).getByText('user:pool-acme:abc')).toBeInTheDocument()
    expect(within(result).getByText('eng')).toBeInTheDocument()
    expect(within(result).getByText('admins')).toBeInTheDocument()
    expect(within(result).queryByText(/Groups unavailable/)).toBeNull()
  })

  it('says groups are unavailable, with the server reason, rather than showing an empty list', async () => {
    vi.mocked(api.verifyUserToken).mockResolvedValue({ valid: true, checks: [checks[0]], groups_available: false, groups_note: 'the pinned claim "groups" was absent', principal_id: 'user:pool-acme:abc', is_workforce: true, live_authentication: true })
    const box = await open()
    fireEvent.change(box, { target: { value: SECRET } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    const result = await screen.findByLabelText('Token test result')
    const note = within(result).getByRole('note')
    expect(note).toHaveTextContent('Groups unavailable.')
    expect(note).toHaveTextContent('the pinned claim "groups" was absent')
    expect(note).toHaveTextContent(/not the same as empty/)
    expect(within(result).queryByText(/empty group list/)).toBeNull()
  })

  it('distinguishes a verified empty group list from unavailable groups', async () => {
    vi.mocked(api.verifyUserToken).mockResolvedValue({ valid: true, checks: [checks[0]], groups_available: true, groups: [], principal_id: 'p', is_workforce: true, live_authentication: true })
    const box = await open()
    fireEvent.change(box, { target: { value: SECRET } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    expect(await screen.findByText(/empty group list/)).toBeInTheDocument()
    expect(screen.queryByText(/Groups unavailable/)).toBeNull()
  })

  it('tests against any provider when none is selected', async () => {
    vi.mocked(api.verifyUserToken).mockResolvedValue({ valid: false, checks, groups_available: false, is_workforce: false, live_authentication: true })
    const box = await open()
    fireEvent.change(screen.getByLabelText('Check against'), { target: { value: '' } })
    fireEvent.change(box, { target: { value: SECRET } })
    fireEvent.click(screen.getByRole('button', { name: 'Test token' }))
    await waitFor(() => expect(api.verifyUserToken).toHaveBeenCalledWith(SECRET, undefined))
  })
})
