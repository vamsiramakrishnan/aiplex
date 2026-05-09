interface KindSelectorProps {
  value: string
  onChange: (kind: string) => void
}

const kinds = [
  { id: '', label: 'All Kinds' },
  { id: 'tool', label: 'Tool' },
  { id: 'task', label: 'Task' },
  { id: 'model', label: 'Model' },
  { id: 'skill', label: 'Skill' },
  { id: 'memory', label: 'Memory' },
]

export default function KindSelector({ value, onChange }: KindSelectorProps) {
  return (
    <div className="flex gap-1 bg-gray-100 rounded-lg p-1">
      {kinds.map((kind) => (
        <button
          key={kind.id}
          onClick={() => onChange(kind.id)}
          className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
            value === kind.id
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          {kind.label}
        </button>
      ))}
    </div>
  )
}
