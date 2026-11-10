import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, renderHook, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AuthProvider, useAuth } from '../hooks/useAuth'
import { accessFrom } from '../hooks/useAccess'
import { ErrorNotice, RequireRole } from '../components/Forbidden'
import ShortId from '../components/ShortId'
import { accessPointView, isFullAccessPoint } from '../api/accessPoints'
import { ageText, isStaleOpen } from '../components/OpenBatches'

const principal = 'principal-' + 'b'.repeat(64)
const fetchMock = vi.fn()
const original = global.fetch
const who = (roles: string[] | null) => ({ ok: true, json: async () => ({ identity: { principal_id: principal, subject: 's', email: 'e@x', display_name: 'Eve' }, roles, scopes: ['llm:model:m'] }) })
const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={new QueryClient()}><AuthProvider>{children}</AuthProvider></QueryClientProvider>

beforeEach(() => { localStorage.clear(); global.fetch = fetchMock; fetchMock.mockReset() })
afterEach(() => { global.fetch = original })

describe('sign-in from the server identity', () => {
  it('uses a session the server provides without a stored token (the local development Console)', async () => {
    fetchMock.mockResolvedValue(who(['admin']))
    const hook = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(hook.result.current.isAuthenticated).toBe(true))
    expect(hook.result.current.ambient).toBe(true)
    expect(hook.result.current.token).toBeNull()
    expect(hook.result.current.user).toMatchObject({ display_name: 'Eve', roles: ['admin'] })
    // No credential header is invented.
    expect(fetchMock.mock.calls[0][1]).not.toHaveProperty('headers')
  })

  it('shows the sign-in form when the server does not know the browser, without an error message', async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 401 })
    const hook = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(hook.result.current.verifying).toBe(false))
    expect(hook.result.current.isAuthenticated).toBe(false)
    expect(hook.result.current.error).toBeNull()
  })

  it('falls back to the server session when a stored token is stale', async () => {
    localStorage.setItem('aiplex_token', 'stale')
    fetchMock.mockImplementation(async (_url: string, init?: { headers?: { Authorization?: string } }) => (init?.headers?.Authorization ? { ok: false, status: 401 } : who(['admin'])))
    const hook = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(hook.result.current.isAuthenticated).toBe(true))
    expect(hook.result.current.ambient).toBe(true)
    expect(localStorage.getItem('aiplex_token')).toBeNull()
  })

  it('treats a caller with null roles as having no role', async () => {
    fetchMock.mockResolvedValue(who(null))
    const hook = renderHook(() => useAuth(), { wrapper })
    await waitFor(() => expect(hook.result.current.isAuthenticated).toBe(true))
    expect(hook.result.current.user?.roles).toEqual([])
  })
})

describe('role helpers and guards', () => {
  it('maps roles to what the caller may do, with a reason when not', () => {
    expect(accessFrom(['admin']).isAdmin).toBe(true)
    expect(accessFrom(['deployer'])).toMatchObject({ isAdmin: false, isOperator: true })
    expect(accessFrom(['deployer']).reason('admin')).toMatch(/admin role/)
    expect(accessFrom(['deployer']).reason('operator')).toBeUndefined()
    expect(accessFrom([]).isOperator).toBe(false)
    expect(accessFrom([]).reason('operator')).toMatch(/admin or deployer/)
  })

  it('renders a clear state, not the screen, when the role is missing', async () => {
    fetchMock.mockResolvedValue(who([]))
    render(<MemoryRouter><AuthProvider><RequireRole need="admin" what="The dashboard"><p>secret</p></RequireRole></AuthProvider></MemoryRouter>, { wrapper: ({ children }) => <QueryClientProvider client={new QueryClient()}>{children}</QueryClientProvider> })
    expect(await screen.findByRole('alert', { name: 'Access denied' })).toHaveTextContent('You need the admin role for this')
    expect(screen.getByRole('alert')).toHaveTextContent('signed in without a role')
    expect(screen.queryByText('secret')).toBeNull()
  })

  it('reads a 403 as a missing role and anything else as the server message', () => {
    const { rerender } = render(<ErrorNotice error={Object.assign(new Error('insufficient role'), { status: 403 })} need="admin" what="usage prices" />)
    expect(screen.getByRole('alert')).toHaveTextContent('You need the admin role for this. (usage prices)')
    expect(screen.getByRole('alert')).not.toHaveTextContent('insufficient role')
    rerender(<ErrorNotice error={new Error('upstream down')} />)
    expect(screen.getByRole('alert')).toHaveTextContent('upstream down')
  })
})

describe('reduced access point views', () => {
  it('tells records from the {id, display_name, endpoints} view a non-operator gets', () => {
    const view = { id: 'local', display_name: 'Local', endpoints: [{ kind: 'models', url: 'http://x/ap/local/v1/models' }] }
    expect(isFullAccessPoint(view)).toBe(false)
    expect(accessPointView(view)).toEqual(view)
    const record = { id: 'a', revision: 2, spec: { id: 'a', display_name: 'A', caller_kinds: ['user' as const], subjects: {} } }
    expect(isFullAccessPoint(record)).toBe(true)
    expect(accessPointView(record).endpoints).toEqual([])
  })
})

describe('small display helpers', () => {
  it('shortens long identifiers but keeps the full value for assistive technology', () => {
    render(<ShortId value={principal} />)
    expect(screen.getByTitle(principal)).toHaveTextContent(principal)
    render(<ShortId value="tutor-agent" />)
    expect(screen.getByText('tutor-agent')).toBeInTheDocument()
  })

  it('flags open batches older than a day and says how old', () => {
    const now = Date.parse('2026-11-10T12:00:00Z')
    expect(isStaleOpen({ state: 'open', created_at: '2026-11-08T12:00:00Z' }, now)).toBe(true)
    expect(isStaleOpen({ state: 'open', created_at: '2026-11-10T08:00:00Z' }, now)).toBe(false)
    expect(isStaleOpen({ state: 'settled', created_at: '2026-11-01T00:00:00Z' }, now)).toBe(false)
    expect(isStaleOpen({ state: 'open' }, now)).toBe(false)
    expect(ageText('2026-11-08T12:00:00Z', now)).toBe('2 days')
    expect(ageText(undefined, now)).toBeUndefined()
  })
})
