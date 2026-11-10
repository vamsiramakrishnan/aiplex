const statusColors: Record<string, string> = {
  running: 'bg-status-ok-bg text-status-ok-fg',
  provisioning: 'bg-status-warning-bg text-status-warning-fg',
  degraded: 'bg-status-warning-bg text-status-warning-fg',
  stopped: 'bg-gray-100 text-gray-800',
  failed: 'bg-status-denied-bg text-status-denied-fg',
  terminated: 'bg-gray-100 text-gray-600 dark:text-gray-400',
  active: 'bg-status-ok-bg text-status-ok-fg',
  suspended: 'bg-status-denied-bg text-status-denied-fg',
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
