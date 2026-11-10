import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import QuotaPlex from './QuotaPlex'
import Usage from './Usage'

// QuotaPlex sections share one tab strip: allocations and the usage ledger.
function QuotaTabs({ active, children }: { active: 'overview' | 'usage'; children: ReactNode }) {
  const tab = (id: 'overview' | 'usage', to: string, label: string) => (
    <Link to={to} aria-current={active === id ? 'page' : undefined}
      className={`border-b-2 px-1 pb-2 text-sm ${active === id ? 'border-brand-600 font-medium' : 'border-transparent text-gray-500 hover:text-gray-900 dark:hover:text-gray-200'}`}>{label}</Link>
  )
  return <div>
    <nav aria-label="QuotaPlex sections" className="mb-6 flex gap-6 border-b border-gray-200 dark:border-gray-800">
      {tab('overview', '/quotaplex', 'Overview')}
      {tab('usage', '/quotaplex/usage', 'Usage')}
    </nav>
    {children}
  </div>
}

export function QuotaOverview() { return <QuotaTabs active="overview"><QuotaPlex /></QuotaTabs> }
export function QuotaUsage() { return <QuotaTabs active="usage"><Usage /></QuotaTabs> }
