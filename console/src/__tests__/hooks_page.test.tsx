import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Hooks from '../pages/Hooks'
import { accessFrom } from '../hooks/useAccess'
import * as api from '../api/hooks'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn() }))
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: null }), useOptionalAuth: () => null }))
let access = accessFrom(['deployer'])
vi.mock('../hooks/useAccess', async (orig) => ({ ...(await orig<typeof import('../hooks/useAccess')>()), useAccess: () => access }))
vi.mock('../api/hooks', async (orig) => {
  const real = await orig<typeof import('../api/hooks')>()
  return { ...real, listHookPolicies: vi.fn(), putHookPolicy: vi.fn(), deleteHookPolicy: vi.fn(), testHookPolicy: vi.fn() }
})

const guard: api.HookPolicyRecord = {
  id: 'pii-guard', revision: 3,
  spec: { id: 'pii-guard', scope: { kind: 'access_point', id: 'ap-1' }, hooks: [{ id: 'redact', type: 'redact', phases: ['request', 'response'] }] },
}
const other: api.HookPolicyRecord = { id: 'size', revision: 1, spec: { id: 'size', scope: { kind: 'tenant' }, hooks: [{ id: 'cap', type: 'size_limit', phases: ['request'] }] } }
const err = (status: number, code: string, message: string, details?: unknown) => Object.assign(new Error(message), { status, code, details })

let client: QueryClient
function show() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><Hooks /></QueryClientProvider>)
}
beforeEach(() => {
  access = accessFrom(['deployer'])
  vi.resetAllMocks()
  localStorage.clear()
  vi.mocked(api.listHookPolicies).mockResolvedValue([guard, other])
})

