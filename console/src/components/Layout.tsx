import { useState, useEffect } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'

const navItems = [
  { to: '/dashboard', label: 'Dashboard' },
  { to: '/mcplex', label: 'MCPlex' },
  { to: '/a2aplex', label: 'A2APlex' },
  { to: '/llmplex', label: 'LLMPlex' },
  { to: '/agents', label: 'Agents' },
  { to: '/permissions', label: 'Permissions' },
]

const routeLabels: Record<string, string> = {
  dashboard: 'Dashboard',
  mcplex: 'MCPlex',
  a2aplex: 'A2APlex',
  llmplex: 'LLMPlex',
  agents: 'Agents',
  permissions: 'Permissions',
  deploy: 'Deploy',
  instances: 'Instances',
}

function useDarkMode() {
  const [dark, setDark] = useState(() => {
    try {
      const stored = localStorage.getItem('aiplex_dark_mode')
      if (stored !== null) return stored === 'true'
      return window.matchMedia('(prefers-color-scheme: dark)').matches
    } catch {
      return false
    }
  })

  useEffect(() => {
    const root = document.documentElement
    if (dark) {
      root.classList.add('dark')
    } else {
      root.classList.remove('dark')
    }
    localStorage.setItem('aiplex_dark_mode', String(dark))
  }, [dark])

  return [dark, () => setDark((d) => !d)] as const
}

function Breadcrumbs() {
  const location = useLocation()
  const segments = location.pathname.split('/').filter(Boolean)

  if (segments.length === 0) return null

  const crumbs = segments.map((seg, i) => {
    const path = '/' + segments.slice(0, i + 1).join('/')
    const label = routeLabels[seg] ?? seg
    const isLast = i === segments.length - 1
    return { path, label, isLast }
  })

  return (
    <nav className="flex items-center text-sm text-gray-500 dark:text-gray-400 mb-4">
      {crumbs.map((crumb, i) => (
        <span key={crumb.path} className="flex items-center">
          {i > 0 && <span className="mx-2">/</span>}
          {crumb.isLast ? (
            <span className="text-gray-900 dark:text-white font-medium">{crumb.label}</span>
          ) : (
            <NavLink to={crumb.path} className="hover:text-gray-700 dark:hover:text-gray-200">
              {crumb.label}
            </NavLink>
          )}
        </span>
      ))}
    </nav>
  )
}

export default function Layout() {
  const { user, logout } = useAuth()
  const [dark, toggleDark] = useDarkMode()
  const navigate = useNavigate()

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  const initials = user?.email
    ? user.email.substring(0, 2).toUpperCase()
    : '??'

  return (
    <div className="min-h-screen flex bg-gray-50 dark:bg-gray-950">
      {/* Sidebar */}
      <nav className="w-56 bg-gray-900 text-white flex flex-col shrink-0">
        <div className="p-4 border-b border-gray-700">
          <h1 className="text-xl font-bold">AIPlex</h1>
          <p className="text-xs text-gray-400 mt-1">Unified AI Control Plane</p>
        </div>
        <div className="flex-1 py-4">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `block px-4 py-2 text-sm ${
                  isActive
                    ? 'bg-brand-600 text-white'
                    : 'text-gray-300 hover:bg-gray-800'
                }`
              }
            >
              {item.label}
            </NavLink>
          ))}
        </div>

        {/* User section */}
        <div className="border-t border-gray-700 p-4 space-y-3">
          <button
            onClick={toggleDark}
            className="flex items-center gap-2 text-sm text-gray-400 hover:text-white transition-colors w-full"
            title={dark ? 'Switch to light mode' : 'Switch to dark mode'}
          >
            {dark ? (
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                  d="M12 3v1m0 16v1m9-9h-1M4 12H3m15.364 6.364l-.707-.707M6.343 6.343l-.707-.707m12.728 0l-.707.707M6.343 17.657l-.707.707M16 12a4 4 0 11-8 0 4 4 0 018 0z" />
              </svg>
            ) : (
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                  d="M20.354 15.354A9 9 0 018.646 3.646 9.003 9.003 0 0012 21a9.003 9.003 0 008.354-5.646z" />
              </svg>
            )}
            <span>{dark ? 'Light mode' : 'Dark mode'}</span>
          </button>

          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-full bg-brand-600 flex items-center justify-center text-xs font-bold text-white shrink-0">
              {initials}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm text-white truncate">{user?.email ?? 'Unknown'}</p>
            </div>
          </div>

          <button
            onClick={handleLogout}
            className="w-full text-left text-sm text-gray-400 hover:text-red-400 transition-colors"
          >
            Sign out
          </button>
        </div>
      </nav>

      {/* Main content */}
      <main className="flex-1 p-6 overflow-auto">
        <Breadcrumbs />
        <Outlet />
      </main>
    </div>
  )
}
