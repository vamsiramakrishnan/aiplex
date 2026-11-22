import type { ComponentType } from 'react'
import MCPlex from './pages/MCPlex'
import A2APlex from './pages/A2APlex'
import LLMPlex from './pages/LLMPlex'
import SkillsPlex from './pages/SkillsPlex'
import ClientPlex from './pages/ClientPlex'
import { QuotaOverview } from './pages/QuotaTabs'
import type { ModuleDescriptor } from './api/client'

export interface ConsoleModule {
  name: string
  path: string
  label: string
  purpose: string
  navOrder: number
  component: ComponentType
}

// Only components shipped with this console can execute. Discovery advertises
// availability; it never supplies JavaScript URLs or arbitrary navigation.
export const consoleModules: readonly ConsoleModule[] = [
  { name: 'mcplex', path: '/mcplex', label: 'MCPlex', purpose: 'MCP tools', navOrder: 3, component: MCPlex },
  { name: 'a2aplex', path: '/a2aplex', label: 'A2APlex', purpose: 'A2A agents', navOrder: 1, component: A2APlex },
  { name: 'llmplex', path: '/llmplex', label: 'LLMPlex', purpose: 'Models', navOrder: 2, component: LLMPlex },
  { name: 'skillsplex', path: '/skillsplex', label: 'SkillsPlex', purpose: 'Deployed skills', navOrder: 5, component: SkillsPlex },
  { name: 'clientplex', path: '/clientplex', label: 'ClientPlex', purpose: 'Harnesses', navOrder: 4, component: ClientPlex },
  { name: 'quotaplex', path: '/quotaplex', label: 'QuotaPlex', purpose: 'Budgets and quota', navOrder: 6, component: QuotaOverview },
]

export function enabledConsoleModules(descriptors: readonly ModuleDescriptor[]): ConsoleModule[] {
  return consoleModules.filter((module) => descriptors.some((d) =>
    d.contract_version === 'v1' && d.name === module.name &&
    d.navigation?.some((entry) => entry.path === module.path),
  ))
}
