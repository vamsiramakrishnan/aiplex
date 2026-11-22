// The one list of screens the side navigation and the command palette both show.
// To add a screen, add it here (and its route in App.tsx): nothing else lists screens.
export type Need = 'operator' | 'admin' | undefined
export const NAV_GROUPS = ['Workspace', 'Modules', 'Operations'] as const
export type NavGroup = typeof NAV_GROUPS[number]
export interface NavItem { to: string; label: string; group: NavGroup; need?: Need; purpose?: string }
/** `modules: true` marks where the enabled module screens (MCPlex, LLMPlex, ...) go. */
export type NavSpec = NavItem | { modules: true }

export const NAV_SPEC: readonly NavSpec[] = [
  { to: '/overview', label: 'Overview', group: 'Workspace' },
  { to: '/access-points', label: 'Access points', group: 'Workspace' },
  { to: '/usage', label: 'Usage', group: 'Workspace' },
  { modules: true },
  { to: '/agents', label: 'Agents', purpose: 'Caller identities', group: 'Operations', need: 'operator' },
  { to: '/identity/users', label: 'User identity', group: 'Operations', need: 'operator' },
  { to: '/hooks', label: 'Hooks', group: 'Operations', need: 'operator' },
  { to: '/runs', label: 'Runs', group: 'Operations', need: 'operator' },
  { to: '/dashboard', label: 'Dashboard', group: 'Operations', need: 'admin' },
  { to: '/permissions', label: 'Permissions', group: 'Operations', need: 'admin' },
]
