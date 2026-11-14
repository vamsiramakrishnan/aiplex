import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import QuotaPlex from './QuotaPlex'
import Usage from './Usage'
import Budgets from './Budgets'

// QuotaPlex sections share one tab strip: allocations, budgets and the usage ledger.
function QuotaTabs({ active, children }: { active: 'overview' | 'budgets' | 'usage'; children: ReactNode }) {
  const tab = (id: 'overview' | 'budgets' | 'usage', to: string, label: string) => (
    <Link to={to} aria-current={active === id ? 'page' : undefined}
      className={`border-b-2 px-1 pb-2 text-sm ${active === id ? 'border-brand-600 font-medium' : 'border-transparent text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-200'}`}>{label}</Link>
  )
  return <div>
    <nav aria-label="QuotaPlex sections" className="mb-6 flex gap-6 border-b border-gray-200 dark:border-gray-800">
      {tab('overview', '/quotaplex', 'Overview')}
      {tab('budgets', '/quotaplex/budgets', 'Budgets')}
      {tab('usage', '/quotaplex/usage', 'Usage')}
    </nav>
    {children}
  </div>
}

export function QuotaOverview() { return <QuotaTabs active="overview"><QuotaPlex /></QuotaTabs> }
export function QuotaUsage() { return <QuotaTabs active="usage"><Usage /></QuotaTabs> }
export function QuotaBudgets() { return <QuotaTabs active="budgets"><Budgets /></QuotaTabs> }