describe('Hooks screen: list', () => {
  it('lists id, scope, hooks and revision, and states the limits', async () => {
    show()
    expect(screen.getByText('Loading hook policies…')).toBeInTheDocument()
    const row = (await screen.findByLabelText('Edit pii-guard')).closest('tr')!
    for (const t of ['access_point:ap-1', 'redact (redact: request+response)', '3']) expect(within(row).getByText(t)).toBeInTheDocument()
    expect(within(screen.getByLabelText('Limits')).getByText(/not part of QuotaPlex replay/)).toBeInTheDocument()
    expect(within(screen.getByLabelText('Limits')).getByText(/setting decides/)).toBeInTheDocument()
  })

  it('shows an empty list as empty', async () => {
    vi.mocked(api.listHookPolicies).mockResolvedValue([])
    show()
    expect(await screen.findByText(/No hook policies/)).toBeInTheDocument()
  })

  it('shows a null or missing list as an error, not as the empty state', async () => {
    const real = await vi.importActual<typeof import('../api/hooks')>('../api/hooks')
    vi.mocked(api.listHookPolicies).mockImplementation(real.listHookPolicies)
    const client = await import('../api/client')
    vi.mocked(client.authenticatedRequest).mockResolvedValue(null)
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load hook policies: The server sent no list of hook policies')
    expect(screen.queryByText(/No hook policies/)).not.toBeInTheDocument()
  })

  it('shows a failed first fetch as an error, never as an empty list', async () => {
    vi.mocked(api.listHookPolicies).mockRejectedValue(err(503, 'X', 'store down'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load hook policies: store down')
    expect(screen.queryByText(/No hook policies/)).not.toBeInTheDocument()
  })

  it('marks rows from an earlier fetch as possibly out of date after a failed refetch, and blocks writes on them', async () => {
    show()
    await screen.findByLabelText('Edit pii-guard')
    vi.mocked(api.listHookPolicies).mockRejectedValue(err(500, 'X', 'boom'))
    fireEvent.click(screen.getByText('Reload'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/may be out of date/)
    expect(screen.getByLabelText('Edit pii-guard')).toBeDisabled()
    expect(screen.getByLabelText('Delete pii-guard')).toBeDisabled()
  })

  it('shows missing revision, scope and a null hooks list as unknown, and will not write against them', async () => {
    vi.mocked(api.listHookPolicies).mockResolvedValue([{ id: 'odd', spec: { hooks: null } }])
    show()
    const row = (await screen.findByLabelText('Edit odd')).closest('tr')!
    expect(within(row).getAllByText('unknown')).toHaveLength(3)
    expect(within(row).queryByText('0')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Edit odd')).toBeDisabled()
    expect(screen.getByLabelText('Delete odd')).toBeDisabled()
  })

  it('shows a non-operator no policy and makes no request', () => {
    access = accessFrom(['viewer'])
    show()
    expect(screen.getByRole('status')).toHaveTextContent(/admin or deployer role/)
    expect(api.listHookPolicies).not.toHaveBeenCalled()
    expect(screen.queryByText('Add hook policy')).not.toBeInTheDocument()
  })
})

describe('Hooks screen: write', () => {
  it('creates a policy at revision 0 sending the document as written', async () => {
    vi.mocked(api.putHookPolicy).mockResolvedValue({ id: 'new', revision: 1 })
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByText('Add hook policy'))
    const doc = '{"id":"new","scope":{"kind":"tenant"},"hooks":[],"surprise":1}'
    fireEvent.change(screen.getByLabelText('Policy document (JSON)'), { target: { value: doc } })
    fireEvent.click(screen.getByText('Create hook policy'))
    await waitFor(() => expect(api.putHookPolicy).toHaveBeenCalledWith('new', 0, doc))
  })

  it('edits at the revision read and shows the server validation error verbatim beside the editor', async () => {
    const message = 'hooks[0].redact.rules[1].pattern: regex "(" does not compile:\n  missing closing )'
    vi.mocked(api.putHookPolicy).mockRejectedValue(err(400, 'INVALID_SPEC', message, { field: 'hooks[0].redact' }))
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByLabelText('Edit pii-guard'))
    expect(screen.getByLabelText('Policy document (JSON)')).toHaveValue(JSON.stringify(guard.spec, null, 2))
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.putHookPolicy).toHaveBeenCalledWith('pii-guard', 3, JSON.stringify(guard.spec, null, 2)))
    const alert = await screen.findByText(/does not compile/)
    expect(alert.textContent).toBe(`${message} (field: hooks[0].redact)`)
    expect(alert.closest('[aria-live]')).toContainElement(screen.getByText('Server validation'))
    // The document is still there to fix.
    expect(screen.getByLabelText('Policy document (JSON)')).not.toBeDisabled()
  })

  it('refuses a document that is not JSON or whose id differs from the policy being edited, without calling the server', async () => {
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByLabelText('Edit pii-guard'))
    const box = screen.getByLabelText('Policy document (JSON)')
    fireEvent.change(box, { target: { value: 'id: x' } })
    fireEvent.click(screen.getByText('Save changes'))
    const first = await screen.findByRole('alert')
    expect(first).toHaveTextContent('This looks like YAML')
    expect(first).toHaveTextContent('aiplex hooks policies set -f FILE')
    expect(first.textContent).not.toContain('id: x')
    fireEvent.change(box, { target: { value: 'not json at all SECRET' } })
    fireEvent.click(screen.getByText('Save changes'))
    const plain = await screen.findByText(/This is not valid JSON/)
    expect(plain).toHaveTextContent('aiplex hooks policies set -f FILE')
    expect(plain.textContent).not.toContain('SECRET')
    expect(screen.queryByText(/looks like YAML/)).not.toBeInTheDocument()
    fireEvent.change(box, { target: { value: '{"id":"someone-else"}' } })
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/differs from the policy being edited/)
    expect(api.putHookPolicy).not.toHaveBeenCalled()
  })

  it('shows a revision conflict, locks the editor and offers a reload', async () => {
    vi.mocked(api.putHookPolicy).mockRejectedValue(err(409, 'REVISION_CONFLICT', 'resource revision changed'))
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByLabelText('Edit pii-guard'))
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/changed since this page read it/)
    expect(screen.getByLabelText('Policy document (JSON)')).toBeDisabled()
    vi.mocked(api.listHookPolicies).mockClear()
    fireEvent.click(screen.getByText('Reload latest version'))
    await waitFor(() => expect(api.listHookPolicies).toHaveBeenCalled())
    expect(screen.queryByLabelText('Policy document (JSON)')).not.toBeInTheDocument()
  })

  it('says a create that collides with an existing id already exists, and leaves the text editable', async () => {
    vi.mocked(api.putHookPolicy).mockRejectedValue(err(409, 'REVISION_CONFLICT', 'resource revision changed'))
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByText('Add hook policy'))
    const doc = '{"id":"pii-guard","scope":{"kind":"tenant"},"hooks":[]}'
    fireEvent.change(screen.getByLabelText('Policy document (JSON)'), { target: { value: doc } })
    fireEvent.click(screen.getByText('Create hook policy'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('already exists')
    expect(alert.textContent).not.toMatch(/changed since this page read it/)
    expect(screen.getByLabelText('Policy document (JSON)')).not.toBeDisabled()
    expect(screen.getByLabelText('Policy document (JSON)')).toHaveValue(doc)
    expect(screen.getByText('Create hook policy')).not.toBeDisabled()
  })

  it('keeps no editor state from one policy to the next', async () => {
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByLabelText('Edit pii-guard'))
    fireEvent.change(screen.getByLabelText('Policy document (JSON)'), { target: { value: '{"id":"pii-guard","draft":true}' } })
    fireEvent.click(screen.getByLabelText('Edit size'))
    expect(screen.getByLabelText('Policy document (JSON)')).toHaveValue(JSON.stringify(other.spec, null, 2))
  })

  it('asks before deleting, then deletes at the revision read; cancel deletes nothing', async () => {
    vi.mocked(api.deleteHookPolicy).mockResolvedValue(undefined)
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByLabelText('Delete pii-guard'))
    expect(screen.getByText(/no longer inspected by its hooks/)).toBeInTheDocument()
    expect(api.deleteHookPolicy).not.toHaveBeenCalled()
    fireEvent.click(screen.getByText('Cancel'))
    expect(api.deleteHookPolicy).not.toHaveBeenCalled()
    fireEvent.click(screen.getByLabelText('Delete pii-guard'))
    fireEvent.click(screen.getByText('Confirm delete'))
    await waitFor(() => expect(api.deleteHookPolicy).toHaveBeenCalledWith('pii-guard', 3))
  })

  it('reports a delete conflict and offers a reload', async () => {
    vi.mocked(api.deleteHookPolicy).mockRejectedValue(err(409, 'REVISION_CONFLICT', 'resource revision changed'))
    show(); await screen.findByLabelText('Edit pii-guard')
    fireEvent.click(screen.getByLabelText('Delete pii-guard'))
    fireEvent.click(screen.getByText('Confirm delete'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/changed since this page read it/)
    expect(screen.getByText('Reload latest version')).toBeInTheDocument()
  })
})

