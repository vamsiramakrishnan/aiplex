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
    <div className="flex gap-1 bg-gray-100 rounded-lg p-1">
      {planes.map((plane) => (
        <button
          key={plane.id}
          onClick={() => onChange(plane.id)}
          className={`px-3 py-1.5 text-sm rounded-md transition-colors ${
            value === plane.id
              ? 'bg-white text-gray-900 shadow-sm'
              : 'text-gray-600 hover:text-gray-900'
          }`}
        >
          {plane.label}
        </button>
      ))}
    </div>
  )
}
