import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ScopeSelector from '../components/ScopeSelector'

async function getUserScopes(userId: string) {
  const res = await fetch(`/auth/users/${encodeURIComponent(userId)}/scopes`)
  if (!res.ok) throw new Error('Failed to fetch scopes')
  return res.json() as Promise<{ user_id: string; scopes: string[]; by_plane: Record<string, string[]> }>
}

async function setUserScopes(userId: string, scopes: string[]) {
  const res = await fetch(`/auth/users/${encodeURIComponent(userId)}/scopes`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ scopes }),
  })
  if (!res.ok) throw new Error('Failed to update scopes')
}

// All known scopes — in production, fetched from the API
const ALL_SCOPES = [
  'mcp:tools:search_curriculum',
  'mcp:tools:generate_quiz',
  'mcp:tools:get_document',
  'mcp:tools:grade_submission',
  'a2a:task:research',
  'a2a:task:summarize',
  'a2a:task:visualize',
  'llm:model:gemini-2.5-flash',
  'llm:model:gemini-2.5-pro',
  'llm:model:claude-opus-4',
  'llm:model:claude-sonnet-4',
  'llm:model:gpt-4.1',
  'llm:capability:text',
  'llm:capability:vision',
  'llm:capability:code',
]

export default function Permissions() {
  const queryClient = useQueryClient()
  const [userId, setUserId] = useState('')
  const [selectedScopes, setSelectedScopes] = useState<string[]>([])
  const [loaded, setLoaded] = useState(false)

  const scopesQuery = useQuery({
    queryKey: ['userScopes', userId],
    queryFn: () => getUserScopes(userId),
    enabled: false,
  })

  const saveMutation = useMutation({
    mutationFn: () => setUserScopes(userId, selectedScopes),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['userScopes', userId] }),
  })

  const loadScopes = async () => {
    const result = await scopesQuery.refetch()
    if (result.data) {
      setSelectedScopes(result.data.scopes || [])
      setLoaded(true)
    }
  }

  return (
    <div className="max-w-2xl">
      <h2 className="text-2xl font-bold mb-2">Permissions</h2>
      <p className="text-gray-500 mb-6">Manage user scopes (Dimension B — user ceiling)</p>

      <div className="flex gap-2 mb-6">
        <input
          value={userId}
          onChange={(e) => { setUserId(e.target.value); setLoaded(false) }}
          placeholder="user@example.com"
          className="flex-1 border rounded px-3 py-2 text-sm"
        />
        <button
          onClick={loadScopes}
          disabled={!userId}
          className="px-4 py-2 bg-gray-100 text-sm rounded hover:bg-gray-200 disabled:opacity-50"
        >
          Load
        </button>
      </div>

      {loaded && (
        <>
          <ScopeSelector
            available={ALL_SCOPES}
            selected={selectedScopes}
            onChange={setSelectedScopes}
          />

          <div className="mt-4 flex gap-2">
            <button
              onClick={() => saveMutation.mutate()}
              disabled={saveMutation.isPending}
              className="px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700 disabled:opacity-50"
            >
              {saveMutation.isPending ? 'Saving...' : 'Save Permissions'}
            </button>
            {saveMutation.isSuccess && (
              <span className="text-sm text-green-600 self-center">Saved!</span>
            )}
            {saveMutation.error && (
              <span className="text-sm text-red-600 self-center">{saveMutation.error.message}</span>
            )}
          </div>
        </>
      )}
    </div>
  )
}
