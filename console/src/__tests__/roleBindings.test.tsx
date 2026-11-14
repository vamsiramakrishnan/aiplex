import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Permissions from '../pages/Permissions'
import { accessFrom } from '../hooks/useAccess'
import * as api from '../api/roleBindings'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), listInstances: vi.fn().mockResolvedValue([]) }))
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: null }), useOptionalAuth: () => null }))
let access = accessFrom(['admin'])
vi.mock('../hooks/useAccess', async (orig) => ({ ...(await orig<typeof import('../hooks/useAccess')>()), useAccess: () => access }))
vi.mock('../api/roleBindings', async (orig) => {
  const real = await orig<typeof import('../api/roleBindings')>()
  return { ...real, listRoleBindings: vi.fn(), listRoles: vi.fn(), createRoleBinding: vi.fn(), updateRoleBinding: vi.fn(), deleteRoleBinding: vi.fn() }
})

const roles: api.RoleInfo[] = [
  { role: 'admin', default_scopes: ['*'] }, { role: 'deployer', default_scopes: ['mcp:tools:deploy'] },
  { role: 'viewer', default_scopes: ['mcp:tools:read'] }, { role: 'agent', default_scopes: [] },
]
const ops: api.RoleBinding = { id: 'rb-1', group: 'ops', role: 'deployer', scopes: ['mcp:tools:deploy', 'llm:model:x'], description: 'On call', revision: 4, provider: 'https://idp.example' }
const err = (status: number, code: string, message: string) => Object.assign(new Error(message), { status, code })

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><Permissions /></QueryClientProvider>)
  fireEvent.click(screen.getByRole('tab', { name: 'Role bindings' }))
}
beforeEach(() => {
  access = accessFrom(['admin'])
  vi.resetAllMocks()
  vi.mocked(api.listRoleBindings).mockResolvedValue([ops])
  vi.mocked(api.listRoles).mockResolvedValue(roles)
})

