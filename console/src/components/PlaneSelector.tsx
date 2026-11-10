interface PlaneSelectorProps {
  value: string
  onChange: (plane: string) => void
}

const planes = [
  { id: '', label: 'All Planes' },
  { id: 'mcplex', label: 'MCPlex' },
  { id: 'a2aplex', label: 'A2APlex' },
  { id: 'llmplex', label: 'LLMPlex' },
  { id: 'skillsplex', label: 'SkillsPlex' },
]

export default function PlaneSelector({ value, onChange }: PlaneSelectorProps) {
  return (
    <div className="flex gap-1 bg-gray-100 rounded-lg p-1 dark:bg-gray-800">
      {planes.map((plane) => (
        <button
          key={plane.id}
          onClick={() => onChange(plane.id)}
          className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
            value === plane.id
              ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-900 dark:text-gray-100'
              : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'
          }`}
        >
          {plane.label}
        </button>
      ))}
    </div>
  )
}
