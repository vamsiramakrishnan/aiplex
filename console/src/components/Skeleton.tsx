export function SkeletonLine({ width = 'w-full' }: { width?: string }) {
  return (
    <div className={`h-4 ${width} rounded bg-gray-200 dark:bg-gray-700 skeleton-shimmer`} />
  )
}

export function SkeletonCard() {
  return (
    <div className="bg-white dark:bg-gray-900 rounded-lg shadow p-6 space-y-4">
      <SkeletonLine width="w-1/3" />
      <SkeletonLine />
      <SkeletonLine width="w-2/3" />
      <SkeletonLine width="w-1/2" />
    </div>
  )
}

export function SkeletonTable({ rows = 4 }: { rows?: number }) {
  return (
    <div className="bg-white dark:bg-gray-900 rounded-lg shadow overflow-hidden">
      {/* Header */}
      <div className="flex gap-4 px-4 py-3 bg-gray-50 dark:bg-gray-800">
        <SkeletonLine width="w-24" />
        <SkeletonLine width="w-32" />
        <SkeletonLine width="w-20" />
        <SkeletonLine width="w-28" />
      </div>
      {/* Rows */}
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex gap-4 px-4 py-3 border-t border-gray-100 dark:border-gray-800">
          <SkeletonLine width="w-24" />
          <SkeletonLine width="w-32" />
          <SkeletonLine width="w-20" />
          <SkeletonLine width="w-28" />
        </div>
      ))}
    </div>
  )
}
