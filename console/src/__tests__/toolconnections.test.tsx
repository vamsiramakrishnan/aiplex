import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ToolConnectionsPanel from '../components/ToolConnectionsPanel'
import { applySelection, checkSecretReference, looksLikeSecretValue, type ToolAuthCatalog, type ToolConnectionRecord } from '../api/toolAuth'
import { authenticatedRequest } from '../api/client'

vi.mock('../api/client', async (importOriginal) => ({ ...(await importOriginal<typeof import('../api/client')>()), authenticatedRequest: vi.fn() }))

const BASE = '/api/v1/mcp/connections'
const catalog: ToolAuthCatalog = {
  default_type: 'oauth_user',
  default_behaviour: 'Keeps its execution credential_mode.',
  secret_references: { pattern: '^[A-Za-z0-9][A-Za-z0-9_-]{0,199}$', namespace: 'tenants/acme/secrets', note: 'References only.' },
  tools_policy: [],
  types: [
    { type: 'none', title: 'No credential', description: 'No credential.', execution_credential_mode: 'connection-auth', fields: [] },
    { type: 'api_key', title: 'API key', description: 'Key in a header or query.', execution_credential_mode: 'connection-auth', fields: [
      { name: 'in', type: 'string', required: true, enum: ['header', 'query'], description: 'Where the key is sent.' },
      { name: 'name', type: 'string', required: true, description: 'Header or query parameter name.' },
      { name: 'prefix', type: 'string', description: 'Scheme before the key.' },
      { name: 'secret_ref', type: 'string', required: true, secret_reference: true, description: 'Reference to the API key secret.' },
    ] },
    { type: 'bearer', title: 'Bearer token', description: 'Bearer.', execution_credential_mode: 'connection-auth', fields: [
      { name: 'secret_ref', type: 'string', required: true, secret_reference: true, description: 'Reference to the token secret.' },
    ] },
    { type: 'basic', title: 'Basic', description: 'Basic.', execution_credential_mode: 'connection-auth', fields: [
      { name: 'username', type: 'string', required: true, description: 'User name.' },
      { name: 'secret_ref', type: 'string', required: true, secret_reference: true, description: 'Reference to the password secret.' },
    ] },
    { type: 'oauth_client_credentials', title: 'OAuth client credentials', description: 'Service token.', execution_credential_mode: 'connection-auth', fields: [
      { name: 'token_endpoint', type: 'string', required: true, description: 'Token endpoint.' },
      { name: 'client_id', type: 'string', required: true, description: 'Client id.' },
      { name: 'client_secret_ref', type: 'string', required: true, secret_reference: true, description: 'Reference to the client secret.' },
      { name: 'client_auth_method', type: 'string', enum: ['client_secret_basic', 'client_secret_post'], description: 'How the client authenticates.' },
      { name: 'scopes', type: 'string[]', description: 'Requested scopes.' },
    ] },
    { type: 'oauth_user', title: 'Delegated user OAuth', description: 'Per-user OAuth grant.', execution_credential_mode: 'delegated-oauth', fields: [] },
  ],
}

const execution = { id: 'search', endpoint: 'https://s.example/mcp', identity_ref: '', credential_mode: 'connection-auth', allowed_tools: ['files.read'], protocol_version: '2025-06-18', mode: 'stateless-json' }
const rule = { id: 'no-etc', effect: 'deny', when: "path_under(args.path, '/etc')", message: 'files under /etc are off limits' }
const search = (): ToolConnectionRecord => ({ id: 'search', revision: 4, spec: {
  id: 'search', resource_url: 'https://s.example/mcp', network_profile_ref: 'egress', future_member: { a: 1 }, execution,
  auth: { type: 'bearer', secret_ref: 'search-token', future_auth: 'keep' },
  tools: { allow: ['files.*'], future_policy: 'keep', rules: [rule, { id: 'eng-only', effect: 'allow', when: "caller.groups_known && 'eng' in caller.groups", future_rule: true }] } } })
const odd = (): ToolConnectionRecord => ({ id: 'a b?x', revision: 2, spec: { id: 'a b?x', resource_url: 'https://o.example/mcp' } })

const err = (status: number, message: string, code?: string) => Object.assign(new Error(message), { status, code })

let store: ToolConnectionRecord[]
let listFails = false
let overrides: Record<string, (init: RequestInit | undefined, path: string) => unknown> = {}
const req = vi.mocked(authenticatedRequest)

async function route(path: string, init?: RequestInit): Promise<unknown> {
  const method = init?.method ?? 'GET'
  const key = `${method} ${path}`
  for (const [k, fn] of Object.entries(overrides)) if (key === k || (k.endsWith('*') && key.startsWith(k.slice(0, -1)))) return fn(init, path)
  if (path === '/api/v1/mcp/auth-types') return catalog
  if (path === `${BASE}/`) { if (listFails) throw err(503, 'store down'); return structuredClone(store) }
  const m = path.match(/^\/api\/v1\/mcp\/connections\/([^/]+)$/)
  if (m) {
    const id = decodeURIComponent(m[1])
    const at = store.findIndex((r) => r.id === id)
    if (method === 'GET') { if (at < 0) throw err(404, 'not found'); return structuredClone(store[at]) }
    if (method === 'PUT') {
      const want = init?.headers && (init.headers as Record<string, string>)['If-Match']
      const spec = JSON.parse(String(init?.body))
      if (at < 0) { if (want !== '0') throw err(409, 'revision mismatch', 'REVISION_CONFLICT'); const r = { id, revision: 1, spec }; store.push(r); return structuredClone(r) }
      if (want !== String(store[at].revision)) throw err(409, 'resource revision changed', 'REVISION_CONFLICT')
      store[at] = { id, revision: store[at].revision + 1, spec }
      return structuredClone(store[at])
    }
    if (method === 'DELETE') { store = store.filter((r) => r.id !== id); return undefined }
  }
  throw new Error(`unexpected request ${key}`)
}

const calls = (method: string, pathPrefix = BASE) => req.mock.calls.filter(([p, i]) => (i?.method ?? 'GET') === method && p.startsWith(pathPrefix))
const writes = () => req.mock.calls.filter(([, i]) => i?.method === 'PUT' || i?.method === 'DELETE' || i?.method === 'POST')
const body = (c: typeof req.mock.calls[number]) => JSON.parse(String(c[1]?.body))
const header = (c: typeof req.mock.calls[number], name: string) => (c[1]?.headers as Record<string, string> | undefined)?.[name]

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><ToolConnectionsPanel /></QueryClientProvider>)
}
const row = (id: string) => screen.getByText(id).closest('tr')!

beforeEach(() => {
  store = [search(), odd()]
  listFails = false
  overrides = {}
  req.mockImplementation(route as never)
})
afterEach(() => vi.resetAllMocks())

