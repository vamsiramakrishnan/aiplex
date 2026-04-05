import { useState } from 'react'

interface ScopeSelectorProps {
  available: string[]
  selected: string[]
  onChange: (scopes: string[]) => void
}

function groupByPlane(scopes: string[]): Record<string, string[]> {
  const groups: Record<string, string[]> = {}
  for (const scope of scopes) {
    const plane = scope.startsWith('mcp:') ? 'MCPlex'
      : scope.startsWith('a2a:') ? 'A2APlex'
      : scope.startsWith('llm:') ? 'LLMPlex'
      : 'Other'
    if (!groups[plane]) groups[plane] = []
    groups[plane].push(scope)
  }
  return groups
}

function scopeLabel(scope: string): string {
  // mcp:tools:search_curriculum → search_curriculum
  const parts = scope.split(':')
  return parts[parts.length - 1]
}

export default function ScopeSelector({ available, selected, onChange }: ScopeSelectorProps) {
  const [search, setSearch] = useState('')
  const groups = groupByPlane(available)

  const toggle = (scope: string) => {
    if (selected.includes(scope)) {
      onChange(selected.filter((s) => s !== scope))
    } else {
      onChange([...selected, scope])
    }
  }

  const toggleAll = (scopes: string[]) => {
    const allSelected = scopes.every((s) => selected.includes(s))
    if (allSelected) {
      onChange(selected.filter((s) => !scopes.includes(s)))
    } else {
      onChange([...new Set([...selected, ...scopes])])
    }
  }

  const filtered = search
    ? Object.fromEntries(
        Object.entries(groups).map(([plane, scopes]) => [
          plane,
          scopes.filter((s) => s.toLowerCase().includes(search.toLowerCase())),
        ]).filter(([, scopes]) => (scopes as string[]).length > 0)
      )
    : groups

  return (
    <div className="space-y-4">
      <input
        type="text"
        placeholder="Filter scopes..."
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full border rounded px-3 py-2 text-sm"
      />

      {Object.entries(filtered).map(([plane, scopes]) => (
        <div key={plane} className="border rounded-lg p-3">
          <div className="flex items-center justify-between mb-2">
            <h4 className="font-semibold text-sm">{plane}</h4>
            <button
              onClick={() => toggleAll(scopes as string[])}
              className="text-xs text-brand-600 hover:underline"
            >
              {(scopes as string[]).every((s) => selected.includes(s)) ? 'Deselect all' : 'Select all'}
            </button>
          </div>
          <div className="space-y-1">
            {(scopes as string[]).map((scope) => (
              <label key={scope} className="flex items-center gap-2 text-sm cursor-pointer">
                <input
                  type="checkbox"
                  checked={selected.includes(scope)}
                  onChange={() => toggle(scope)}
                  className="rounded"
                />
                <code className="text-xs bg-gray-100 px-1 py-0.5 rounded">{scopeLabel(scope)}</code>
                <span className="text-gray-400 text-xs">{scope}</span>
              </label>
            ))}
          </div>
        </div>
      ))}

      <div className="text-xs text-gray-500">
        {selected.length} of {available.length} scopes selected
      </div>
    </div>
  )
}
