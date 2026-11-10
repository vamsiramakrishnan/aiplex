import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { listModules, type ModuleDescriptor } from '../api/client'
import { useAuth } from './useAuth'
import { consoleModules, enabledConsoleModules } from '../modules'

interface ModuleState {
  descriptors: ModuleDescriptor[]
  loading: boolean
  error: string | null
}

const ModuleContext = createContext<ModuleState | null>(null)

export function ModuleProvider({ children }: { children: ReactNode }) {
  const { user, token: bearer, isAuthenticated } = useAuth()
  const token = bearer ?? user?.sub ?? null
  const [state, setState] = useState<ModuleState>({ descriptors: [], loading: true, error: null })
  // Scope the snapshot to the authenticated session. Never retain a previous
  // user's module visibility during a credential change or failed discovery.
  const [owner, setOwner] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    setOwner(token)
    setState({ descriptors: [], loading: isAuthenticated, error: null })
    if (isAuthenticated) {
      listModules().then(
        (descriptors) => { if (active) setState({ descriptors, loading: false, error: null }) },
        (error: unknown) => { if (active) setState({ descriptors: [], loading: false, error: error instanceof Error ? error.message : 'Module discovery failed' }) },
      )
    }
    return () => { active = false }
  }, [token, isAuthenticated])

  const value = owner === token ? state : { descriptors: [], loading: isAuthenticated, error: null }
  return <ModuleContext.Provider value={value}>{children}</ModuleContext.Provider>
}

export function useModules() {
  const state = useContext(ModuleContext)
  if (!state) throw new Error('useModules must be used within ModuleProvider')
  return { ...state, enabled: enabledConsoleModules(state.descriptors) }
}

export function RequireModule({ name, children }: { name: string; children: ReactNode }) {
  const { enabled, loading, error } = useModules()
  if (loading) return <p role="status">Loading modules…</p>
  if (error) return <p role="alert">Module discovery unavailable: {error}</p>
  if (!enabled.some((module) => module.name === name)) return <div><h1 className="text-xl font-semibold mb-2">{consoleModules.find((m) => m.name === name)?.label ?? name}</h1><p role="alert">This module is not enabled on this Aiplex server.</p></div>
  return <>{children}</>
}
