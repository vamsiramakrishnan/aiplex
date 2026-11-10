import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import MCPlex from '../pages/MCPlex'
import * as api from '../api/toolAuth'
import { applySelection, isRevisionConflict, validateAuthValues, type ToolAuthCatalog, type ToolConnectionRecord } from '../api/toolAuth'

vi.mock('../api/client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/client')>()),
  getCatalog: vi.fn().mockResolvedValue({ templates: [], total: 0, page: 1, page_size: 20 }),
  listInstances: vi.fn().mockResolvedValue([]),
}))
vi.mock('../api/toolAuth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/toolAuth')>()),
  getToolAuthCatalog: vi.fn(),
  listToolConnections: vi.fn(),
  getToolConnection: vi.fn(),
  putToolConnection: vi.fn(),
}))

const catalog: ToolAuthCatalog = {
  default_type: 'oauth_user',
  default_behaviour: 'A connection without an auth block keeps its execution credential_mode.',
  secret_references: { pattern: '^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$', namespace: 'tenants/acme/secrets', note: 'References only.' },
  tools_policy: [],
  types: [
    { type: 'oauth_user', title: 'Delegated user OAuth', description: 'Per-user OAuth grant.', execution_credential_mode: 'delegated-oauth', fields: [] },
    { type: 'api_key', title: 'API key', description: 'Key in a header or query.', execution_credential_mode: 'connection-auth', fields: [
      { name: 'in', type: 'string', required: true, enum: ['header', 'query'], description: 'Where the key is sent.' },
      { name: 'name', type: 'string', required: true, description: 'Header or query parameter name.' },
      { name: 'secret_ref', type: 'string', required: true, secret_reference: true, description: 'Reference to the API key secret.' },
    ] },
    { type: 'oauth_client_credentials', title: 'OAuth client credentials', description: 'Service token.', execution_credential_mode: 'connection-auth', fields: [
      { name: 'client_secret_ref', type: 'string', required: true, secret_reference: true, description: 'Reference to the client secret.' },
      { name: 'scopes', type: 'string[]', description: 'Requested scopes.' },
      { name: 'catalog_only_field', type: 'string', description: 'Present only in this catalog.' },
    ] },
  ],
}

const execution = { id: 'search', endpoint: 'https://s.example/mcp', identity_ref: 'operator', credential_mode: 'operator-bearer', allowed_tools: ['files.read'], protocol_version: '2025-06-18', mode: 'stateless-json' }
const searchRecord: ToolConnectionRecord = { id: 'search', revision: 4, spec: { id: 'search', resource_url: 'https://s.example/mcp', network_profile_ref: 'egress', execution } }
const filesRecord: ToolConnectionRecord = { id: 'files', revision: 2, spec: { id: 'files', execution: { ...execution, id: 'files', identity_ref: '', credential_mode: 'connection-auth' },
  auth: { type: 'api_key', in: 'header', name: 'X-Api-Key', secret_ref: 'files-key' }, tools: { allow: ['files.*'], deny: ['files.delete'], read_only: true } } }
const bareRecord: ToolConnectionRecord = { id: 'bare', revision: 1, spec: { id: 'bare', resource_url: 'https://b.example/mcp' } }

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><MemoryRouter><MCPlex /></MemoryRouter></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'Tool connections' }))
}

beforeEach(() => {
  vi.mocked(api.getToolAuthCatalog).mockResolvedValue(catalog)
  vi.mocked(api.listToolConnections).mockResolvedValue([searchRecord, filesRecord, bareRecord])
})
afterEach(() => vi.resetAllMocks())

