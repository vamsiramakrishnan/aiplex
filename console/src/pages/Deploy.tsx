import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { deployInstance } from '../api/client'
import PlaneSelector from '../components/PlaneSelector'

export default function Deploy() {
  const queryClient = useQueryClient()
  const [plane, setPlane] = useState('mcplex')
  const [templateId, setTemplateId] = useState('')
  const [displayName, setDisplayName] = useState('')

  const deploy = useMutation({
    mutationFn: deployInstance,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['instances'] })
      setTemplateId('')
      setDisplayName('')
    },
  })

  return (
    <div className="max-w-lg">
      <h2 className="text-2xl font-bold mb-6">Deploy</h2>

      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium mb-2">Plane</label>
          <PlaneSelector value={plane} onChange={(p) => setPlane(p || 'mcplex')} />
        </div>

        <div>
          <label className="block text-sm font-medium mb-1">Template ID</label>
          <input
            value={templateId}
            onChange={(e) => setTemplateId(e.target.value)}
            className="w-full border rounded px-3 py-2 text-sm"
            placeholder="e.g. kb-search-server"
          />
        </div>

        <div>
          <label className="block text-sm font-medium mb-1">Display Name (optional)</label>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            className="w-full border rounded px-3 py-2 text-sm"
            placeholder="e.g. Knowledge Base Search"
          />
        </div>

        {deploy.error && (
          <p className="text-sm text-red-600">{deploy.error.message}</p>
        )}

        {deploy.isSuccess && (
          <p className="text-sm text-green-600">Deployed successfully!</p>
        )}

        <button
          onClick={() => deploy.mutate({ plane, template_id: templateId, display_name: displayName || undefined })}
          disabled={!templateId || deploy.isPending}
          className="px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700 disabled:opacity-50"
        >
          {deploy.isPending ? 'Deploying...' : 'Deploy'}
        </button>
      </div>
    </div>
  )
}