describe('Role bindings tab', () => {
  it('lists bindings with group, role, source, scopes, description and revision', async () => {
    show()
    expect(screen.getByText('Loading role bindings…')).toBeInTheDocument()
    const row = (await screen.findByText('ops')).closest('tr')!
    for (const t of ['deployer', 'https://idp.example', 'mcp:tools:deploy', 'llm:model:x', 'On call', '4']) expect(within(row).getByText(t)).toBeInTheDocument()
  })

  it('shows an empty list as empty', async () => {
    vi.mocked(api.listRoleBindings).mockResolvedValue([])
    show()
    expect(await screen.findByText(/No role bindings/)).toBeInTheDocument()
  })

  it('shows a failed fetch as an error, never as an empty list', async () => {
    vi.mocked(api.listRoleBindings).mockRejectedValue(err(503, 'X', 'store down'))
    show()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load role bindings: store down')
    expect(screen.queryByText(/No role bindings/)).not.toBeInTheDocument()
  })

  it('says so when a refetch fails and the rows on screen are stale, and blocks writes on them', async () => {
    show()
    await screen.findByText('ops')
    vi.mocked(api.listRoleBindings).mockRejectedValue(err(500, 'X', 'boom'))
    fireEvent.click(screen.getByText('Reload'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/may be out of date/)
    expect(screen.getByText('ops')).toBeInTheDocument()
    expect(screen.getByLabelText('Edit ops deployer')).toBeDisabled()
    expect(screen.getByLabelText('Delete ops deployer')).toBeDisabled()
  })

  it('shows a missing revision and missing scopes as unknown, and will not write against them', async () => {
    vi.mocked(api.listRoleBindings).mockResolvedValue([{ id: 'rb-2', group: 'g', role: 'viewer', scopes: null }])
    show()
    const row = (await screen.findByText('g')).closest('tr')!
    expect(within(row).getAllByText('unknown')).toHaveLength(2)
    expect(within(row).queryByText('0')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Edit g viewer')).toBeDisabled()
    expect(screen.getByLabelText('Delete g viewer')).toBeDisabled()
  })

  it('creates with the role defaults by omitting scopes', async () => {
    vi.mocked(api.createRoleBinding).mockResolvedValue({ ...ops, id: 'rb-new' })
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByText('Add role binding'))
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: ' dev ' } })
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'viewer' } })
    expect(await screen.findByText(/Defaults for viewer: mcp:tools:read/)).toBeInTheDocument()
    fireEvent.click(screen.getByText('Create role binding'))
    await waitFor(() => expect(api.createRoleBinding).toHaveBeenCalledTimes(1))
    const sent = vi.mocked(api.createRoleBinding).mock.calls[0]![0]
    expect(sent).toMatchObject({ group: 'dev', role: 'viewer' })
    expect('scopes' in sent).toBe(false)
  })

  it('creates with an explicit empty list when told to grant none', async () => {
    vi.mocked(api.createRoleBinding).mockResolvedValue(ops)
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByText('Add role binding'))
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: 'dev' } })
    fireEvent.click(screen.getByLabelText(/Choose the scopes/))
    fireEvent.click(screen.getByLabelText(/mcp:tools:read/))
    expect(screen.getByText('This binding will grant no scopes.')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Create role binding'))
    await waitFor(() => expect(api.createRoleBinding).toHaveBeenCalledWith(expect.objectContaining({ group: 'dev', scopes: [] })))
  })

  it('reports an already existing binding on create', async () => {
    vi.mocked(api.createRoleBinding).mockRejectedValue(err(409, 'CONFLICT', 'role binding already exists'))
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByText('Add role binding'))
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: 'dev' } })
    fireEvent.click(screen.getByText('Create role binding'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/already exists/)
  })

  it('an edit that did not touch scopes sends the stored scopes and the revision read', async () => {
    vi.mocked(api.updateRoleBinding).mockResolvedValue({ ...ops, revision: 5 })
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    expect(screen.getByText(/Scopes are unchanged/)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Description, optional'), { target: { value: 'Pager rota' } })
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.updateRoleBinding).toHaveBeenCalledWith('rb-1', 4, {
      group: 'ops', role: 'deployer', provider: 'https://idp.example', pool_id: '', description: 'Pager rota', scopes: ['mcp:tools:deploy', 'llm:model:x'],
    }))
  })

  it('a role change resets scopes to the new role defaults, says so, and sends them explicitly', async () => {
    vi.mocked(api.updateRoleBinding).mockResolvedValue(ops)
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    await screen.findByLabelText(/mcp:tools:deploy/)
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'viewer' } })
    expect(screen.getByText(/Scopes were reset to the default scopes for viewer/)).toBeInTheDocument()
    expect(screen.getByLabelText(/mcp:tools:read/)).toBeChecked()
    expect(screen.queryByLabelText(/llm:model:x/)).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Add custom scope'), { target: { value: 'llm:model:y' } })
    fireEvent.click(screen.getByText('Add'))
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.updateRoleBinding).toHaveBeenLastCalledWith('rb-1', 4, expect.objectContaining({ scopes: ['mcp:tools:read', 'llm:model:y'] })))
  })

  it('changing the role back restores nothing but that role\'s defaults', async () => {
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'viewer' } })
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'deployer' } })
    expect(screen.getByLabelText(/mcp:tools:deploy/)).toBeChecked()
    expect(screen.queryByLabelText(/llm:model:x/)).not.toBeInTheDocument()
  })

  it('with the roles list unavailable a role change needs scopes stated before Save', async () => {
    vi.mocked(api.listRoles).mockRejectedValue(new Error('roles down'))
    vi.mocked(api.updateRoleBinding).mockResolvedValue(ops)
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'viewer' } })
    expect(screen.getByText(/default scopes for viewer are unavailable/)).toBeInTheDocument()
    expect(screen.getByText('Save changes')).toBeDisabled()
    fireEvent.click(screen.getByText('Grant no scopes'))
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.updateRoleBinding).toHaveBeenCalledWith('rb-1', 4, expect.objectContaining({ role: 'viewer', scopes: [] })))
  })

  it('refuses to save an edit whose stored scopes are unknown until they are stated', async () => {
    vi.mocked(api.listRoleBindings).mockResolvedValue([{ ...ops, scopes: null }])
    show(); await screen.findByText('ops')
    expect(screen.getByLabelText('Edit ops deployer')).toBeEnabled()
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    expect(screen.getByText(/stored scopes are unknown/i)).toBeInTheDocument()
    expect(screen.getByText('Save changes')).toBeDisabled()
    fireEvent.click(screen.getByLabelText(/mcp:tools:deploy/))
    expect(screen.getByText('Save changes')).toBeEnabled()
    vi.mocked(api.updateRoleBinding).mockResolvedValue(ops)
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.updateRoleBinding).toHaveBeenCalledWith('rb-1', 4, expect.objectContaining({ scopes: ['mcp:tools:deploy'] })))
  })

  it('can state "grant none" for unknown stored scopes, even with the roles list unavailable', async () => {
    vi.mocked(api.listRoleBindings).mockResolvedValue([{ ...ops, scopes: null }])
    vi.mocked(api.listRoles).mockRejectedValue(new Error('roles down'))
    vi.mocked(api.updateRoleBinding).mockResolvedValue(ops)
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    expect(screen.getByText('Save changes')).toBeDisabled()
    fireEvent.click(screen.getByText('Grant no scopes'))
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.updateRoleBinding).toHaveBeenCalledWith('rb-1', 4, expect.objectContaining({ scopes: [] })))
  })

  it('reports saving an edit of a binding that is gone', async () => {
    vi.mocked(api.updateRoleBinding).mockRejectedValue(err(404, 'NOT_FOUND', 'role binding not found'))
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer exists. Nothing was saved.')
    expect(screen.getByText('Save changes')).toBeDisabled()
  })

  it('reports deleting a binding that is already gone', async () => {
    vi.mocked(api.deleteRoleBinding).mockRejectedValue(err(404, 'NOT_FOUND', 'not found'))
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Delete ops deployer'))
    fireEvent.click(screen.getByText('Confirm delete'))
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer exists. Nothing was deleted.')
    expect(screen.getByText('Reload latest version')).toBeInTheDocument()
  })

  it('reports an edit conflict, overwrites nothing, and offers a reload', async () => {
    vi.mocked(api.updateRoleBinding).mockRejectedValue(err(412, 'REVISION_CONFLICT', 'configuration changed; reload before retrying'))
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    fireEvent.click(screen.getByText('Save changes'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/changed since this page read it. Nothing was saved/)
    expect(screen.getByText('Save changes')).toBeDisabled()
    expect(api.updateRoleBinding).toHaveBeenCalledTimes(1)
    vi.mocked(api.listRoleBindings).mockResolvedValue([{ ...ops, revision: 7, description: 'Newer' }])
    fireEvent.click(screen.getByText('Reload latest version'))
    expect(await screen.findByText('Newer')).toBeInTheDocument()
    expect(screen.queryByText('Save changes')).not.toBeInTheDocument()
  })

  it('deletes only after confirmation, with the revision read', async () => {
    vi.mocked(api.deleteRoleBinding).mockResolvedValue(undefined)
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Delete ops deployer'))
    expect(api.deleteRoleBinding).not.toHaveBeenCalled()
    fireEvent.click(screen.getByText('Cancel'))
    expect(api.deleteRoleBinding).not.toHaveBeenCalled()
    fireEvent.click(screen.getByLabelText('Delete ops deployer'))
    fireEvent.click(screen.getByText('Confirm delete'))
    await waitFor(() => expect(api.deleteRoleBinding).toHaveBeenCalledWith('rb-1', 4))
  })

  it('reports a delete conflict and deletes nothing', async () => {
    vi.mocked(api.deleteRoleBinding).mockRejectedValue(err(412, 'REVISION_CONFLICT', 'changed'))
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Delete ops deployer'))
    fireEvent.click(screen.getByText('Confirm delete'))
    expect(await screen.findByRole('alert')).toHaveTextContent(/Nothing was saved or deleted/)
    expect(screen.queryByText('Confirm delete')).not.toBeInTheDocument()
    expect(screen.getByText('Reload latest version')).toBeInTheDocument()
  })

  it('shows non-admins no write controls and does not fetch', () => {
    access = accessFrom(['viewer'])
    show()
    expect(screen.getByRole('status')).toHaveTextContent('You need the admin role for this.')
    for (const t of ['Add role binding', 'Edit', 'Delete']) expect(screen.queryByText(new RegExp(t))).not.toBeInTheDocument()
    expect(api.listRoleBindings).not.toHaveBeenCalled()
    expect(api.listRoles).not.toHaveBeenCalled()
  })
})

