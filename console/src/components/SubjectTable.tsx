import type { ReactNode } from 'react'

export type SubjectKind = 'agent' | 'user' | 'client' | 'group' | 'principal' | 'registration'

export interface SubjectRow {
  kind: SubjectKind
  id: string
  /** Human label; falls back to id. */
  label?: string
  /** Free-form secondary text (identity source, how it authenticates, ...). */
  detail?: ReactNode
}

const KIND_LABEL: Record<SubjectKind, string> = {
  agent: 'Agent', user: 'User', client: 'Client', group: 'Group', principal: 'Principal', registration: 'Client registration',
}

/**
 * Table of callers. Used for an access point's subject constraints; shaped so
 * a subjects overview (agents / users / clients) can reuse it with extra
 * columns via `columns`.
 */
export default function SubjectTable({ rows, caption, empty, columns, actions }: {
  rows: SubjectRow[]
  caption: string
  empty: ReactNode
  columns?: { header: string; cell: (row: SubjectRow) => ReactNode }[]
  actions?: (row: SubjectRow) => ReactNode
}) {
  if (rows.length === 0) return <p className="text-sm text-gray-600 dark:text-gray-400">{empty}</p>
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr className="border-b border-gray-200 dark:border-gray-800 text-xs text-gray-600 dark:text-gray-400">
            <th scope="col" className="py-1 pr-4">Type</th>
            <th scope="col" className="pr-4">Subject</th>
            <th scope="col" className="pr-4">Detail</th>
            {columns?.map(c => <th key={c.header} scope="col" className="pr-4">{c.header}</th>)}
            {actions && <th scope="col"><span className="sr-only">Actions</span></th>}
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={`${row.kind}:${row.id}`} className="border-t border-gray-200 dark:border-gray-800 align-top">
              <td className="py-1 pr-4">{KIND_LABEL[row.kind]}</td>
              <th scope="row" className="pr-4 font-normal font-mono text-xs break-all">{row.label && row.label !== row.id ? `${row.label} (${row.id})` : row.id}</th>
              <td className="pr-4 text-xs text-gray-600 dark:text-gray-400">{row.detail ?? '—'}</td>
              {columns?.map(c => <td key={c.header} className="pr-4">{c.cell(row)}</td>)}
              {actions && <td className="whitespace-nowrap">{actions(row)}</td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
