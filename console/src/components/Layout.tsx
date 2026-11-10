import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth } from '../hooks/useAuth'
import { useModules } from '../hooks/useModules'
import { useAccess, ADMIN_REASON } from '../hooks/useAccess'
import { useFocusTrap } from '../hooks/useFocusTrap'
import { useDarkMode } from '../theme'
import logoReversed from '../brand/aiplex-lockup-horizontal-reversed.svg'

type Need = 'operator' | 'admin' | undefined
interface NavItem { to: string; label: string; need?: Need }

const routeLabels: Record<string, string> = {
  overview: 'Overview',
  dashboard: 'Dashboard',
  mcplex: 'MCPlex',
  a2aplex: 'A2APlex',
  llmplex: 'LLMPlex',
  skillsplex: 'SkillsPlex',
  clientplex: 'ClientPlex',
  quotaplex: 'QuotaPlex',
  usage: 'Usage',
  agents: 'Agents',
  'access-points': 'Access points',
  identity: 'Identity',
  users: 'User identity',
  runs: 'Runs',
  permissions: 'Permissions',
  deploy: 'Deploy',
  instances: 'Instances',
}

function Breadcrumbs() {
  const location = useLocation()
  const segments = location.pathname.split('/').filter(Boolean)
  if (segments.length === 0) return null
  const crumbs = segments.map((seg, i) => ({ path: '/' + segments.slice(0, i + 1).join('/'), label: routeLabels[seg] ?? decodeURIComponent(seg), isLast: i === segments.length - 1 }))
  return (
    <nav aria-label="Breadcrumb" className="flex flex-wrap items-center text-sm text-gray-600 dark:text-gray-400 mb-4">
      {crumbs.map((crumb, i) => (
        <span key={crumb.path} className="flex items-center">
          {i > 0 && <span className="mx-2" aria-hidden="true">/</span>}
          {crumb.isLast ? (
            <span aria-current="page" className="text-gray-900 dark:text-white font-medium">{crumb.label}</span>
          ) : (
            <NavLink to={crumb.path} className="hover:text-gray-900 dark:hover:text-gray-200 underline-offset-2 hover:underline">{crumb.label}</NavLink>
          )}
        </span>
      ))}
    </nav>
  )
}

const linkBase = 'block px-4 py-2 text-sm focus-visible:outline-offset-[-2px]'