describe('Role binding requests', () => {
  it('update states every field, scopes included, and sends If-Match', async () => {
    const real = await vi.importActual<typeof import('../api/roleBindings')>('../api/roleBindings')
    const client = await import('../api/client')
    vi.mocked(client.authenticatedRequest).mockResolvedValue(ops)
    await real.updateRoleBinding('rb-1', 4, { group: 'ops', role: 'viewer', scopes: [] })
    expect(client.authenticatedRequest).toHaveBeenCalledWith('/api/v1/iam/role-bindings/rb-1', {
      method: 'PUT', headers: { 'If-Match': '4' },
      body: JSON.stringify({ group: 'ops', role: 'viewer', provider: '', pool_id: '', description: '', scopes: [] }),
    })
  })
  it('encodes the id in the path for update and delete', async () => {
    const real = await vi.importActual<typeof import('../api/roleBindings')>('../api/roleBindings')
    const client = await import('../api/client')
    vi.mocked(client.authenticatedRequest).mockResolvedValue(ops)
    await real.updateRoleBinding('a/b?c', 2, { group: 'g', role: 'viewer', scopes: [] })
    await real.deleteRoleBinding('a/b?c', 2)
    expect(vi.mocked(client.authenticatedRequest).mock.calls.map((c) => c[0])).toEqual(['/api/v1/iam/role-bindings/a%2Fb%3Fc', '/api/v1/iam/role-bindings/a%2Fb%3Fc'])
  })
  it('create omits scopes unless stated', async () => {
    const real = await vi.importActual<typeof import('../api/roleBindings')>('../api/roleBindings')
    const client = await import('../api/client')
    vi.mocked(client.authenticatedRequest).mockResolvedValue(ops)
    await real.createRoleBinding({ group: 'g', role: 'viewer' })
    expect(JSON.parse((vi.mocked(client.authenticatedRequest).mock.calls[0]![1] as RequestInit).body as string)).not.toHaveProperty('scopes')
  })
})

describe('Role change without further edits', () => {
  it('sends exactly the new role defaults', async () => {
    vi.mocked(api.updateRoleBinding).mockResolvedValue(ops)
    show(); await screen.findByText('ops')
    fireEvent.click(screen.getByLabelText('Edit ops deployer'))
    fireEvent.change(screen.getByLabelText('Role'), { target: { value: 'viewer' } })
    fireEvent.click(screen.getByText('Save changes'))
    await waitFor(() => expect(api.updateRoleBinding).toHaveBeenCalledWith('rb-1', 4, expect.objectContaining({ role: 'viewer', scopes: ['mcp:tools:read'] })))
  })
})
