// The one list of screens the side navigation and the command palette both show.
// To add a screen, add it here (and its route in App.tsx): nothing else lists screens.
export type Need = 'operator' | 'admin' | undefined
export interface NavItem { to: string; label: string; need?: Need }
/** `modules: true` marks where the enabled module screens (MCPlex, LLMPlex, ...) go. */
export type NavSpec = NavItem | { modules: true }

export const NAV_SPEC: readonly NavSpec[] = [
  { to: '/overview', label: 'Overview' },
  { to: '/dashboard', label: 'Dashboard', need: 'admin' },
  { modules: true },
  { to: '/usage', label: 'Usage' },
  { to: '/agents', label: 'Agents', need: 'operator' },
  { to: '/access-points', label: 'Access points' },
  { to: '/hooks', label: 'Hooks', need: 'operator' },
  { to: '/identity/users', label: 'User identity', need: 'operator' },
  { to: '/runs', label: 'Runs', need: 'operator' },
  { to: '/permissions', label: 'Permissions', need: 'admin' },
]
