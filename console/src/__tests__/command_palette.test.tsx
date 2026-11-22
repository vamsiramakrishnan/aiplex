import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Layout from '../components/Layout'
import CommandPalette from '../components/CommandPalette'
import { accessFrom } from '../hooks/useAccess'
import { consoleModules } from '../modules'

// Three entries added to the one navigation list, with no edit to the palette or to Layout.
vi.mock('../components/navItems', async (orig) => {
  const real = await orig<typeof import('../components/navItems')>()
  return { ...real, NAV_SPEC: [
    ...real.NAV_SPEC,
    { to: '/newly-added', label: 'Newly added', group: 'Workspace' },
    { to: '/ops-new', label: 'Ops new', group: 'Operations', need: 'operator' },
    { to: '/admin-new', label: 'Admin new', group: 'Operations', need: 'admin' },
  ] }
})
let access = accessFrom(['admin'])
vi.mock('../hooks/useAccess', async (orig) => ({ ...(await orig<typeof import('../hooks/useAccess')>()), useAccess: () => access }))
const llm = consoleModules.find((module) => module.name === 'llmplex')!
let modules = [llm]
vi.mock('../hooks/useModules', () => ({ useModules: () => ({ enabled: modules, error: null, loading: false, descriptors: [] }) }))
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: { email: 'a@b.c' }, logout: vi.fn(), ambient: true }) }))

function show() {
  render(<MemoryRouter initialEntries={['/overview']}>
    <CommandPalette />
    <Routes><Route element={<Layout />}><Route path="*" element={<p>page</p>} /></Route></Routes>
  </MemoryRouter>)
  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
}
const paletteLabels = () => within(screen.getByLabelText('Command').closest('div')!.parentElement!).queryAllByRole('button').map((b) => b.getAttribute('aria-label'))
const mainNav = () => within(screen.getByRole('navigation', { name: 'Main' }))
const navLinks = () => mainNav().queryAllByRole('link')
// Entries the navigation lets this role open: a link that is not marked disabled.
const openableInNav = () => navLinks().filter((l) => l.getAttribute('aria-disabled') !== 'true').map((l) => l.getAttribute('aria-label'))
const blockedInNav = () => navLinks().filter((l) => l.getAttribute('aria-disabled') === 'true').map((l) => l.textContent!.replace('Admin only', ''))

beforeEach(() => { modules = [llm] })

describe('Command palette', () => {
  it.each([['admin', ['admin']], ['deployer', ['deployer']], ['viewer', ['viewer']], ['no role', []]])('offers exactly the navigation entries a %s can open', (_name, roles) => {
    access = accessFrom(roles)
    show()
    const nav = openableInNav()
    expect(nav.length).toBeGreaterThan(0)
    const screens = paletteLabels().filter((l) => l?.startsWith('Go to '))
    expect(screens).toEqual(nav.map((l) => `Go to ${l}`))
  })

  it('shows an entry added to the navigation list in the palette, with no palette edit', () => {
    access = accessFrom(['admin'])
    show()
    expect(paletteLabels()).toEqual(expect.arrayContaining(['Go to Newly added', 'Go to Ops new', 'Go to Admin new', 'Go to Hooks']))
  })

  it('leaves out what the navigation hides or blocks for the role', () => {
    access = accessFrom(['viewer'])
    show()
    const labels = paletteLabels()
    expect(labels).toContain('Go to Newly added')
    for (const hidden of ['Hooks', 'Ops new', 'Admin new', 'Dashboard', 'Permissions', 'Agents', 'LLMPlex', 'Runs']) {
      expect(labels).not.toContain(`Go to ${hidden}`)
      expect(mainNav().queryByRole('link', { name: hidden })).toBeNull()
    }
    expect(labels).not.toContain('Deploy new instance')
  })

  it('does not offer an admin-only screen to a deployer, where the navigation shows it as admin only', () => {
    access = accessFrom(['deployer'])
    show()
    expect(blockedInNav().sort()).toEqual(['Admin new', 'Dashboard', 'Permissions'])
    const labels = paletteLabels()
    for (const blocked of blockedInNav()) expect(labels).not.toContain(`Go to ${blocked}`)
    expect(labels).toEqual(expect.arrayContaining(['Go to Hooks', 'Go to Ops new', 'Go to LLMPlex', 'Go to Agents', 'Deploy new instance']))
  })

  it('follows the enabled modules like the navigation does', () => {
    access = accessFrom(['admin'])
    modules = []
    show()
    expect(paletteLabels()).not.toContain('Go to LLMPlex')
    expect(mainNav().queryByRole('group', { name: 'Modules' })).toBeNull()
    expect(mainNav().queryByRole('link', { name: 'LLMPlex' })).toBeNull()
  })

  it('prioritizes workspace links and lets operators find a module by its purpose', () => {
    access = accessFrom(['admin'])
    show()
    const workspace = within(mainNav().getByRole('group', { name: 'Workspace' }))
    expect(workspace.getAllByRole('link').slice(0, 3).map((link) => link.textContent)).toEqual(['Overview', 'Access points', 'Usage'])
    const modelModule = within(mainNav().getByRole('group', { name: 'Modules' })).getByRole('link', { name: 'LLMPlex' })
    expect(modelModule).toHaveAccessibleDescription('Models')
    fireEvent.change(screen.getByLabelText('Command'), { target: { value: 'models' } })
    expect(paletteLabels()).toEqual(['Go to LLMPlex'])
    expect(screen.getByRole('button', { name: 'Go to LLMPlex' })).toHaveAccessibleDescription('Models')
  })

  it('finds coding harnesses under the canonical ClientPlex route', () => {
    access = accessFrom(['admin'])
    modules = [consoleModules.find((module) => module.name === 'clientplex')!]
    show()
    expect(mainNav().getByRole('link', { name: 'ClientPlex', description: 'Harnesses' })).toHaveAttribute('href', '/clientplex')
    fireEvent.change(screen.getByLabelText('Command'), { target: { value: 'harness' } })
    expect(paletteLabels()).toEqual(['Go to ClientPlex'])
  })

  it('keeps agents, models, tools, and harnesses in order when discovery order varies', () => {
    access = accessFrom(['admin'])
    modules = [...consoleModules].reverse()
    show()
    const group = within(mainNav().getByRole('group', { name: 'Modules' }))
    expect(group.getAllByRole('link').map((link) => link.getAttribute('aria-label'))).toEqual([
      'A2APlex', 'LLMPlex', 'MCPlex', 'ClientPlex', 'SkillsPlex', 'QuotaPlex',
    ])
  })

  it('closes the mobile drawer when selecting the page already open', () => {
    access = accessFrom(['admin'])
    show()
    const menu = screen.getByRole('button', { name: 'Menu' })
    fireEvent.click(menu)
    expect(menu).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(mainNav().getByRole('link', { name: 'Overview' }))
    expect(menu).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(menu)
    fireEvent.click(screen.getByRole('button', { name: 'Close menu' }))
    expect(menu).toHaveAttribute('aria-expanded', 'false')
  })
})
