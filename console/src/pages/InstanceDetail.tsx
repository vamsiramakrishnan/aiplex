import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getInstance, undeployInstance, getInstanceHistory } from '../api/client'
import StatusBadge from '../components/StatusBadge'
import { SkeletonCard, SkeletonTable } from '../components/Skeleton'
import { useToast } from '../components/Toast'

export default function InstanceDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const toast = useToast()

  const instance = useQuery({
    queryKey: ['instance', id],
    queryFn: () => getInstance(id!),
    enabled: !!id,
  })

  const history = useQuery({
    queryKey: ['instance-history', id],
    queryFn: () => getInstanceHistory(id!),
    enabled: !!id,
  })

  const undeploy = useMutation({
    mutationFn: () => undeployInstance(id!),
    onSuccess: () => {
      toast.success(`Instance ${id} undeployed.`)
      queryClient.invalidateQueries({ queryKey: ['instances'] })
      navigate(-1)
    },
    onError: (err: Error) => {
      toast.error(`Undeploy failed: ${err.message}`)
    },
  })

  if (instance.isLoading) {
    return (
      <div className="space-y-6">
        <SkeletonCard />
        <SkeletonTable />
      </div>
    )
  }

  if (instance.error) {
    return (
      <div className="text-center py-12">
        <p className="text-red-600 dark:text-red-400">
          Failed to load instance: {(instance.error as Error).message}
        </p>
      </div>
    )
  }

  const inst = instance.data!

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-2xl font-bold text-gray-900 dark:text-white">
            {inst.display_name || inst.id}
          </h2>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-1 font-mono">{inst.id}</p>
        </div>
        <div className="flex items-center gap-3">
          <StatusBadge status={inst.status} />
          <button
            onClick={() => undeploy.mutate()}
            disabled={undeploy.isPending}
            className="px-4 py-2 bg-red-600 hover:bg-red-700 disabled:opacity-50 text-white text-sm rounded-lg transition-colors"
          >
            {undeploy.isPending ? 'Undeploying...' : 'Undeploy'}
          </button>
        </div>
      </div>

      {/* Info grid */}
      <div className="grid grid-cols-2 gap-6">
        <Section title="Instance Info">
          <InfoRow label="Kind" value={inst.kind} />
          <InfoRow label="Template" value={inst.template_id} />
          <InfoRow label="Namespace" value={inst.namespace} />
          <InfoRow label="Owner" value={inst.owner} />
          <InfoRow label="Deployed By" value={inst.deployed_by} />
          <InfoRow label="Deployed At" value={new Date(inst.deployed_at).toLocaleString()} />
          <InfoRow label="Updated At" value={new Date(inst.updated_at).toLocaleString()} />
          <InfoRow label="Replicas" value={String(inst.replicas)} />
          {inst.spiffe_id && <InfoRow label="SPIFFE ID" value={inst.spiffe_id} mono />}
        </Section>

        <Section title="Health">
          {inst.health ? (
            <>
              <InfoRow label="Status" value={inst.health.status} />
              <InfoRow label="Last Check" value={new Date(inst.health.last_check).toLocaleString()} />
              <InfoRow label="Latency" value={`${inst.health.latency_ms}ms`} />
            </>
          ) : (
            <p className="text-sm text-gray-400 dark:text-gray-500">No health data available.</p>
          )}
        </Section>
      </div>

      <Section title="Capabilities">
        {inst.capabilities.length > 0 ? (
          <div className="flex flex-wrap gap-2">
            {inst.capabilities.map((c) => (
              <span
                key={c.uri}
                className="px-2 py-1 bg-blue-50 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 text-xs rounded font-mono"
              >
                {c.uri}{c.actions ? ` [${c.actions.join(',')}]` : ''}
              </span>
            ))}
          </div>
        ) : (
          <p className="text-sm text-gray-400 dark:text-gray-500">No capabilities registered.</p>
        )}
      </Section>

      {/* Config */}
      {inst.config && Object.keys(inst.config).length > 0 && (
        <Section title="Configuration">
          <pre className="text-xs bg-gray-50 dark:bg-gray-800 rounded-lg p-4 overflow-x-auto font-mono text-gray-700 dark:text-gray-300">
            {JSON.stringify(inst.config, null, 2)}
          </pre>
        </Section>
      )}

      {/* Deploy history */}
      <Section title="Deploy History">
        {history.isLoading ? (
          <SkeletonTable />
        ) : history.data && history.data.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 dark:bg-gray-800">
                <tr>
                  <th className="text-left px-4 py-3 text-gray-500 dark:text-gray-400">Time</th>
                  <th className="text-left px-4 py-3 text-gray-500 dark:text-gray-400">Action</th>
                  <th className="text-left px-4 py-3 text-gray-500 dark:text-gray-400">Actor</th>
                  <th className="text-left px-4 py-3 text-gray-500 dark:text-gray-400">Details</th>
                </tr>
              </thead>
              <tbody>
                {history.data.map((entry, i) => (
                  <tr key={i} className="border-t border-gray-100 dark:border-gray-800">
                    <td className="px-4 py-3 text-gray-500 dark:text-gray-400 text-xs">
                      {new Date(entry.timestamp).toLocaleString()}
                    </td>
                    <td className="px-4 py-3 font-medium text-gray-900 dark:text-gray-100">{entry.action}</td>
                    <td className="px-4 py-3 font-mono text-xs text-gray-600 dark:text-gray-400">{entry.actor}</td>
                    <td className="px-4 py-3 text-xs text-gray-500 dark:text-gray-400">{entry.details ?? '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-sm text-gray-400 dark:text-gray-500">No history entries.</p>
        )}
      </Section>
    </div>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="bg-white dark:bg-gray-900 rounded-lg shadow p-6">
      <h3 className="font-semibold text-lg text-gray-900 dark:text-white mb-4">{title}</h3>
      {children}
    </div>
  )
}

function InfoRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-start py-1.5">
      <span className="text-sm text-gray-500 dark:text-gray-400 w-32 shrink-0">{label}</span>
      <span className={`text-sm text-gray-900 dark:text-gray-100 break-all ${mono ? 'font-mono text-xs' : ''}`}>
        {value}
      </span>
    </div>
  )
}
