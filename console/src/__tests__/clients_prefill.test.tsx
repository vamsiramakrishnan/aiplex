import { fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ClientPlex from '../pages/ClientPlex'
import { authenticatedRequest } from '../api/client'
vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn() }))
const request = vi.mocked(authenticatedRequest)
const profiles = [
  { id: 'codex', name: 'Codex CLI', surface: 'cli', modes: ['direct', 'gateway'], protocols: ['openai-responses'] },
  { id: 'claude-code', name: 'Claude Code', surface: 'cli', modes: ['gateway'], protocols: ['anthropic-messages'] },
]
function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><ClientPlex /></QueryClientProvider>)
}
const editor = () => document.getElementById('client-registration') as HTMLTextAreaElement
const card = (name: string) => screen.getByText(name).closest('div') as HTMLElement
beforeEach(() => { request.mockReset(); request.mockImplementation(async (path) => (path.endsWith('/profiles') ? profiles : [])) })

describe('ClientPlex profile prefill', () => {
  it('fills the editor from the profile the API returned, not a hard-coded client', async () => {
    show()
    await screen.findByText('Claude Code')
    fireEvent.click(within(card('Claude Code')).getByRole('button', { name: 'Use this profile' }))
    const spec = JSON.parse(editor().value)
    expect(spec).toMatchObject({ id: 'developer-claude-code', profile_id: 'claude-code', mode: 'gateway', protocol: 'anthropic-messages' })
    expect(editor().value).not.toContain('openai-responses')
    expect(screen.getByText('New registration')).toBeInTheDocument()
  })

  it('prefills a gateway mode and its protocol, not direct mode', async () => {
    show()
    await screen.findByText('Codex CLI')
    fireEvent.click(within(card('Codex CLI')).getByRole('button', { name: 'Use this profile' }))
    expect(JSON.parse(editor().value)).toMatchObject({ profile_id: 'codex', mode: 'gateway', protocol: 'openai-responses' })
  })

  // Keys the server requires to save a registration (pkg/clientplex Registration.Validate), per mode.
  const gatewayKeys = ['id', 'profile_id', 'mode', 'protocol', 'principal_binding', 'auth_mode', 'destination_ref', 'credential_env', 'credential_ref']
  const claudeAppsKeys = ['id', 'profile_id', 'mode', 'protocol', 'principal_binding', 'auth_mode', 'destination_ref']
  it.each([
    { profile: { id: 'codex', name: 'Codex CLI', surface: 'cli', modes: ['direct', 'gateway'], protocols: ['openai-responses'] }, keys: gatewayKeys, mode: 'gateway', auth: 'gateway-key' },
    { profile: { id: 'claude-code', name: 'Claude Code', surface: 'cli', modes: ['direct', 'gateway', 'claude-apps-gateway'], protocols: ['anthropic-messages', 'vertex-anthropic', 'claude-apps'] }, keys: gatewayKeys, mode: 'gateway', auth: 'gateway-key' },
    { profile: { id: 'claude-desktop', name: 'Claude Desktop', surface: 'desktop', modes: ['claude-apps-gateway'], protocols: ['claude-apps'] }, keys: claudeAppsKeys, mode: 'claude-apps-gateway', auth: 'oidc' },
  ])('fills every required key for $profile.id and marks what the operator must decide', async ({ profile, keys, mode, auth }) => {
    request.mockImplementation(async (path) => (path.endsWith('/profiles') ? [profile] : []))
    show()
    await screen.findByText(profile.name)
    fireEvent.click(within(card(profile.name)).getByRole('button', { name: 'Use this profile' }))
    const spec = JSON.parse(editor().value) as Record<string, string>
    for (const key of keys) expect(spec, `missing ${key}`).toHaveProperty(key)
    expect(spec).toMatchObject({ mode, auth_mode: auth })
    for (const key of ['principal_binding', 'destination_ref', 'credential_env', 'credential_ref']) {
      if (key in spec) expect(spec[key]).toMatch(/replace/i)
    }
  })

  it('fills the direct Google Cloud keys for a direct-only profile', async () => {
    request.mockImplementation(async (path) => (path.endsWith('/profiles') ? [{ id: 'antigravity', name: 'Antigravity', surface: 'app-cli', modes: ['direct'], protocols: ['google-genai'] }] : []))
    show()
    await screen.findByText('Antigravity')
    fireEvent.click(within(card('Antigravity')).getByRole('button', { name: 'Use this profile' }))
    const spec = JSON.parse(editor().value) as Record<string, string>
    for (const key of ['id', 'profile_id', 'mode', 'protocol', 'principal_binding', 'auth_mode', 'project_id', 'location']) expect(spec, `missing ${key}`).toHaveProperty(key)
    expect(spec).toMatchObject({ mode: 'direct', auth_mode: 'adc', project_id: expect.stringMatching(/replace/) })
  })

  it('refuses to save a prefilled registration until its placeholders are replaced', async () => {
    show()
    await screen.findByText('Codex CLI')
    fireEvent.click(within(card('Codex CLI')).getByRole('button', { name: 'Use this profile' }))
    fireEvent.click(screen.getByText('Save registration'))
    expect(await screen.findByText(/Replace the placeholder values before saving: .*destination_ref/)).toBeInTheDocument()
    expect(request.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(false)
  })

  it('shows a loading state, then an empty state, in the registrations table', async () => {
    let finish!: (value: unknown[]) => void
    request.mockImplementation(async (path) => (path.endsWith('/profiles') ? profiles : new Promise((resolve) => { finish = resolve })))
    show()
    expect(await screen.findByText('Loading registrations…')).toBeInTheDocument()
    finish([])
    expect(await screen.findByText(/^No registrations yet\./)).toBeInTheDocument()
    expect(screen.queryByText('Loading registrations…')).not.toBeInTheDocument()
  })

  it('shows an error state in the registrations table when the list fails', async () => {
    request.mockImplementation(async (path) => { if (path.endsWith('/profiles')) return profiles; throw new Error('boom') })
    show()
    expect(await screen.findByText('Could not load registrations: boom')).toBeInTheDocument()
    expect(screen.queryByText(/^No registrations yet\./)).not.toBeInTheDocument()
  })
})