describe('Hooks screen: dry run', () => {
  async function choose(id = 'pii-guard') {
    show(); await screen.findByLabelText(`Edit ${id}`)
    fireEvent.click(screen.getByLabelText(`Test ${id}`))
    fireEvent.change(screen.getByLabelText('Policy'), { target: { value: id } })
  }
  const run = (text: string) => {
    fireEvent.change(screen.getByLabelText('Sample text'), { target: { value: text } })
    fireEvent.click(screen.getByText('Run test'))
  }

  it('shows an allowed sample', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'allow', texts: ['hello'], results: [], uninspected_parts: 0 })
    await choose(); run('hello')
    await waitFor(() => expect(api.testHookPolicy).toHaveBeenCalledWith('pii-guard', { texts: ['hello'], phase: 'request', plane: 'llmplex' }))
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Allowed.')
    expect(result).toHaveTextContent('hello')
  })

  it('says an allow was reached only because a hook failed open, and names the hook', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'allow', texts: ['x'], uninspected_parts: 0, policy: 'pii-guard', phase: 'request',
      results: [{ id: 'sidecar', type: 'http', phase: 'request', decision: 'error', duration_ms: 2000, fail_open_used: true }, { id: 'cap', type: 'size_limit', phase: 'request', decision: 'allow', duration_ms: 1 }] })
    await choose(); run('x')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Allowed only because a hook failed open: sidecar.')
    expect(result).not.toHaveTextContent('The sample passed unchanged')
    const row = within(result).getByText('sidecar', { selector: 'td' }).closest('tr')!
    expect(row).toHaveTextContent('Failed open')
    expect(row).toHaveTextContent('2000 ms')
    expect(within(result).getByText('cap', { selector: 'td' }).closest('tr')).not.toHaveTextContent('Failed open')
  })

  it('says a plain allow passed unchanged when no hook failed open', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'allow', texts: ['x'], results: [{ id: 'cap', decision: 'allow' }, { id: 'later', decision: 'skipped' }] })
    await choose(); run('x')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Allowed. The sample passed unchanged.')
    expect(result).not.toHaveTextContent(/failed open/i)
    expect(within(result).getByText('later', { selector: 'td' }).closest('tr')).toHaveTextContent('This hook did not run.')
  })

  it('warns about a fail-open hook beside a redaction', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'transform', texts: ['[E]'], results: [
      { id: 'redact', type: 'redact', decision: 'transform', rules: [{ id: 'email', count: 1 }] },
      { id: 'sidecar', type: 'http', decision: 'error', fail_open_used: true }] })
    await choose(); run('a@b.c')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Redacted.')
    expect(result).toHaveTextContent('A hook failed open on this sample and its check did not apply: sidecar.')
  })

  it('shows an error that was not failed open as a hook that could not decide', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'deny', code: 'HOOK_ERROR', message: 'unavailable', results: [{ id: 'sidecar', type: 'http', decision: 'error' }] })
    await choose(); run('x')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('could not decide (error or timeout)')
    expect(result).not.toHaveTextContent(/failed open/i)
  })

  it('shows a redacted sample with the redacted text and the rule counts', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'transform', texts: ['mail [EMAIL]'], uninspected_parts: 0,
      results: [{ id: 'redact', type: 'redact', decision: 'transform', rules: [{ id: 'email', count: 2 }, { id: 'phone' }] }] })
    await choose(); run('mail jane@example.com')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Redacted.')
    expect(within(result).getByText('mail [EMAIL]')).toBeInTheDocument()
    expect(within(result).getByText('email: 2')).toBeInTheDocument()
    expect(within(result).getByText('phone: unknown')).toBeInTheDocument()
  })

  it('shows a refused sample with the reason and no output text', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'deny', code: 'HOOK_DENIED', message: 'The request contains a private key.', results: null, uninspected_parts: 1 })
    await choose(); run('-----BEGIN PRIVATE KEY-----')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Refused.')
    expect(result).toHaveTextContent('The request contains a private key. (HOOK_DENIED)')
    expect(result).not.toHaveTextContent('Output text')
    expect(result).toHaveTextContent('Parts not inspected: 1')
  })

  it('says unknown for a missing decision, output and count', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({})
    await choose(); run('x')
    const result = await screen.findByLabelText('Dry run result')
    expect(result).toHaveTextContent('Decision: unknown.')
    expect(result).toHaveTextContent('Output text: unknown')
    expect(result).toHaveTextContent('Parts not inspected: unknown')
  })

  it('shows a failed dry run as a failure without echoing the sample', async () => {
    vi.mocked(api.testHookPolicy).mockRejectedValue(err(409, 'INVALID_STORED_POLICY', 'the stored hook policy no longer validates; save it again'))
    await choose(); run('SECRET-VALUE')
    const alert = await screen.findByText(/Test failed/)
    expect(alert).toHaveTextContent('no longer validates')
    expect(alert.textContent).not.toContain('SECRET-VALUE')
  })

  it('sends the chosen phase and plane', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'allow', texts: ['x'] })
    await choose()
    fireEvent.change(screen.getByLabelText('Phase'), { target: { value: 'response' } })
    fireEvent.change(screen.getByLabelText('Plane'), { target: { value: 'mcplex' } })
    run('x')
    await waitFor(() => expect(api.testHookPolicy).toHaveBeenCalledWith('pii-guard', { texts: ['x'], phase: 'response', plane: 'mcplex' }))
  })

  it('keeps the sample and its result out of storage, the URL, the query cache and the console', async () => {
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}))
    const url = location.href
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'transform', texts: ['OUT-9f3'], results: [] })
    await choose(); run('SECRET-9f3')
    await screen.findByLabelText('Dry run result')
    const everything = () => JSON.stringify([{ ...localStorage }, { ...sessionStorage }, location.href, client.getQueryCache().getAll().map((q) => q.state), spies.map((s) => s.mock.calls)])
    expect(everything()).not.toMatch(/SECRET-9f3|OUT-9f3/)
    expect(location.href).toBe(url)
    // The mutation holds the sample only while observed.
    expect(JSON.stringify(client.getMutationCache().getAll().map((m) => m.state.variables))).toContain('SECRET-9f3')
  })

  it('clears the sample and result when another policy is chosen', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'allow', texts: ['SECRET-1'] })
    await choose(); run('SECRET-1')
    await screen.findByLabelText('Dry run result')
    fireEvent.change(screen.getByLabelText('Policy'), { target: { value: 'size' } })
    expect(screen.getByLabelText('Sample text')).toHaveValue('')
    expect(screen.queryByLabelText('Dry run result')).not.toBeInTheDocument()
    await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
  })

  it('clears the sample and drops the mutation when the screen is left', async () => {
    vi.mocked(api.testHookPolicy).mockResolvedValue({ decision: 'allow', texts: ['SECRET-2'] })
    const view = show()
    await screen.findByLabelText('Edit pii-guard')
    fireEvent.change(screen.getByLabelText('Policy'), { target: { value: 'pii-guard' } })
    run('SECRET-2')
    await screen.findByLabelText('Dry run result')
    view.unmount()
    await waitFor(() => expect(client.getMutationCache().getAll()).toHaveLength(0))
    expect(JSON.stringify(client.getQueryCache().getAll().map((q) => q.state))).not.toContain('SECRET-2')
  })
})

