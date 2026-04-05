const statusColors: Record<string, string> = {
  running: 'bg-green-100 text-green-800',
  provisioning: 'bg-yellow-100 text-yellow-800',
  degraded: 'bg-orange-100 text-orange-800',
  stopped: 'bg-gray-100 text-gray-800',
  failed: 'bg-red-100 text-red-800',
  terminated: 'bg-gray-100 text-gray-500',
  active: 'bg-green-100 text-green-800',
  suspended: 'bg-red-100 text-red-800',
}

interface StatusBadgeProps {
  status: string
}

export default function StatusBadge({ status }: StatusBadgeProps) {
  const color = statusColors[status] ?? 'bg-gray-100 text-gray-800'
  return (
    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${color}`}>
      {status}
    </span>
  )
}
