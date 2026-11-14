import { useModules } from '../hooks/useModules'
import { useAccess, type Access } from '../hooks/useAccess'
import type { ConsoleModule } from '../modules'
import { NAV_SPEC, type NavItem } from './navItems'

/** The full list: the static entries with the enabled modules in their slot. */
export function resolveNav(modules: readonly Pick<ConsoleModule, 'path' | 'label'>[]): NavItem[] {
  return NAV_SPEC.flatMap((spec): NavItem[] =>
    'modules' in spec ? modules.map((m) => ({ to: m.path, label: m.label, need: 'operator' as const })) : [spec])
}

/** An operator-only entry is hidden from everyone else. */
export const visibleTo = (item: NavItem, access: Pick<Access, 'isOperator'>) => item.need !== 'operator' || access.isOperator
/** An admin-only entry is shown to operators as unavailable; only an admin can open it. */
export const openableBy = (item: NavItem, access: Pick<Access, 'isOperator' | 'isAdmin'>) => visibleTo(item, access) && (item.need !== 'admin' || access.isAdmin)

/** Shared by the side navigation and the command palette so the two cannot drift. */
export function useNavigation() {
  const { enabled } = useModules()
  const access = useAccess()
  const items = resolveNav(enabled)
  const shown = items.filter((item) => visibleTo(item, access))
  const openable = shown.filter((item) => openableBy(item, access))
  return { items, shown, openable, hiddenForRole: items.length - shown.length, access }
}