describe('Hooks API', () => {
  it('url-encodes the id in the path, and sends the revision and the document untouched', async () => {
    const real = await vi.importActual<typeof import('../api/hooks')>('../api/hooks')
    const client = await import('../api/client')
    const request = vi.mocked(client.authenticatedRequest).mockResolvedValue(undefined)
    await real.putHookPolicy('a/b c?d', 4, '{"x":1}')
    expect(request).toHaveBeenCalledWith('/api/v1/hooks/policies/a%2Fb%20c%3Fd', { method: 'PUT', headers: { 'If-Match': '4' }, body: '{"x":1}' })
    await real.deleteHookPolicy('a/b', 2)
    expect(request).toHaveBeenLastCalledWith('/api/v1/hooks/policies/a%2Fb', { method: 'DELETE', headers: { 'If-Match': '2' } })
    await real.testHookPolicy('a/b', { texts: ['t'], phase: 'request', plane: 'llmplex' })
    expect(request).toHaveBeenLastCalledWith('/api/v1/hooks/policies/a%2Fb/test', { method: 'POST', body: JSON.stringify({ texts: ['t'], phase: 'request', plane: 'llmplex' }) })
  })

  it('refuses a null list and a non-list, and keeps an empty array as empty', async () => {
    const real = await vi.importActual<typeof import('../api/hooks')>('../api/hooks')
    const client = await import('../api/client')
    vi.mocked(client.authenticatedRequest).mockResolvedValueOnce(null)
    await expect(real.listHookPolicies()).rejects.toThrow('The server sent no list of hook policies')
    vi.mocked(client.authenticatedRequest).mockResolvedValueOnce({})
    await expect(real.listHookPolicies()).rejects.toThrow('The server sent no list of hook policies')
    vi.mocked(client.authenticatedRequest).mockResolvedValueOnce([])
    expect(await real.listHookPolicies()).toEqual([])
  })
})