describe('create a connection (part A)', () => {
  const openForm = async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }))
  }
  const fillBase = (id = 'docs') => {
    fireEvent.change(screen.getByLabelText(/^Connection id/), { target: { value: id } })
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://docs.example/mcp' } })
    fireEvent.change(screen.getByLabelText(/^Allowed tools/), { target: { value: 'search, fetch\nlookup' } })
  }

  it('creates a connection with a secret reference, checking the id is new and writing with If-Match 0', async () => {
    await openForm()
    fillBase()
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'bearer' } })
    fireEvent.change(screen.getByLabelText(/secret_ref/), { target: { value: 'docs-token' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(calls('GET', `${BASE}/docs`)).toHaveLength(1)
    const put = calls('PUT')[0]
    expect(put[0]).toBe(`${BASE}/docs`)
    expect(header(put, 'If-Match')).toBe('0')
    expect(body(put)).toEqual({
      id: 'docs', resource_url: 'https://docs.example/mcp',
      execution: { id: 'docs', endpoint: 'https://docs.example/mcp', credential_mode: 'connection-auth', allowed_tools: ['search', 'fetch', 'lookup'], protocol_version: '2025-06-18', mode: 'stateless-streamable-http' },
      auth: { type: 'bearer', secret_ref: 'docs-token' },
    })
    expect(await screen.findByText('docs')).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: 'New tool connection form' })).toBeNull()
  })

  it('offers fields from the catalog and says where references come from', async () => {
    await openForm()
    expect(screen.getByText(/never takes a secret value/)).toHaveTextContent('tenants/acme/secrets')
    expect(screen.getByText(/AIPLEX_TOOL_SECRET_/)).toBeInTheDocument()
    const types = within(screen.getByLabelText('Auth type')).getAllByRole('option').map((o) => o.getAttribute('value'))
    expect(types).toEqual(['none', 'api_key', 'bearer', 'basic', 'oauth_client_credentials'])
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'oauth_client_credentials' } })
    expect(screen.getByLabelText(/client_secret_ref \(secret reference\)/)).toBeInTheDocument()
    expect(screen.getByLabelText(/^client_auth_method/)).toHaveValue('client_secret_basic')
  })

  it('has no input that could take a secret value: every credential field is a labelled reference field and none is a password input', async () => {
    await openForm()
    for (const t of ['api_key', 'bearer', 'basic', 'oauth_client_credentials']) {
      fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: t } })
      expect(document.querySelectorAll('input[type="password"]')).toHaveLength(0)
      const secretFields = catalog.types.find((x) => x.type === t)!.fields.filter((f) => f.secret_reference)
      for (const f of secretFields) expect(screen.getByLabelText(new RegExp(`^${f.name} \\(secret reference\\)`))).toHaveAttribute('autocomplete', 'off')
      expect(screen.queryByLabelText(/^(password|token|secret|api_key|client_secret)\b/i)).toBeNull()
    }
  })

  const TOKEN_LIKE = ['sk-live-4f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c', 'ghp_abcdefghijklmnopqrstuvwxyz0123456789', 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2lnbmF0dXJl', 'my secret value', 'p@ss/word', 'AKIAABCDEFGHIJKLMNOP', 'AIzaSyA-abcdefghijklmnopqrstuvwxyz']
  const fieldCases: [string, string][] = [['api_key', 'secret_ref'], ['bearer', 'secret_ref'], ['basic', 'secret_ref'], ['oauth_client_credentials', 'client_secret_ref']]
  for (const [type, field] of fieldCases) {
    it(`refuses a pasted token in the ${type} ${field} field before any request is made`, async () => {
      await openForm()
      fillBase()
      fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: type } })
      if (type === 'api_key') { fireEvent.change(screen.getByLabelText(/^name/), { target: { value: 'X-Api-Key' } }) }
      if (type === 'basic') fireEvent.change(screen.getByLabelText(/^username/), { target: { value: 'svc' } })
      if (type === 'oauth_client_credentials') {
        fireEvent.change(screen.getByLabelText(/^token_endpoint/), { target: { value: 'https://idp.example/token' } })
        fireEvent.change(screen.getByLabelText(/^client_id/), { target: { value: 'svc' } })
      }
      for (const token of TOKEN_LIKE) {
        fireEvent.change(screen.getByLabelText(new RegExp(`^${field}`)), { target: { value: token } })
        fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
        expect(await screen.findByText(/not the secret value/, { selector: '[role="alert"]' })).toBeInTheDocument()
        expect(writes()).toHaveLength(0)
        expect(calls('GET', `${BASE}/docs`)).toHaveLength(0)
      }
      // A reference name is accepted.
      fireEvent.change(screen.getByLabelText(new RegExp(`^${field}`)), { target: { value: 'docs-token' } })
      fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
      await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    })
  }

  it('refuses a URL that carries credentials, and an id that cannot be a path segment', async () => {
    await openForm()
    fillBase('a/b')
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://user:hunter2@docs.example/mcp' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    expect(await screen.findByText(/id: Must not contain/)).toBeInTheDocument()
    expect(screen.getByText(/user name or password is refused/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://docs.example/mcp?api_key=abc' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    expect(await screen.findByText(/looks like a credential/)).toBeInTheDocument()
    expect(writes()).toHaveLength(0)
  })

  it('requires the allowed tools and the auth fields the catalog marks required', async () => {
    await openForm()
    fireEvent.change(screen.getByLabelText(/^Connection id/), { target: { value: 'docs' } })
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://docs.example/mcp' } })
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'api_key' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    expect(await screen.findByText(/tools: At least one tool is required/)).toBeInTheDocument()
    expect(screen.getByText(/secret_ref: Required/)).toBeInTheDocument()
    expect(writes()).toHaveLength(0)
  })

  it('refuses an id that already exists without writing', async () => {
    await openForm()
    fillBase('search')
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    expect(await screen.findByText(/Tool connection search already exists \(revision 4\)/)).toBeInTheDocument()
    expect(writes()).toHaveLength(0)
  })

  it('shows the server error for a reference that does not exist, unchanged', async () => {
    overrides['PUT /api/v1/mcp/connections/docs'] = () => { throw err(400, 'auth.secret_ref "docs-token" does not exist') }
    await openForm()
    fillBase()
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'bearer' } })
    fireEvent.change(screen.getByLabelText(/secret_ref/), { target: { value: 'docs-token' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    expect(await screen.findByText('Could not create the connection: auth.secret_ref "docs-token" does not exist')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create connection' })).toBeEnabled()
  })

  describe('paste a full specification', () => {
    const paste = async (spec: unknown) => {
      await openForm()
      fireEvent.click(screen.getByRole('button', { name: 'Paste a full specification' }))
      fireEvent.change(screen.getByLabelText('Full specification (JSON)'), { target: { value: typeof spec === 'string' ? spec : JSON.stringify(spec) } })
      fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    }
    const base = { id: 'user-oauth', resource_url: 'https://u.example/mcp', execution: { id: 'user-oauth', endpoint: 'https://u.example/mcp', credential_mode: 'delegated-oauth', allowed_tools: ['a'], protocol_version: '2025-06-18', mode: 'stateless-json' } }

    it('creates a connection of an auth type the form does not offer', async () => {
      await paste({ ...base, auth: { type: 'oauth_user' }, extra: { kept: true } })
      await waitFor(() => expect(calls('PUT')).toHaveLength(1))
      expect(body(calls('PUT')[0])).toMatchObject({ id: 'user-oauth', auth: { type: 'oauth_user' }, extra: { kept: true } })
      expect(header(calls('PUT')[0], 'If-Match')).toBe('0')
    })

    it.each([
      ['a token in a reference field', { ...base, auth: { type: 'bearer', secret_ref: 'sk-live-4f9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c' } }, /not the secret value/],
      ['a JWT in a reference field', { ...base, auth: { type: 'bearer', secret_ref: 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2ln' } }, /not the secret value/],
      ['a credential-named member', { ...base, auth: { type: 'bearer', secret_ref: 'ok', token: 'abc' } }, /credentials are secret references/],
      ['a password member', { ...base, auth: { type: 'basic', username: 'u', secret_ref: 'ok', password: 'hunter2' } }, /spec\.auth\.password/],
      ['an unknown auth member', { ...base, auth: { type: 'bearer', secret_ref: 'ok', value: 'abc' } }, /auth\.value: not a field of bearer/],
      ['an unknown auth type', { ...base, auth: { type: 'magic' } }, /auth\.type/],
      ['a bearer header value anywhere', { ...base, headers: { x: 'Bearer abcdefghijklmnop' } }, /shape of a credential/],
      ['a URL with credentials', { ...base, resource_url: 'https://u:p@u.example/mcp' }, /resource_url: A URL with a user name/],
    ])('refuses %s without sending anything', async (_name, spec, message) => {
      await paste(spec)
      expect(await screen.findByRole('alert')).toHaveTextContent(message)
      expect(writes()).toHaveLength(0)
      expect(calls('GET', `${BASE}/user-oauth`)).toHaveLength(0)
    })

    it('refuses text that is not a JSON object', async () => {
      await paste('[1]')
      expect(await screen.findByText('The specification must be a JSON object')).toBeInTheDocument()
      fireEvent.change(screen.getByLabelText('Full specification (JSON)'), { target: { value: '{nope' } })
      fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
      expect(await screen.findByText(/Not valid JSON/)).toBeInTheDocument()
      expect(writes()).toHaveLength(0)
    })
  })
})

describe('delete a connection (part A)', () => {
  it('asks first, shows the revision, then deletes with If-Match and an encoded id', async () => {
    show()
    await screen.findByText('a b?x')
    fireEvent.click(within(row('a b?x')).getByRole('button', { name: 'Delete a b?x' }))
    expect(screen.getByRole('heading', { name: /Delete tool connection/ })).toHaveTextContent('a b?x')
    expect(screen.getByText('Revision 2')).toBeInTheDocument()
    expect(calls('DELETE')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(calls('DELETE')).toHaveLength(1))
    const del = calls('DELETE')[0]
    expect(del[0]).toBe(`${BASE}/a%20b%3Fx`)
    expect(header(del, 'If-Match')).toBe('2')
    await waitFor(() => expect(screen.queryByText('a b?x')).toBeNull())
  })

  it('cancel deletes nothing', async () => {
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Delete search' }))
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(calls('DELETE')).toHaveLength(0)
    expect(screen.queryByRole('button', { name: 'Confirm delete' })).toBeNull()
  })

  it('shows a conflict, does not retry, and offers a reload', async () => {
    overrides['DELETE /api/v1/mcp/connections/search'] = () => { throw err(409, 'resource revision changed', 'REVISION_CONFLICT') }
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Delete search' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText('This connection was changed by someone else. Nothing was deleted.')).toBeInTheDocument()
    expect(calls('DELETE')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Confirm delete' })).toBeNull()
    store = [{ ...search(), revision: 5 }, odd()]
    fireEvent.click(screen.getByRole('button', { name: 'Reload latest version' }))
    expect(await screen.findByText('Revision 5')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Confirm delete' })).toBeEnabled()
    expect(calls('DELETE')).toHaveLength(1)
  })

  it('says so when the connection is already gone', async () => {
    overrides['DELETE /api/v1/mcp/connections/search'] = () => { throw err(404, 'not found') }
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Delete search' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText('This connection no longer exists. Nothing was deleted.')).toBeInTheDocument()
  })

  it('shows another failure verbatim', async () => {
    overrides['DELETE /api/v1/mcp/connections/search'] = () => { throw err(500, 'store down') }
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Delete search' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText('Could not delete: store down')).toBeInTheDocument()
  })

  it('does not allow a delete when the revision is unknown', async () => {
    store = [{ id: 'norev', spec: { id: 'norev' } } as unknown as ToolConnectionRecord]
    show()
    await screen.findByText('norev')
    expect(within(row('norev')).getByRole('button', { name: 'Delete norev' })).toBeDisabled()
  })
})

describe('list tools (part B)', () => {
  const open = async (id = 'search') => {
    show()
    await screen.findByText(id)
    fireEvent.click(within(row(id)).getByRole('button', { name: `List tools of ${id}` }))
    fireEvent.click(await screen.findByRole('button', { name: 'List tools' }))
  }

  it('calls tools/list through the gateway invoke route and shows the tools, with unknown read-only hints', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ jsonrpc: '2.0', id: 1, result: { tools: [
      { name: 'write_file', description: 'Writes a file', annotations: { readOnlyHint: false } },
      { name: 'read_file', title: 'Read', annotations: { readOnlyHint: true } },
      { name: 'mystery' },
    ] } })
    await open()
    const table = await screen.findByRole('table', { name: 'Tools callers can see on search' })
    const rows = within(table).getAllByRole('row').slice(1).map((r) => within(r).getAllByRole('cell').map((c) => c.textContent))
    expect(rows).toEqual([['mystery', 'not reported', 'unknown'], ['read_file', 'Read', 'true'], ['write_file', 'Writes a file', 'false']])
    const post = calls('POST')[0]
    expect(post[0]).toBe(`${BASE}/search/invoke`)
    expect(body(post)).toMatchObject({ jsonrpc: '2.0', method: 'tools/list' })
  })

  it('follows cursors', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = (init) => {
      const b = JSON.parse(String(init?.body))
      return b.params?.cursor === 'p2' ? { result: { tools: [{ name: 'b' }] } } : { result: { tools: [{ name: 'a' }], nextCursor: 'p2' } }
    }
    await open()
    expect(await screen.findByText('b')).toBeInTheDocument()
    expect(screen.getByText('a')).toBeInTheDocument()
    expect(calls('POST')).toHaveLength(2)
  })

  it('says when the policy hides every tool', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ result: { tools: [] } })
    await open()
    expect(await screen.findByText(/The server listed no tools\. The connection.s tools policy may be hiding them, or the server may have none/)).toBeInTheDocument()
  })

  it('shows the tool server error a client would hit', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ jsonrpc: '2.0', id: 1, error: { code: -32001, message: 'upstream rejected the credential' } })
    await open()
    expect(await screen.findByRole('alert')).toHaveTextContent('The tool server refused tools/list through the gateway: upstream rejected the credential')
  })

  it('shows a gateway failure and an HTTP error', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => { throw err(502, 'bad gateway: connection refused') }
    await open()
    expect(await screen.findByRole('alert')).toHaveTextContent('bad gateway: connection refused')
    expect(screen.getByRole('alert')).toHaveTextContent('could not get a valid answer')
  })

  it('shows a malformed answer as an error, not as an empty list', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ jsonrpc: '2.0', id: 1 })
    await open()
    expect(await screen.findByRole('alert')).toHaveTextContent('tools/list returned no result')
    expect(screen.queryByText(/The server listed no tools/)).toBeNull()
  })

  it('carries nothing over to another connection', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ result: { tools: [{ name: 'only_search' }] } })
    await open()
    expect(await screen.findByText('only_search')).toBeInTheDocument()
    fireEvent.click(within(row('a b?x')).getByRole('button', { name: 'List tools of a b?x' }))
    expect(screen.queryByText('only_search')).toBeNull()
  })
})

