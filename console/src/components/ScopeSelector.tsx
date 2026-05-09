import { useState } from 'react'

interface CapSelectorProps {
  available?: string[]
  selected: string[]
  onChange: (caps: string[]) => void
}

// Common capability URIs for quick selection.
const COMMON_CAPS: Record<string, { label: string; caps: string[] }> = {
  Tools: {
    label: 'Tools',
    caps: [
      'cap://tool/search_curriculum@v1',
      'cap://tool/get_document@v1',
      'cap://tool/generate_quiz@v1',
      'cap://tool/read_mastery@v1',
      'cap://tool/update_mastery@v1',
      'cap://tool/github_search@v1',
      'cap://tool/github_read_file@v1',
      'cap://tool/pg_query@v1',
    ],
  },
  Tasks: {
    label: 'Tasks',
    caps: [
      'cap://task/research@v1',
      'cap://task/visualize@v1',
      'cap://task/summarize@v1',
    ],
  },
  Models: {
    label: 'Models',
    caps: [
      'cap://model/gemini-2.5-flash@v1',
      'cap://model/gemini-2.5-pro@v1',
      'cap://model/claude-sonnet-4@v1',
      'cap://model/gpt-4.1@v1',
      'cap://model/gpt-4.1-mini@v1',
    ],
  },
}

function groupByKind(uris: string[]): Record<string, string[]> {
  const groups: Record<string, string[]> = {}
  for (const uri of uris) {
    const m = uri.match(/^cap:\/\/([^/]+)\//)
    const kind = m ? m[1] : 'Other'
    const label = kind.charAt(0).toUpperCase() + kind.slice(1)
    if (!groups[label]) groups[label] = []
    groups[label].push(uri)
  }
  return groups
}

function capLabel(uri: string): string {
  const m = uri.match(/^cap:\/\/[^/]+\/([^@]+)@/)
  return m ? m[1] : uri
}

export default function ScopeSelector({ available, selected, onChange }: CapSelectorProps) {
  const [search, setSearch] = useState('')
  const [customCap, setCustomCap] = useState('')

  const allCaps = available ?? Object.values(COMMON_CAPS).flatMap(g => g.caps)
  const merged = [...new Set([...allCaps, ...selected])]
  const groups = groupByKind(merged)

  const toggle = (cap: string) => {
    if (selected.includes(cap)) {
      onChange(selected.filter((s) => s !== cap))
    } else {
      onChange([...selected, cap])
    }
  }

  const toggleAll = (caps: string[]) => {
    const allSelected = caps.every((s) => selected.includes(s))
    if (allSelected) {
      onChange(selected.filter((s) => !caps.includes(s)))
    } else {
      onChange([...new Set([...selected, ...caps])])
    }
  }

  const addCustom = () => {
    const uri = customCap.trim()
    if (uri && !selected.includes(uri)) {
      onChange([...selected, uri])
      setCustomCap('')
    }
  }

  const filtered = search
    ? Object.fromEntries(
        Object.entries(groups).map(([kind, caps]) => [
          kind,
          caps.filter((c) => c.toLowerCase().includes(search.toLowerCase())),
        ]).filter(([, caps]) => (caps as string[]).length > 0)
      )
    : groups

  return (
    <div className="space-y-4">
      <input
        type="text"
        placeholder="Filter capabilities..."
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full border rounded px-3 py-2 text-sm"
      />

      {Object.entries(filtered).map(([kind, caps]) => (
        <div key={kind} className="border rounded-lg p-3">
          <div className="flex items-center justify-between mb-2">
            <h4 className="font-semibold text-sm">{kind}</h4>
            <button
              onClick={() => toggleAll(caps as string[])}
              className="text-xs text-brand-600 hover:underline"
            >
              {(caps as string[]).every((s) => selected.includes(s)) ? 'Deselect all' : 'Select all'}
            </button>
          </div>
          <div className="space-y-1">
            {(caps as string[]).map((uri) => (
              <label key={uri} className="flex items-center gap-2 text-sm cursor-pointer">
                <input
                  type="checkbox"
                  checked={selected.includes(uri)}
                  onChange={() => toggle(uri)}
                  className="rounded"
                />
                <code className="text-xs bg-gray-100 px-1 py-0.5 rounded">{capLabel(uri)}</code>
                <span className="text-gray-400 text-xs">{uri}</span>
              </label>
            ))}
          </div>
        </div>
      ))}

      <div className="flex gap-2">
        <input
          type="text"
          placeholder="Add custom capability (e.g. cap://tool/my_tool@v1)"
          value={customCap}
          onChange={(e) => setCustomCap(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && addCustom()}
          className="flex-1 border rounded px-3 py-2 text-sm font-mono"
        />
        <button
          onClick={addCustom}
          disabled={!customCap.trim()}
          className="px-3 py-2 border rounded text-sm hover:bg-gray-50 disabled:opacity-50"
        >
          Add
        </button>
      </div>

      <div className="text-xs text-gray-500">
        {selected.length} capabilit{selected.length !== 1 ? 'ies' : 'y'} selected
      </div>
    </div>
  )
}