describe('MCPlex tool connection auth', () => {
  it('shows each connection with its current auth type and tool policy', async () => {
    show()
    const files = (await screen.findByText('files')).closest('tr')!
    expect(within(files).getByText('API key')).toBeInTheDocument()
    expect(within(files).getByText('allow 1 · deny 1 · read-only')).toBeInTheDocument()
    const search = screen.getByText('search').closest('tr')!
    expect(within(search).getByText('Default (operator identity)')).toBeInTheDocument()
    expect(within(search).getByText('No restrictions')).toBeInTheDocument()
  })

  it('handles loading, empty and error states', async () => {
    vi.mocked(api.listToolConnections).mockResolvedValue([])
    show()
    expect(screen.getByRole('status')).toHaveTextContent('Loading tool connections')
    expect(await screen.findByText('No tool connections yet.')).toBeInTheDocument()
  })

  it('shows a load failure instead of an empty list', async () => {
    vi.mocked(api.listToolConnections).mockRejectedValue(new Error('Tool read grant required'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Tool read grant required')
    expect(screen.queryByText('No tool connections yet.')).toBeNull()
  })

  it('renders fields from the catalog and labels secret fields as references', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit auth for search' }))
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'oauth_client_credentials' } })
    expect(screen.getByLabelText(/client_secret_ref \(secret reference\)/)).toBeInTheDocument()
    expect(screen.getByLabelText('catalog_only_field')).toBeInTheDocument()
    expect(screen.getByText(/not the secret value/)).toBeInTheDocument()
    expect(screen.queryByLabelText('in')).toBeNull()
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'api_key' } })
    expect(screen.getByLabelText(/^in/)).toHaveProperty('tagName', 'SELECT')
  })

  it('rejects a secret value typed into a reference field without saving', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit auth for search' }))
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'api_key' } })
    fireEvent.change(screen.getByLabelText(/^in/), { target: { value: 'header' } })
    fireEvent.change(screen.getByLabelText(/^name/), { target: { value: 'X-Api-Key' } })
    fireEvent.change(screen.getByLabelText(/secret_ref/), { target: { value: 'sk-live/abc 123' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/not the secret value/, { selector: '[role="alert"]' })).toBeInTheDocument()
    expect(api.putToolConnection).not.toHaveBeenCalled()
  })

  it('saves the auth block and tool policy with the loaded revision, keeping other fields', async () => {
    vi.mocked(api.putToolConnection).mockImplementation(async (id, spec, revision) => ({ id, revision: revision + 1, spec }))
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit auth for search' }))
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'api_key' } })
    fireEvent.change(screen.getByLabelText(/^in/), { target: { value: 'header' } })
    fireEvent.change(screen.getByLabelText(/^name/), { target: { value: 'X-Api-Key' } })
    fireEvent.change(screen.getByLabelText(/secret_ref/), { target: { value: 'search-key' } })
    fireEvent.change(screen.getByLabelText('Allowed tools'), { target: { value: 'files.*' } })
    fireEvent.change(screen.getByLabelText('Denied tools'), { target: { value: 'files.delete' } })
    fireEvent.click(screen.getByLabelText('Read-only'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.putToolConnection).toHaveBeenCalledTimes(1))
    const [id, spec, revision] = vi.mocked(api.putToolConnection).mock.calls[0]
    expect(id).toBe('search')
    expect(revision).toBe(4)
    expect(spec.auth).toEqual({ type: 'api_key', in: 'header', name: 'X-Api-Key', secret_ref: 'search-key' })
    expect(spec.tools).toEqual({ allow: ['files.*'], deny: ['files.delete'], read_only: true })
    expect(spec.execution).toMatchObject({ credential_mode: 'connection-auth', identity_ref: '', protocol_version: '2025-06-18' })
    expect(spec.network_profile_ref).toBe('egress')
    await waitFor(() => expect(screen.queryByRole('form')).toBeNull())
    const row = screen.getByText('search').closest('tr')!
    expect(within(row).getByText('API key')).toBeInTheDocument()
    expect(within(row).getByText('allow 1 · deny 1 · read-only')).toBeInTheDocument()
  })

  it('shows a revision conflict, keeps the editor from overwriting, and reloads on request', async () => {
    vi.mocked(api.putToolConnection).mockRejectedValue(Object.assign(new Error('resource revision changed'), { status: 409, code: 'REVISION_CONFLICT' }))
    const latest: ToolConnectionRecord = { ...searchRecord, revision: 5, spec: { ...searchRecord.spec, tools: { deny: ['files.delete'] } } }
    vi.mocked(api.getToolConnection).mockResolvedValue(latest)
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit auth for search' }))
    fireEvent.click(screen.getByLabelText('Read-only'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('This connection was changed by someone else.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(api.putToolConnection).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Reload latest' }))
    expect(await screen.findByText('Revision 5')).toBeInTheDocument()
    expect(screen.queryByText('This connection was changed by someone else.')).toBeNull()
    expect(screen.getByLabelText('Denied tools')).toHaveValue('files.delete')
    expect(screen.getByLabelText('Read-only')).not.toBeChecked()
    expect(api.putToolConnection).toHaveBeenCalledTimes(1)
  })

  it('shows other save errors without treating them as conflicts', async () => {
    vi.mocked(api.putToolConnection).mockRejectedValue(new Error('auth.secret_ref does not exist'))
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit auth for files' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/auth.secret_ref does not exist/)).toBeInTheDocument()
    expect(screen.queryByText('This connection was changed by someone else.')).toBeNull()
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  })

  it('limits connections without managed execution to delegated user OAuth and no tool policy', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Edit auth for bare' }))
    expect(screen.getByRole('option', { name: /API key/ })).toBeDisabled()
    expect(screen.getByRole('option', { name: /Delegated user OAuth/ })).toBeEnabled()
    expect(screen.getByLabelText('Allowed tools')).toBeDisabled()
  })
})

describe('tool auth helpers', () => {
  it('aligns the credential mode and removes empty policy', () => {
    const next = applySelection(filesRecord.spec, catalog, { authType: 'oauth_user', values: {}, allow: '', deny: '', readOnly: false })
    expect(next.auth).toEqual({ type: 'oauth_user' })
    expect(next.execution).toMatchObject({ credential_mode: 'delegated-oauth', identity_ref: '' })
    expect('tools' in next).toBe(false)
    expect(filesRecord.spec.auth?.type).toBe('api_key')
  })

  it('validates required, enum and reference fields from the catalog', () => {
    const type = catalog.types[1]
    expect(Object.keys(validateAuthValues(catalog, type, {}))).toEqual(['in', 'name', 'secret_ref'])
    expect(validateAuthValues(catalog, type, { in: 'header', name: 'X', secret_ref: 'ok-ref' })).toEqual({})
    expect(validateAuthValues(catalog, type, { in: 'cookie', name: 'X', secret_ref: 'a b' })).toHaveProperty('secret_ref')
  })

  it('recognises revision conflicts by status or code', () => {
    expect(isRevisionConflict(Object.assign(new Error('x'), { status: 409 }))).toBe(true)
    expect(isRevisionConflict(Object.assign(new Error('x'), { code: 'REVISION_CONFLICT' }))).toBe(true)
    expect(isRevisionConflict(new Error('x'))).toBe(false)
  })
})