describe('policy rules (part C)', () => {
  const openRules = async () => {
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    return screen.findByRole('region', { name: 'Policy rules of search' }, { timeout: 2000 }).catch(() => screen.findByLabelText('Policy rules of search'))
  }
  const lastPut = () => calls('PUT').at(-1)!

  it('lists rules in evaluation order', async () => {
    const panel = await openRules()
    const rows = within(panel).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('no-etc')).toBeInTheDocument()
    expect(within(rows[0]).getByText("path_under(args.path, '/etc')")).toBeInTheDocument()
    expect(within(rows[1]).getByText('eng-only')).toBeInTheDocument()
  })

  it('adds a rule at a position, sending the whole connection with the revision and keeping fields it does not know', async () => {
    const panel = await openRules()
    fireEvent.change(within(panel).getByLabelText('Rule id'), { target: { value: 'no-force' } })
    fireEvent.change(within(panel).getByLabelText('Position'), { target: { value: 'eng-only' } })
    fireEvent.change(within(panel).getByLabelText('Expression (when)'), { target: { value: 'has(args.force) && args.force == true' } })
    fireEvent.change(within(panel).getByLabelText(/^Message to the caller/), { target: { value: 'no force' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Add rule' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    const put = lastPut()
    expect(put[0]).toBe(`${BASE}/search`)
    expect(header(put, 'If-Match')).toBe('4')
    const sent = body(put)
    expect(sent.tools.rules.map((r: { id: string }) => r.id)).toEqual(['no-etc', 'no-force', 'eng-only'])
    expect(sent.tools.rules[1]).toEqual({ id: 'no-force', effect: 'deny', when: 'has(args.force) && args.force == true', message: 'no force' })
    expect(sent.tools.rules[2].future_rule).toBe(true)
    expect(sent.tools.future_policy).toBe('keep')
    expect(sent.tools.allow).toEqual(['files.*'])
    expect(sent.future_member).toEqual({ a: 1 })
    expect(sent.auth.future_auth).toBe('keep')
    expect(await screen.findByText('Revision 5')).toBeInTheDocument()
    expect(within(row('search')).getByText('3')).toBeInTheDocument()
  })

  it('shows the server validation error verbatim and saves nothing', async () => {
    const message = 'tools.rules[no-force].when: ERROR: <input>:1:5: undeclared reference to \'arg\'\n | arg.force\n | ....^'
    overrides['PUT /api/v1/mcp/connections/search'] = () => { throw err(400, message) }
    const panel = await openRules()
    fireEvent.change(within(panel).getByLabelText('Rule id'), { target: { value: 'no-force' } })
    fireEvent.change(within(panel).getByLabelText('Expression (when)'), { target: { value: 'arg.force' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Add rule' }))
    const alert = await screen.findByText((_, el) => el?.tagName === 'PRE' && el.textContent === message)
    expect(alert).toBeInTheDocument()
    expect(within(panel).getAllByRole('row')).toHaveLength(3)
    // The draft is kept for correction.
    expect(within(panel).getByLabelText('Expression (when)')).toHaveValue('arg.force')
    expect(screen.queryByText('Revision 5')).toBeNull()
  })

  it('does not evaluate or pre-validate an expression: it is sent as typed', async () => {
    const panel = await openRules()
    fireEvent.change(within(panel).getByLabelText('Rule id'), { target: { value: 'bad' } })
    fireEvent.change(within(panel).getByLabelText('Expression (when)'), { target: { value: 'this is not ((( cel' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Add rule' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(lastPut()).tools.rules.at(-1).when).toBe('this is not ((( cel')
  })

  it('refuses a duplicate id unless replacing, and replaces in place of the old rule', async () => {
    const panel = await openRules()
    fireEvent.change(within(panel).getByLabelText('Rule id'), { target: { value: 'no-etc' } })
    fireEvent.change(within(panel).getByLabelText('Expression (when)'), { target: { value: "path_under(args.path, '/root')" } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Add rule' }))
    expect(await within(panel).findByText(/A rule no-etc already exists/)).toBeInTheDocument()
    expect(calls('PUT')).toHaveLength(0)
    fireEvent.click(within(panel).getByLabelText('Replace the rule with this id'))
    fireEvent.click(within(panel).getByRole('button', { name: 'Add rule' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(lastPut()).tools.rules.map((r: { id: string; when: string }) => [r.id, r.when])).toEqual([['eng-only', "caller.groups_known && 'eng' in caller.groups"], ['no-etc', "path_under(args.path, '/root')"]])
  })

  it('removes a rule only after confirmation', async () => {
    const panel = await openRules()
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule no-etc' }))
    expect(calls('PUT')).toHaveLength(0)
    expect(within(panel).getByText(/Calls it refused are no longer refused/)).toBeInTheDocument()
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(lastPut()).tools.rules.map((r: { id: string }) => r.id)).toEqual(['eng-only'])
    expect(header(lastPut(), 'If-Match')).toBe('4')
  })

  it('removing the last rule drops an otherwise empty tools block but keeps the other fields', async () => {
    store = [{ id: 'search', revision: 4, spec: { id: 'search', future: 1, tools: { rules: [rule] } } }]
    const panel = await openRules()
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule no-etc' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(lastPut())).toEqual({ id: 'search', future: 1 })
  })

  it('replaces the whole set after a review step, from a list or an object with rules', async () => {
    const panel = await openRules()
    fireEvent.click(within(panel).getByRole('button', { name: /Replace the whole set/ }))
    const text = within(panel).getByLabelText(/^Rules \(JSON list/)
    expect(JSON.parse((text as HTMLTextAreaElement).value)).toHaveLength(2)
    fireEvent.change(text, { target: { value: JSON.stringify({ rules: [{ id: 'only', effect: 'deny', when: 'true' }] }) } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Review replacement' }))
    expect(within(panel).getByText(/Replace all 2 rules of search with the 1 you entered/)).toBeInTheDocument()
    expect(calls('PUT')).toHaveLength(0)
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm replace' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(lastPut()).tools.rules).toEqual([{ id: 'only', effect: 'deny', when: 'true' }])
  })

  it('refuses a replacement that is not a list of rules, and an empty list removes all rules after confirmation', async () => {
    const panel = await openRules()
    fireEvent.click(within(panel).getByRole('button', { name: /Replace the whole set/ }))
    const text = within(panel).getByLabelText(/^Rules \(JSON list/)
    for (const bad of ['{"a":1}', '[1]', '{nope']) {
      fireEvent.change(text, { target: { value: bad } })
      fireEvent.click(within(panel).getByRole('button', { name: 'Review replacement' }))
      expect(await within(panel).findByRole('alert')).toBeInTheDocument()
    }
    expect(calls('PUT')).toHaveLength(0)
    fireEvent.change(text, { target: { value: '[]' } })
    fireEvent.click(within(panel).getByRole('button', { name: 'Review replacement' }))
    expect(within(panel).getByText(/an empty list removes every rule/)).toBeInTheDocument()
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm replace' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(lastPut()).tools).toEqual({ allow: ['files.*'], future_policy: 'keep' })
  })

  it('shows a revision conflict, saves nothing, and works on the latest version after a reload', async () => {
    store = [{ ...search(), revision: 5, spec: { ...search().spec, tools: { rules: [{ id: 'theirs', effect: 'deny', when: 'true' }] } } }]
    overrides['GET /api/v1/mcp/connections/'] = () => [search()]
    show()
    await screen.findByText('search')
    delete overrides['GET /api/v1/mcp/connections/']
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const panel = await screen.findByLabelText('Policy rules of search')
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule no-etc' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    expect(await within(panel).findByText('This connection was changed by someone else.')).toBeInTheDocument()
    expect(store[0].spec.tools?.rules).toHaveLength(1)
    fireEvent.click(within(panel).getByRole('button', { name: 'Reload latest version' }))
    expect(await within(panel).findByText('theirs')).toBeInTheDocument()
    expect(within(panel).queryByText('This connection was changed by someone else.')).toBeNull()
    expect(within(panel).getByText('Revision 5')).toBeInTheDocument()
  })

})

describe('rules dry run (part D)', () => {
  const openDryRun = async () => {
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    return within(await screen.findByRole('form', { name: 'Rules dry run' }))
  }
  const result = {
    connection: 'search', tool: 'read_file', decision: 'deny', error_class: 'tool_policy_denied', reason: 'tool_policy_denied', rule: 'no-etc', message: 'files under /etc are off limits',
    trace: [{ set: 'connection', id: 'no-etc', effect: 'deny', result: 'matched' }, { set: 'connection', id: 'eng-only', effect: 'allow', result: 'not_evaluated' }],
    list_hidden: false, note: 'Only rules are evaluated.',
  }

  it('posts the sample to the rules test route and shows which rule matched', async () => {
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => result
    const dry = await openDryRun()
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 'read_file' } })
    fireEvent.change(dry.getByLabelText(/^Sample arguments/), { target: { value: '{"path":"/etc/hosts"}' } })
    fireEvent.change(dry.getByLabelText('Caller kind'), { target: { value: 'agent' } })
    fireEvent.change(dry.getByLabelText(/^Caller groups/), { target: { value: 'eng, ops' } })
    fireEvent.change(dry.getByLabelText('destructive'), { target: { value: 'true' } })
    fireEvent.change(dry.getByLabelText(/^Access point/), { target: { value: 'team' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    await screen.findByRole('region', { name: 'Dry run result' })
    const post = calls('POST', `${BASE}/search/rules/test`)[0]
    expect(body(post)).toEqual({ tool: 'read_file', args: { path: '/etc/hosts' }, subject: { kind: 'agent', groups: ['eng', 'ops'] }, access_point: 'team', annotations: { destructive: true } })
    const res = within(screen.getByRole('region', { name: 'Dry run result' }))
    expect(res.getByText('deny', { selector: 'strong' })).toBeInTheDocument()
    expect(res.getByText(/tool_policy_denied, rule no-etc/)).toBeInTheDocument()
    expect(res.getByText('Caller sees: files under /etc are off limits')).toBeInTheDocument()
    const rows = res.getAllByRole('row').slice(1).map((r) => within(r).getAllByRole('cell').map((c) => c.textContent))
    expect(rows).toEqual([['connection', 'no-etc', 'deny', 'matched', '-'], ['connection', 'eng-only', 'allow', 'not_evaluated', '-']])
    expect(res.getByText('Only rules are evaluated.')).toBeInTheDocument()
  })

  it('sends groups_known false and candidate rules when asked, and shows an evaluator error per rule', async () => {
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => ({ ...result, decision: 'error', trace: [{ set: 'connection', id: 'x', effect: 'deny', result: 'error', error: 'no such key: path' }], list_hidden: true })
    const dry = await openDryRun()
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    fireEvent.click(dry.getByLabelText('Group membership unknown'))
    fireEvent.change(dry.getByLabelText(/^Candidate rules/), { target: { value: '[{"id":"x","effect":"deny","when":"args.path == 1"}]' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    const res = within(await screen.findByRole('region', { name: 'Dry run result' }))
    expect(body(calls('POST', `${BASE}/search/rules/test`)[0])).toEqual({ tool: 't', subject: { groups_known: false }, rules: [{ id: 'x', effect: 'deny', when: 'args.path == 1' }] })
    expect(res.getByText('no such key: path')).toBeInTheDocument()
    expect(res.getByText('tools/list hides this tool for this caller.')).toBeInTheDocument()
  })

  it('shows a server error unchanged and refuses unusable local input before sending', async () => {
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => { throw err(400, 'rules[0].when: could not compile') }
    const dry = await openDryRun()
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    expect(await dry.findByText('A tool name is required')).toBeInTheDocument()
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    fireEvent.change(dry.getByLabelText(/^Sample arguments/), { target: { value: '[1]' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    expect(await dry.findByText('The sample arguments must be a JSON object')).toBeInTheDocument()
    expect(calls('POST', `${BASE}/search/rules/test`)).toHaveLength(0)
    fireEvent.change(dry.getByLabelText(/^Sample arguments/), { target: { value: '' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    expect(await dry.findByText('rules[0].when: could not compile')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Dry run result' })).toBeNull()
  })
})

describe('list states and unknown fields', () => {
  it('shows a failed first fetch as an error, never as an empty list', async () => {
    listFails = true
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load tool connections: store down')
    expect(screen.queryByText('No tool connections yet.')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Add connection' })).toBeNull()
  })

  it('treats a null list as empty', async () => {
    overrides['GET /api/v1/mcp/connections/'] = () => null
    show()
    expect(await screen.findByText('No tool connections yet.')).toBeInTheDocument()
  })

  it('marks rows from an earlier fetch as possibly out of date after a failed reload, and blocks writes until it succeeds', async () => {
    show()
    await screen.findByText('search')
    listFails = true
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    expect(await screen.findByText(/may be out of date/)).toBeInTheDocument()
    expect(within(row('search')).getByRole('button', { name: 'Edit auth for search' })).toBeDisabled()
    expect(within(row('search')).getByRole('button', { name: 'Delete search' })).toBeDisabled()
    listFails = false
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    await waitFor(() => expect(within(row('search')).getByRole('button', { name: 'Delete search' })).toBeEnabled())
  })

  it('keeps members it does not know when the auth editor saves', async () => {
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Edit auth for search' }))
    fireEvent.change(screen.getByLabelText('Denied tools'), { target: { value: 'files.delete' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    const sent = body(calls('PUT')[0])
    expect(sent.future_member).toEqual({ a: 1 })
    expect(sent.auth).toEqual({ type: 'bearer', secret_ref: 'search-token', future_auth: 'keep' })
    expect(sent.tools.future_policy).toBe('keep')
    expect(sent.tools.deny).toEqual(['files.delete'])
    expect(sent.tools.rules).toHaveLength(2)
    expect(sent.tools.rules[1].future_rule).toBe(true)
  })

  it('applySelection keeps unknown tools members and rules', () => {
    const next = applySelection(search().spec, catalog, { authType: 'bearer', values: { secret_ref: 'search-token' }, allow: '', deny: '', readOnly: false })
    expect(next.tools).toMatchObject({ future_policy: 'keep' })
    expect(next.tools?.allow).toBeUndefined()
    expect(next.tools?.rules).toHaveLength(2)
  })
})

// ---- review round 1 ----

const PROD_TOKENS = ['sk-live-abcdefghijklmnopqrstuvwxyz', 'ghp_abcdefghijklmnopqrstuvwxyz0123456789', 'AIzaSyA-abcdefghijklmnopqrstuvwxyz', 'ya29.a0AfH6SMBabcdef', 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.c2ln', 'xoxb-123-456-abc', 'AKIAABCDEFGHIJKLMNOP']

describe('credential shapes (points 2 to 5)', () => {
  it('accepts every name the server pattern accepts, however long or mixed, and refuses the known shapes saying why', () => {
    for (const ok of ['github-token-prod', 'dev-tool-key', 'stripe_live_ref', 'crm0123456789abcdefghijklmnopqrstuv', 'a'.repeat(200)]) {
      expect(checkSecretReference(catalog, ok)).toBeUndefined()
    }
    for (const bad of PROD_TOKENS) {
      const why = checkSecretReference(catalog, bad)
      expect(why).toMatch(/shape of a credential/)
      expect(why).toMatch(/name of a stored secret, not the secret value/)
      expect(looksLikeSecretValue(bad)).toBe(true)
    }
    expect(checkSecretReference(catalog, 'has space')).toMatch(/not the secret value/)
  })

  it('still saves an existing connection whose reference is a long mixed name', async () => {
    store = [{ id: 'crm', revision: 1, spec: { id: 'crm', execution, auth: { type: 'bearer', secret_ref: 'crm0123456789abcdefghijklmnopqrstuv' } } }]
    show()
    await screen.findByText('crm')
    fireEvent.click(within(row('crm')).getByRole('button', { name: 'Edit auth for crm' }))
    fireEvent.click(screen.getByLabelText('Read-only'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(body(calls('PUT')[0]).auth.secret_ref).toBe('crm0123456789abcdefghijklmnopqrstuv')
  })

  it('applies one list to every reference field of the guided form', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }))
    fireEvent.change(screen.getByLabelText(/^Connection id/), { target: { value: 'docs' } })
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://docs.example/mcp' } })
    fireEvent.change(screen.getByLabelText(/^Allowed tools/), { target: { value: 'a' } })
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'bearer' } })
    for (const t of PROD_TOKENS) {
      fireEvent.change(screen.getByLabelText(/secret_ref/), { target: { value: t } })
      fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
      expect(await screen.findByText(/shape of a credential/, { selector: '[role="alert"]' })).toBeInTheDocument()
    }
    expect(writes()).toHaveLength(0)
  })

  describe('a pasted specification', () => {
    const base = { id: 'p', resource_url: 'https://u.example/mcp', execution: { id: 'p', endpoint: 'https://u.example/mcp', credential_mode: 'connection-auth', allowed_tools: ['a'], protocol_version: '2025-06-18', mode: 'stateless-json' } }
    const paste = (spec: unknown) => {
      show()
      return screen.findByRole('button', { name: 'Add connection' }).then((b) => {
        fireEvent.click(b)
        fireEvent.click(screen.getByRole('button', { name: 'Paste a full specification' }))
        fireEvent.change(screen.getByLabelText('Full specification (JSON)'), { target: { value: typeof spec === 'string' ? spec : JSON.stringify(spec) } })
        fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
      })
    }

    it.each(PROD_TOKENS)('refuses %s as a description, a nested value or a word in a sentence', async (token) => {
      await paste({ ...base, auth: { type: 'bearer', secret_ref: 'ok' }, description: token, nested: { list: [`key is ${token}`] } })
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('spec.description: this value has the shape of a credential')
      expect(alert).toHaveTextContent('spec.nested.list[0]')
      expect(pageText()).not.toContain(token)
      expect(writes()).toHaveLength(0)
    })

    it('refuses a member name that has the shape of a credential, without echoing it', async () => {
      const name = 'ghp_abcdefghijklmnopqrstuvwxyz0123456789'
      await paste({ ...base, auth: { type: 'bearer', secret_ref: 'ok', [name]: 'x' }, [`sk-live-abcdefghijklmnopqrstuvwxyz`]: 1 })
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('a member name has the shape of a credential')
      expect(pageText()).not.toContain(name)
      expect(pageText()).not.toContain('sk-live-abcdefghijklmnopqrstuvwxyz')
      expect(writes()).toHaveLength(0)
    })

    it('shows a fixed message for text that is not JSON and never quotes the input', async () => {
      const token = 'ghp_abcdefghijklmnopqrstuvwxyz0123456789'
      await paste(token)
      expect(await screen.findByText(/^Not valid JSON/)).toBeInTheDocument()
      expect(pageText()).not.toContain(token)
      expect(writes()).toHaveLength(0)
    })

    it('holds auth.token_endpoint and every other URL to the URL check', async () => {
      for (const url of ['https://client:secret@idp.example/token', 'https://idp.example/token?client_secret=abc', 'https://idp.example/token?api_key=abc', 'https://idp.example/token?token=abc', 'https://idp.example/token?key=abc', 'https://idp.example/token?password=abc']) {
        await paste({ ...base, auth: { type: 'oauth_client_credentials', token_endpoint: url, client_id: 'svc', client_secret_ref: 'ok' } })
        expect(await screen.findByRole('alert')).toHaveTextContent('auth.token_endpoint')
        expect(writes()).toHaveLength(0)
        cleanup()
      }
    })
  })

  it('holds the guided form token endpoint to the URL check', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }))
    fireEvent.change(screen.getByLabelText(/^Connection id/), { target: { value: 'docs' } })
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://docs.example/mcp' } })
    fireEvent.change(screen.getByLabelText(/^Allowed tools/), { target: { value: 'a' } })
    fireEvent.change(screen.getByLabelText('Auth type'), { target: { value: 'oauth_client_credentials' } })
    fireEvent.change(screen.getByLabelText(/^client_id/), { target: { value: 'svc' } })
    fireEvent.change(screen.getByLabelText(/^client_secret_ref/), { target: { value: 'ok-ref' } })
    for (const url of ['https://client:secret@idp.example/token', 'https://idp.example/token?client_secret=abc', 'https://idp.example/token?password=abc']) {
      fireEvent.change(screen.getByLabelText(/^token_endpoint/), { target: { value: url } })
      fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
      expect(await screen.findByText(/^token_endpoint: /, { selector: '[role="alert"]' })).toBeInTheDocument()
    }
    expect(writes()).toHaveLength(0)
  })

  it('does not quote a pasted token in the dry run arguments or the replacement rules', async () => {
    const token = 'ghp_abcdefghijklmnopqrstuvwxyz0123456789'
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const dry = within(await screen.findByRole('form', { name: 'Rules dry run' }))
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    fireEvent.change(dry.getByLabelText(/^Sample arguments/), { target: { value: token } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    expect(await dry.findByText(/^The sample arguments: Not valid JSON/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Replace the whole set/ }))
    fireEvent.change(screen.getByLabelText(/^Rules \(JSON list/), { target: { value: token } })
    fireEvent.click(screen.getByRole('button', { name: 'Review replacement' }))
    expect(await screen.findByText(/^Not valid JSON/)).toBeInTheDocument()
    expect(pageText()).not.toContain(token)
  })
})


describe('unknown is not empty (points 6 and 7)', () => {
  const open = async () => {
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'List tools of search' }))
    fireEvent.click(await screen.findByRole('button', { name: 'List tools' }))
  }
  it('treats an answer without a tool list as an error', async () => {
    for (const result of [{}, { tools: 'x' }, { tools: null }]) {
      overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ jsonrpc: '2.0', id: 1, result })
      await open()
      expect(await screen.findByRole('alert')).toHaveTextContent("The server's answer had no tool list")
      expect(screen.queryByText(/The server listed no tools/)).toBeNull()
      cleanup()
    }
  })
  it('keeps the real empty list as the empty state', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ result: { tools: [] } })
    await open()
    expect(await screen.findByText(/The server listed no tools/)).toBeInTheDocument()
  })
  it('says not reported for a missing trace and a missing list_hidden, and keeps the definite wording for a sent value', async () => {
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const dry = within(await screen.findByRole('form', { name: 'Rules dry run' }))
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => ({ connection: 'search', tool: 't', decision: 'allow' })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    const res = within(await screen.findByRole('region', { name: 'Dry run result' }))
    expect(res.getByText('Rule results: not reported.')).toBeInTheDocument()
    expect(res.getByText('Whether tools/list hides this tool: not reported.')).toBeInTheDocument()
    expect(res.queryByText('No rules to evaluate.')).toBeNull()
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => ({ connection: 'search', tool: 't', decision: 'allow', trace: [], list_hidden: false })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    await waitFor(() => expect(screen.getByText('No rules to evaluate.')).toBeInTheDocument())
    expect(screen.queryByText(/not reported/)).toBeNull()
  })
})

describe('codes and refused writes (points 9 to 11)', () => {
  it('shows the code beside the message on a failed delete, create, tools list and rules write', async () => {
    overrides['DELETE /api/v1/mcp/connections/search'] = () => { throw err(500, 'store down', 'STORE_DOWN') }
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => { throw err(502, 'bad gateway', 'UPSTREAM') }
    overrides['PUT /api/v1/mcp/connections/search'] = () => { throw err(400, 'tools.rules[x].when: bad', 'INVALID_SPEC') }
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'List tools of search' }))
    fireEvent.click(await screen.findByRole('button', { name: 'List tools' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('bad gateway (code UPSTREAM)')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const panel = await screen.findByLabelText('Policy rules of search')
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule no-etc' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    expect(await within(panel).findByText('tools.rules[x].when: bad')).toBeInTheDocument()
    expect(within(panel).getByText('Code: INVALID_SPEC')).toBeInTheDocument()
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Delete search' }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText('Could not delete: store down (code STORE_DOWN)')).toBeInTheDocument()
  })

  it('says nothing was saved when the create PUT is refused with a conflict, and keeps what was typed', async () => {
    overrides['PUT /api/v1/mcp/connections/docs'] = () => { throw err(409, 'resource revision changed', 'REVISION_CONFLICT') }
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }))
    fireEvent.change(screen.getByLabelText(/^Connection id/), { target: { value: 'docs' } })
    fireEvent.change(screen.getByLabelText(/^MCP server URL/), { target: { value: 'https://docs.example/mcp' } })
    fireEvent.change(screen.getByLabelText(/^Allowed tools/), { target: { value: 'a, b' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Nothing was saved')
    expect(alert).toHaveTextContent('exists now')
    expect(alert).toHaveTextContent('REVISION_CONFLICT')
    expect(screen.getByLabelText(/^Connection id/)).toHaveValue('docs')
    expect(screen.getByLabelText(/^Allowed tools/)).toHaveValue('a, b')
    expect(screen.getByRole('button', { name: 'Create connection' })).toBeEnabled()
  })

  it('disables Add connection while the list is stale, and Edit auth when the revision is unknown', async () => {
    store = [search(), { id: 'norev', spec: { id: 'norev' } } as unknown as ToolConnectionRecord]
    show()
    await screen.findByText('search')
    expect(within(row('norev')).getByRole('button', { name: 'Edit auth for norev' })).toBeDisabled()
    expect(within(row('search')).getByRole('button', { name: 'Edit auth for search' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Add connection' })).toBeEnabled()
    listFails = true
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    await screen.findByText(/may be out of date/)
    expect(screen.getByRole('button', { name: 'Add connection' })).toBeDisabled()
  })
})

describe('replacing a rule (points 12 and 13)', () => {
  const open = async () => {
    store = [{ ...search(), spec: { ...search().spec, tools: { rules: [
      { id: 'a', effect: 'deny', when: 'true', message: 'old', description: 'why', future_rule: { k: 1 } },
      { id: 'b', effect: 'allow', when: 'false' },
      { id: 'c', effect: 'deny', when: 'true' },
    ] } } }]
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    return within(await screen.findByLabelText('Policy rules of search'))
  }
  const sentIds = () => (body(calls('PUT').at(-1)!).tools.rules as { id: string }[]).map((r) => r.id)
  const fill = (panel: ReturnType<typeof within>, id: string, before = '') => {
    fireEvent.change(panel.getByLabelText('Rule id'), { target: { value: id } })
    fireEvent.change(panel.getByLabelText('Expression (when)'), { target: { value: 'args.x == 1' } })
    fireEvent.change(panel.getByLabelText('Position'), { target: { value: before } })
    fireEvent.click(panel.getByLabelText('Replace the rule with this id'))
    fireEvent.click(panel.getByRole('button', { name: 'Add rule' }))
  }

  it('keeps the members of the stored rule the Console does not model and clears only the optional fields the user emptied', async () => {
    const panel = await open()
    fill(panel, 'a')
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    const sent = body(calls('PUT')[0]).tools.rules.find((r: { id: string }) => r.id === 'a')
    expect(sent).toEqual({ id: 'a', effect: 'deny', when: 'args.x == 1', future_rule: { k: 1 } })
  })

  it('keeps the place of a rule replaced "before itself"', async () => {
    const panel = await open()
    fill(panel, 'b', 'b')
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(sentIds()).toEqual(['a', 'b', 'c'])
    expect(body(calls('PUT')[0]).tools.rules[1].when).toBe('args.x == 1')
  })

  it('resolves the position against the list as shown, whichever side the old rule was on', async () => {
    let panel = await open()
    fill(panel, 'c', 'a')
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(sentIds()).toEqual(['c', 'a', 'b'])
    cleanup()
    vi.mocked(authenticatedRequest).mockClear()
    panel = await open()
    fill(panel, 'a', 'c')
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    expect(sentIds()).toEqual(['b', 'a', 'c'])
  })
})

/** The text of the page outside the text boxes the user typed into. */
function pageText(): string {
  const copy = document.body.cloneNode(true) as HTMLElement
  copy.querySelectorAll("textarea,input").forEach((n) => n.remove())
  return copy.textContent ?? ""
}

describe('credential shapes never refuse prose (round 2)', () => {
  it('refuses a bearer scheme followed by something token-like, anywhere in the text', () => {
    expect(looksLikeSecretValue('Authorization: Bearer abcdefghijklmnop1234')).toBe(true)
    expect(looksLikeSecretValue('Bearer abcdefghijklmnop1234')).toBe(true)
    expect(looksLikeSecretValue('send "bearer QWxhZGRpbjpvcGVuIHNlc2FtZQ==" always')).toBe(true)
  })
  it.each(['Bearer tokens are issued by the IdP', 'bearer auth', 'uses a bearer token', 'Bearer token'])('accepts the prose %j', (text) => {
    expect(looksLikeSecretValue(text)).toBe(false)
  })
  it.each([
    'task-runner', 'desk-booking service', 'risk-score', 'ask-hr', 'pk-test', 'sk-short', 'AKIAN river tools', 'eyJ', 'version 1.2.3', 'a.b.c',
    'key=value', 'mode=fast;retries=3', 'region=eu-west-1, tier=gold',
  ])('accepts %j', (text) => {
    expect(looksLikeSecretValue(text)).toBe(false)
  })
  it('accepts the prose as a description of a pasted specification', async () => {
    show()
    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }))
    fireEvent.click(screen.getByRole('button', { name: 'Paste a full specification' }))
    const spec = { id: 'p', resource_url: 'https://u.example/mcp', description: 'Bearer tokens are issued by the IdP; uses a bearer token', execution: { id: 'p', endpoint: 'https://u.example/mcp', credential_mode: 'connection-auth', allowed_tools: ['a'], protocol_version: '2025-06-18', mode: 'stateless-json' }, auth: { type: 'bearer', secret_ref: 'ok-ref' } }
    fireEvent.change(screen.getByLabelText('Full specification (JSON)'), { target: { value: JSON.stringify(spec) } })
    fireEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
  })
})

describe('dry run errors (round 2)', () => {
  it('shows the server code beside the message of a failed dry run', async () => {
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => { throw err(400, 'rules[0].when: could not compile', 'INVALID_RULE') }
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const dry = within(await screen.findByRole('form', { name: 'Rules dry run' }))
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    expect(await dry.findByText('rules[0].when: could not compile')).toBeInTheDocument()
    expect(dry.getByText('Code: INVALID_RULE')).toBeInTheDocument()
  })
})

describe('round 3', () => {
  it('passes plainTextErrors only on the invoke route, never on the dry run or a rules write', async () => {
    overrides['POST /api/v1/mcp/connections/search/invoke'] = () => ({ result: { tools: [] } })
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => ({ connection: 'search', tool: 't', decision: 'allow', trace: [], list_hidden: false })
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'List tools of search' }))
    fireEvent.click(await screen.findByRole('button', { name: 'List tools' }))
    await screen.findByText(/The server listed no tools/)
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const panel = await screen.findByLabelText('Policy rules of search')
    const dry = within(await screen.findByRole('form', { name: 'Rules dry run' }))
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    await screen.findByRole('region', { name: 'Dry run result' })
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule no-etc' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    await waitFor(() => expect(calls('PUT')).toHaveLength(1))
    const opts = (m: string, p: string) => calls(m, p)[0][1] as { plainTextErrors?: boolean }
    expect(opts('POST', `${BASE}/search/invoke`).plainTextErrors).toBe(true)
    expect(opts('POST', `${BASE}/search/rules/test`).plainTextErrors).toBeUndefined()
    expect(opts('PUT', `${BASE}/search`).plainTextErrors).toBeUndefined()
  })

  it('falls back to the built-in reference shape for an empty, missing or invalid catalog pattern', () => {
    for (const pattern of ['', undefined, '(', '   ']) {
      const c = { ...catalog, secret_references: { ...catalog.secret_references, pattern } } as unknown as ToolAuthCatalog
      expect(checkSecretReference(c, 'has space/and slash')).toMatch(/not the secret value/)
      expect(checkSecretReference(c, 'ok-ref')).toBeUndefined()
    }
    expect(checkSecretReference({ ...catalog, secret_references: undefined } as unknown as ToolAuthCatalog, 'a b')).toMatch(/not the secret value/)
  })

  it('a reload clears the open removal confirmation and the failure banner', async () => {
    overrides['PUT /api/v1/mcp/connections/search'] = () => { throw err(500, 'store down', 'STORE_DOWN') }
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const panel = await screen.findByLabelText('Policy rules of search')
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule no-etc' }))
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    expect(await within(panel).findByText('store down')).toBeInTheDocument()
    fireEvent.click(within(panel).getByRole('button', { name: 'Remove rule eng-only' }))
    delete overrides['PUT /api/v1/mcp/connections/search']
    overrides['PUT /api/v1/mcp/connections/search'] = () => { throw err(409, 'resource revision changed', 'REVISION_CONFLICT') }
    fireEvent.click(within(panel).getByRole('button', { name: 'Confirm remove' }))
    const reloadButton = await within(panel).findByRole('button', { name: 'Reload latest version' })
    store[0].revision = 5
    fireEvent.click(reloadButton)
    await waitFor(() => expect(within(panel).queryByText('This connection was changed by someone else.')).toBeNull())
    expect(within(panel).queryByRole('group', { name: 'Confirm rule removal' })).toBeNull()
    expect(within(panel).queryByText('store down')).toBeNull()
  })

  it('clears the dry run result when the input changes or a new attempt fails local validation', async () => {
    overrides['POST /api/v1/mcp/connections/search/rules/test'] = () => ({ connection: 'search', tool: 't', decision: 'deny', trace: [], list_hidden: false })
    show()
    await screen.findByText('search')
    fireEvent.click(within(row('search')).getByRole('button', { name: 'Rules of search' }))
    const dry = within(await screen.findByRole('form', { name: 'Rules dry run' }))
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 't' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    await screen.findByRole('region', { name: 'Dry run result' })
    fireEvent.change(dry.getByLabelText('Tool name'), { target: { value: 'u' } })
    expect(screen.queryByRole('region', { name: 'Dry run result' })).toBeNull()
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    await screen.findByRole('region', { name: 'Dry run result' })
    fireEvent.change(dry.getByLabelText(/^Sample arguments/), { target: { value: '[1]' } })
    fireEvent.click(dry.getByRole('button', { name: 'Run dry run' }))
    expect(await dry.findByText('The sample arguments must be a JSON object')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Dry run result' })).toBeNull()
  })
})
