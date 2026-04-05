import { createContext, useContext, useState, useCallback, useMemo, type ReactNode } from 'react'

const TOKEN_KEY = 'aiplex_token'

interface AuthUser {
  sub: string
  email: string
  azp?: string
  scope?: string
}

interface AuthContextValue {
  isAuthenticated: boolean
  user: AuthUser | null
  token: string | null
  login: (token: string) => void
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

function decodeJwtPayload(token: string): AuthUser | null {
  try {
    const parts = token.split('.')
    if (parts.length !== 3) return null
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    return {
      sub: payload.sub ?? '',
      email: payload.email ?? payload.sub ?? '',
      azp: payload.azp,
      scope: payload.scope,
    }
  } catch {
    return null
  }
}

function getStoredToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(getStoredToken)

  const user = useMemo(() => (token ? decodeJwtPayload(token) : null), [token])
  const isAuthenticated = token !== null && user !== null

  const login = useCallback((newToken: string) => {
    const raw = newToken.startsWith('Bearer ') ? newToken.slice(7) : newToken
    localStorage.setItem(TOKEN_KEY, raw)
    setToken(raw)
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY)
    setToken(null)
  }, [])

  const value = useMemo<AuthContextValue>(
    () => ({ isAuthenticated, user, token, login, logout }),
    [isAuthenticated, user, token, login, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
