import { useState } from 'react'

interface ScopeSelectorProps {
  available?: string[]
  selected: string[]
  onChange: (scopes: string[]) => void
}

// Common scopes for quick selection
const COMMON_SCOPES: Record<string, { label: string; scopes: string[] }> = {
  MCPlex: {
    label: 'Tools',
    scopes: [
      'mcp:tools:search_curriculum',
      'mcp:tools:get_document',
      'mcp:tools:generate_quiz',
      'mcp:tools:read_mastery',
      'mcp:tools:update_mastery',
      'mcp:tools:github_search',
      'mcp:tools:github_read_file',
      'mcp:tools:pg_query',
    ],
  },
  A2APlex: {
    label: 'Tasks',
    scopes: [
      'a2a:task:research',
      'a2a:task:visualize',
      'a2a:task:summarize',
    ],
  },
  LLMPlex: {
    label: 'Models',
    scopes: [
      'llm:model:gemini-2.5-flash',
      'llm:model:gemini-2.5-pro',
      'llm:model:claude-sonnet-4-20250514',
      'llm:model:gpt-4.1',
      'llm:model:gpt-4.1-mini',
    ],
  },
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
  const parts = scope.split(':')
  return parts[parts.length - 1]
}

export default function ScopeSelector({ available, selected, onChange }: ScopeSelectorProps) {
  const [search, setSearch] = useState('')
  const [customScope, setCustomScope] = useState('')

  // Use available scopes if provided, otherwise use common scopes
  const allScopes = available ?? Object.values(COMMON_SCOPES).flatMap(g => g.scopes)
  // Include any selected scopes that aren't in the available list
  const merged = [...new Set([...allScopes, ...selected])]
  const groups = groupByPlane(merged)

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

  const addCustom = () => {
    const scope = customScope.trim()
    if (scope && !selected.includes(scope)) {
      onChange([...selected, scope])
      setCustomScope('')
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

      {/* Custom scope entry */}
      <div className="flex gap-2">
        <input
          type="text"
          placeholder="Add custom scope (e.g. mcp:tools:my_tool)"
          value={customScope}
          onChange={(e) => setCustomScope(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && addCustom()}
          className="flex-1 border rounded px-3 py-2 text-sm font-mono"
        />
        <button
          onClick={addCustom}
          disabled={!customScope.trim()}
          className="px-3 py-2 border rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          Add
        </button>
      </div>

      <div className="text-xs text-gray-500">
        {selected.length} scope{selected.length !== 1 ? 's' : ''} selected
      </div>
    </div>
  )
}
