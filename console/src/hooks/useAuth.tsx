import { createContext, useContext, useState, useCallback, useEffect, useMemo, useRef, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { ResolvedAccess } from '../api/client'

const TOKEN_KEY = 'aiplex_token'
interface AuthUser { sub: string; email: string; azp?: string; scope?: string }
interface AuthContextValue {
  isAuthenticated: boolean
  verifying: boolean
  error: string | null
  user: AuthUser | null
  token: string | null
  login: (token: string) => Promise<void>
  logout: () => void
}
const AuthContext = createContext<AuthContextValue | null>(null)
function storedToken(): string | null { try { return localStorage.getItem(TOKEN_KEY) } catch { return null } }

// Use the server-verified canonical identity, rather than trusting JWT decoding.
async function verifyToken(token: string, signal?: AbortSignal): Promise<AuthUser> {
  const response = await fetch('/api/v1/iam/whoami', { headers: { Authorization: `Bearer ${token}` }, signal })
  if (!response.ok) throw new Error(response.status === 401 ? 'The server rejected this credential. Sign in with a valid identity.' : 'Identity verification unavailable. Check your server access and configuration.')
  const access = await response.json() as ResolvedAccess
  if (!/^principal-[0-9a-f]{64}$/.test(access.identity?.principal_id ?? '') || !Array.isArray(access.scopes)) throw new Error('The server returned an invalid verified identity.')
  return { sub: access.identity.principal_id, email: access.identity.email || access.identity.subject || access.identity.principal_id, azp: access.identity.authorized_party, scope: access.scopes.join(' ') }
}
export function AuthProvider({ children }: { children: ReactNode }) {
  const cache = useQueryClient()
  const initialToken = useRef(storedToken())
  const generation = useRef(0)
  const activeToken = useRef<string | null>(null)
  const [session, setSession] = useState<{ token: string | null; user: AuthUser | null; verifying: boolean; error: string | null }>({ token: null, user: null, verifying: !!initialToken.current, error: null })
  const clear = useCallback(() => { activeToken.current = null; try { localStorage.removeItem(TOKEN_KEY) } catch { /* storage may be disabled */ }; cache.clear() }, [cache])
  const logout = useCallback(() => { generation.current++; clear(); setSession({ token: null, user: null, verifying: false, error: null }) }, [clear])
  const login = useCallback(async (newToken: string) => {
    const raw = newToken.trim().replace(/^Bearer\s+/i, '')
    const current = ++generation.current
    clear()
    setSession({ token: null, user: null, verifying: true, error: null })
    try {
      if (!raw || /\s/.test(raw)) throw new Error('Enter a bearer credential without whitespace.')
      const user = await verifyToken(raw)
      if (generation.current !== current) throw new Error('Sign in was canceled.')
      localStorage.setItem(TOKEN_KEY, raw)
      activeToken.current = raw
      setSession({ token: raw, user, verifying: false, error: null })
    } catch (error) {
      if (generation.current === current) setSession({ token: null, user: null, verifying: false, error: error instanceof Error ? error.message : 'Identity verification failed.' })
      throw error
    }
  }, [clear])
  useEffect(() => {
    if (!initialToken.current) return
    const controller = new AbortController()
    const current = ++generation.current
    clear()
    setSession({ token: null, user: null, verifying: true, error: null })
    verifyToken(initialToken.current, controller.signal).then((user) => {
      if (generation.current !== current || controller.signal.aborted) return
      try { localStorage.setItem(TOKEN_KEY, initialToken.current!) } catch { setSession({ token: null, user: null, verifying: false, error: 'Browser credential storage is unavailable.' }); return }
      activeToken.current = initialToken.current
      setSession({ token: initialToken.current, user, verifying: false, error: null })
    }, (error: unknown) => {
      if (generation.current !== current || controller.signal.aborted) return
      setSession({ token: null, user: null, verifying: false, error: error instanceof Error ? error.message : 'Identity verification failed.' })
    })
    return () => { controller.abort() }
  }, [clear])
  useEffect(() => { const invalid = (event: Event) => { const rejected = (event as CustomEvent<{ token: string | null }>).detail?.token; if (activeToken.current && rejected === activeToken.current) logout() }; window.addEventListener('aiplex:session-invalid', invalid); return () => window.removeEventListener('aiplex:session-invalid', invalid) }, [logout])
  const value = useMemo<AuthContextValue>(() => ({ ...session, isAuthenticated: !!session.token && !!session.user, login, logout }), [session, login, logout])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
export function useAuth(): AuthContextValue { const ctx = useContext(AuthContext); if (!ctx) throw new Error('useAuth must be used within AuthProvider'); return ctx }
