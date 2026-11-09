import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { AuthProvider, useAuth } from '../hooks/useAuth'
const fetchMock = vi.fn()
const original = global.fetch
function setup() { const cache = new QueryClient(); const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={cache}><AuthProvider>{children}</AuthProvider></QueryClientProvider>; return { ...renderHook(() => useAuth(), { wrapper }), cache } }
beforeEach(() => { localStorage.clear(); global.fetch = fetchMock; fetchMock.mockReset() })
afterEach(() => { global.fetch = original })
describe('Verified Console sessions', () => {
  it('does not trust a decodable token when the server rejects its identity', async () => {
    localStorage.setItem('aiplex_token', 'header.eyJzdWIiOiJmb3JnZWQifQ.signature'); fetchMock.mockResolvedValue({ ok: false, status: 401 }); const hook = setup(); expect(hook.result.current.isAuthenticated).toBe(false)
    await waitFor(() => expect(hook.result.current.verifying).toBe(false)); expect(hook.result.current.isAuthenticated).toBe(false); expect(hook.result.current.error).toContain('server rejected'); expect(localStorage.getItem('aiplex_token')).toBeNull()
  })
  it('uses verified canonical IDs, clears session caches and ignores stale-token rejections', async () => {
    const id = 'principal-' + 'a'.repeat(64); fetchMock.mockResolvedValue({ ok: true, json: async () => ({ identity: { principal_id: id, subject: 'immutable-subject', email: 'display@example.com' }, scopes: ['llm:model:current'] }) }); const hook = setup(); hook.cache.setQueryData(['private-data'], 'old-session')
    await act(async () => { await hook.result.current.login('Bearer new-token') }); expect(hook.result.current.isAuthenticated).toBe(true); expect(hook.result.current.user?.sub).toBe(id); expect(hook.cache.getQueryData(['private-data'])).toBeUndefined(); expect(fetchMock).toHaveBeenCalledWith('/api/v1/iam/whoami', expect.objectContaining({ headers: { Authorization: 'Bearer new-token' } }))
    act(() => window.dispatchEvent(new CustomEvent('aiplex:session-invalid', { detail: { token: 'old-token' } }))); expect(hook.result.current.isAuthenticated).toBe(true)
    hook.cache.setQueryData(['private-data'], 'current-session'); act(() => window.dispatchEvent(new CustomEvent('aiplex:session-invalid', { detail: { token: 'new-token' } }))); expect(hook.result.current.isAuthenticated).toBe(false); expect(hook.cache.getQueryData(['private-data'])).toBeUndefined()
  })
})
