import type { ComponentType } from 'react'
import MCPlex from './pages/MCPlex'
import A2APlex from './pages/A2APlex'
import LLMPlex from './pages/LLMPlex'
import SkillsPlex from './pages/SkillsPlex'
import type { ModuleDescriptor } from './api/client'

export interface ConsoleModule {
  name: string
  path: string
  label: string
  component: ComponentType
}

// Only components shipped with this console can execute. Discovery advertises
// availability; it never supplies JavaScript URLs or arbitrary navigation.
export const consoleModules: readonly ConsoleModule[] = [
  { name: 'mcplex', path: '/mcplex', label: 'MCPlex', component: MCPlex },
  { name: 'a2aplex', path: '/a2aplex', label: 'A2APlex', component: A2APlex },
  { name: 'llmplex', path: '/llmplex', label: 'LLMPlex', component: LLMPlex },
  { name: 'skillsplex', path: '/skillsplex', label: 'SkillsPlex', component: SkillsPlex },
]

export function enabledConsoleModules(descriptors: readonly ModuleDescriptor[]): ConsoleModule[] {
  return consoleModules.filter((module) => descriptors.some((d) =>
    d.contract_version === 'v1' && d.name === module.name &&
    d.navigation?.some((entry) => entry.path === module.path),
  ))
}
