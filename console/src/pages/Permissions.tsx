import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import ScopeSelector from '../components/ScopeSelector'
import type { Cap } from '../api/client'

async function getUserCaps(userId: string) {
  const res = await fetch(`/auth/users/${encodeURIComponent(userId)}/caps`)
  if (!res.ok) throw new Error('Failed to fetch caps')
  return res.json() as Promise<{ user_id: string; caps: Cap[]; by_kind: Record<string, Cap[]> }>
}

async function setUserCaps(userId: string, caps: Cap[]) {
  const res = await fetch(`/auth/users/${encodeURIComponent(userId)}/caps`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ caps }),
  })
  if (!res.ok) throw new Error('Failed to update caps')
}

const ALL_CAPS = [
  'cap://tool/search_curriculum@v1',
  'cap://tool/generate_quiz@v1',
  'cap://tool/get_document@v1',
  'cap://tool/grade_submission@v1',
  'cap://task/research@v1',
  'cap://task/summarize@v1',
  'cap://task/visualize@v1',
  'cap://model/gemini-2.5-flash@v1',
  'cap://model/gemini-2.5-pro@v1',
  'cap://model/claude-opus-4@v1',
  'cap://model/claude-sonnet-4@v1',
  'cap://model/gpt-4.1@v1',
]

export default function Permissions() {
  const queryClient = useQueryClient()
  const [userId, setUserId] = useState('')
  const [selectedURIs, setSelectedURIs] = useState<string[]>([])
  const [loaded, setLoaded] = useState(false)

  const capsQuery = useQuery({
    queryKey: ['userCaps', userId],
    queryFn: () => getUserCaps(userId),
    enabled: false,
  })

  const saveMutation = useMutation({
    mutationFn: () => setUserCaps(userId, selectedURIs.map(uri => ({ uri }))),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['userCaps', userId] }),
  })

  const loadCaps = async () => {
    const result = await capsQuery.refetch()
    if (result.data) {
      setSelectedURIs((result.data.caps || []).map(c => c.uri))
      setLoaded(true)
    }
  }

  return (
    <div className="max-w-2xl">
      <h2 className="text-2xl font-bold mb-2">Permissions</h2>
      <p className="text-gray-500 mb-6">Manage user capabilities (Dimension B — user ceiling)</p>

      <div className="flex gap-2 mb-6">
        <input
          value={userId}
          onChange={(e) => { setUserId(e.target.value); setLoaded(false) }}
          placeholder="user@example.com"
          className="flex-1 border rounded px-3 py-2 text-sm"
        />
        <button
          onClick={loadCaps}
          disabled={!userId}
          className="px-4 py-2 bg-gray-100 text-sm rounded hover:bg-gray-200 disabled:opacity-50"
        >
          Load
        </button>
      </div>

      {loaded && (
        <>
          <ScopeSelector
            available={ALL_CAPS}
            selected={selectedURIs}
            onChange={setSelectedURIs}
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