export default function Layout() {
  const { user, logout, ambient } = useAuth()
  const { enabled, error } = useModules()
  const access = useAccess()
  const [dark, toggleDark] = useDarkMode()
  const [open, setOpen] = useState(false)
  const navigate = useNavigate()
  const location = useLocation()

  // A phone shows the navigation as a drawer: modal while open, closed after navigating.
  useEffect(() => { setOpen(false) }, [location.pathname])
  const drawerRef = useFocusTrap<HTMLElement>(open, () => setOpen(false))

  const items: NavItem[] = [
    { to: '/overview', label: 'Overview' },
    { to: '/dashboard', label: 'Dashboard', need: 'admin' },
    ...enabled.map((module) => ({ to: module.path, label: module.label, need: 'operator' as const })),
    { to: '/usage', label: 'Usage' },
    { to: '/agents', label: 'Agents', need: 'operator' },
    { to: '/access-points', label: 'Access points' },
    { to: '/identity/users', label: 'User identity', need: 'operator' },
    { to: '/runs', label: 'Runs', need: 'operator' },
    { to: '/permissions', label: 'Permissions', need: 'admin' },
  ]
  const shown = items.filter((item) => item.need !== 'operator' || access.isOperator)
  const hiddenForRole = items.length - shown.length

  function handleLogout() {
    logout()
    navigate('/login', { replace: true })
  }

  const who = user?.display_name || user?.email || 'Unknown'
  const initials = who.substring(0, 2).toUpperCase()

  return (
    <div className="min-h-screen md:flex">
      <a href="#main" className="sr-only focus:not-sr-only focus:fixed focus:left-2 focus:top-2 focus:z-50 focus:rounded focus:bg-brand-600 focus:px-3 focus:py-2 focus:text-brand-contrast">Skip to content</a>
      {/* Phone top bar */}
      <header className="md:hidden flex items-center justify-between bg-gray-900 text-white px-4 py-2 sticky top-0 z-30">
        <img src={logoReversed} alt="AIPlex" className="h-7 w-auto" />
        <button type="button" aria-expanded={open} aria-controls="primary-nav" onClick={() => setOpen((v) => !v)} className="rounded border border-gray-600 px-3 py-1.5 text-sm">
          {open ? 'Close menu' : 'Menu'}
        </button>
      </header>
      {open && <div className="md:hidden fixed inset-0 z-30 bg-black/50" aria-hidden="true" onClick={() => setOpen(false)} />}

      <aside
        id="primary-nav"
        ref={drawerRef}
        tabIndex={-1}
        aria-label="Primary"
        className={`${open ? 'flex fixed inset-y-0 left-0 z-40 w-64 overflow-y-auto' : 'hidden'} md:flex md:sticky md:top-0 md:h-screen md:w-56 md:z-auto bg-gray-900 text-white flex-col shrink-0`}
      >
        <div className="p-4 border-b border-gray-700">
          {/* The sidebar is always dark, so it uses the reversed lockup in both themes. */}
          <img src={logoReversed} alt="AIPlex" className="h-8 w-auto" />
          <p className="text-xs text-gray-300 mt-1">A control plane for agent interactions across models, tools, agents and skills</p>
        </div>
        <nav aria-label="Main" className="flex-1 py-4 overflow-y-auto">
          {shown.map((item) => {
            const blocked = item.need === 'admin' && !access.isAdmin
            if (blocked) {
              return (
                <span key={item.to} role="link" aria-disabled="true" title={ADMIN_REASON} className={`${linkBase} text-gray-300 cursor-not-allowed`}>
                  {item.label}<span className="block text-xs text-gray-300">Admin only</span>
                </span>
              )
            }
            return (
              <NavLink key={item.to} to={item.to} className={({ isActive }) => `${linkBase} ${isActive ? 'bg-brand-600 text-brand-contrast' : 'text-gray-200 hover:bg-gray-800'}`}>
                {item.label}
              </NavLink>
            )
          })}
          {hiddenForRole > 0 && <p className="px-4 pt-3 text-xs text-gray-300">Configuration screens need the admin or deployer role.</p>}
        </nav>

        <div className="border-t border-gray-700 p-4 space-y-3">
          <button type="button" onClick={toggleDark} className="flex items-center gap-2 text-sm text-gray-300 hover:text-white transition-colors w-full">
            <span aria-hidden="true">{dark ? '☀' : '☾'}</span>
            <span>{dark ? 'Light mode' : 'Dark mode'}</span>
          </button>
          <div className="flex items-center gap-3">
            <div aria-hidden="true" className="w-8 h-8 rounded-full bg-brand-600 flex items-center justify-center text-xs font-bold text-brand-contrast shrink-0">{initials}</div>
            <div className="flex-1 min-w-0">
              <p className="text-sm text-white truncate" title={user?.email}>{who}</p>
              <p className="text-xs text-gray-300 truncate">{access.roles.length ? access.roles.join(', ') : 'no role'}</p>
            </div>
          </div>
          {ambient
            ? <p className="text-xs text-gray-300">Signed in by this server.</p>
            : <button type="button" onClick={handleLogout} className="w-full text-left text-sm text-gray-300 hover:text-white underline-offset-2 hover:underline">Sign out</button>}
        </div>
      </aside>

      <main id="main" tabIndex={-1} className="flex-1 min-w-0 p-4 md:p-6 focus:outline-none">
        {error && <p role="alert" className="mb-4 text-status-denied-fg">Module discovery unavailable. Module navigation is hidden.</p>}
        <Breadcrumbs />
        <Outlet />
      </main>
    </div>
  )
}
