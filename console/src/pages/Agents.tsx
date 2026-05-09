import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listAgents, registerAgent, deleteAgent, type Cap } from '../api/client'
import StatusBadge from '../components/StatusBadge'
import ScopeSelector from '../components/ScopeSelector'

const AUTH_METHODS = [
  { value: 'client_credentials', label: 'Client Credentials', desc: 'Server-to-server (internal agents, CI/CD)' },
  { value: 'authorization_code', label: 'Authorization Code + PKCE', desc: 'IDE integrations (Cursor, VS Code)' },
  { value: 'device_code', label: 'Device Grant', desc: 'CLI tools (Claude Code, terminal agents)' },
]

const KIND_OF = (uri: string): string => {
  const m = uri.match(/^cap:\/\/([^/]+)\//)
  return m ? m[1] : 'other'
}

const KIND_COLORS: Record<string, string> = {
  tool: 'bg-blue-100 text-blue-700',
  task: 'bg-purple-100 text-purple-700',
  model: 'bg-green-100 text-green-700',
  skill: 'bg-amber-100 text-amber-700',
  memory: 'bg-pink-100 text-pink-700',
  meta: 'bg-gray-100 text-gray-700',
  other: 'bg-gray-100 text-gray-700',
}

export default function Agents() {
  const queryClient = useQueryClient()
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents })
  const [showRegister, setShowRegister] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  const remove = useMutation({
    mutationFn: deleteAgent,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['agents'] }),
  })

  const filteredAgents = agents.data?.filter(agent =>
    agent.client_id?.toLowerCase().includes(search.toLowerCase()) ||
    agent.display_name?.toLowerCase().includes(search.toLowerCase())
  ) || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-2xl font-bold">Agents</h2>
          <p className="text-gray-500">Registered OAuth clients across all capability kinds</p>
        </div>
        <button
          onClick={() => setShowRegister(!showRegister)}
          className="px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
        >
          {showRegister ? 'Cancel' : 'Register Agent'}
        </button>
      </div>

      {showRegister && (
        <RegisterAgentForm
          onSuccess={() => {
            setShowRegister(false)
            queryClient.invalidateQueries({ queryKey: ['agents'] })
          }}
        />
      )}

      <div className="mb-4">
        <input
          type="text"
          placeholder="Search agents..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full border rounded px-3 py-2 text-sm"
        />
      </div>

      <div className="bg-white rounded-lg shadow">
        <table className="w-full text-sm">
          <thead className="bg-gray-50">
            <tr>
              <th className="text-left px-4 py-3">Name</th>
              <th className="text-left px-4 py-3">Client ID</th>
              <th className="text-left px-4 py-3">Auth Method</th>
              <th className="text-left px-4 py-3">Capabilities</th>
              <th className="text-left px-4 py-3">Status</th>
              <th className="text-left px-4 py-3"></th>
            </tr>
          </thead>
          <tbody>
            {filteredAgents.map((agent) => (
              <>
                <tr
                  key={agent.client_id}
                  className="border-t cursor-pointer hover:bg-gray-50"
                  onClick={() => setExpanded(expanded === agent.client_id ? null : agent.client_id)}
                >
                  <td className="px-4 py-3 font-medium">{agent.display_name}</td>
                  <td className="px-4 py-3 text-gray-500 font-mono text-xs">{agent.client_id}</td>
                  <td className="px-4 py-3 text-gray-500">{agent.auth_method}</td>
                  <td className="px-4 py-3 text-gray-500">{agent.allowed_caps?.length ?? 0} caps</td>
                  <td className="px-4 py-3"><StatusBadge status={agent.status} /></td>
                  <td className="px-4 py-3">
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        if (confirm(`Delete agent ${agent.client_id}?`)) {
                          remove.mutate(agent.client_id)
                        }
                      }}
                      className="text-xs text-red-500 hover:text-red-700"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
                {expanded === agent.client_id && (
                  <tr key={agent.client_id + '-detail'} className="bg-gray-50">
                    <td colSpan={6} className="px-4 py-3">
                      {agent.description && (
                        <p className="text-sm text-gray-600 mb-2">{agent.description}</p>
                      )}
                      <div className="text-xs font-medium text-gray-500 mb-1">Allowed Capabilities (Dimension A):</div>
                      <div className="flex flex-wrap gap-1">
                        {agent.allowed_caps?.map(c => {
                          const kind = KIND_OF(c.uri)
                          return (
                            <span key={c.uri} className={`px-2 py-0.5 rounded text-xs ${KIND_COLORS[kind]}`}>
                              {c.uri}{c.actions ? ` [${c.actions.join(',')}]` : ''}
                            </span>
                          )
                        })}
                      </div>
                      <div className="mt-2 text-xs text-gray-400">
                        Registered: {new Date(agent.registered_at).toLocaleDateString()}
                      </div>
                    </td>
                  </tr>
                )}
              </>
            ))}
            {filteredAgents.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-gray-400">
                  {agents.data?.length === 0
                    ? 'No agents registered yet. Click "Register Agent" to get started.'
                    : 'No agents match your search.'}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function RegisterAgentForm({ onSuccess }: { onSuccess: () => void }) {
  const [step, setStep] = useState(1)
  const [clientId, setClientId] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [description, setDescription] = useState('')
  const [authMethod, setAuthMethod] = useState('client_credentials')
  const [capURIs, setCapURIs] = useState<string[]>([])
  const [error, setError] = useState('')

  const register = useMutation({
    mutationFn: registerAgent,
    onSuccess,
    onError: (e) => setError((e as Error).message),
  })

  const grantTypes = authMethod === 'client_credentials' ? ['client_credentials'] :
                     authMethod === 'device_code' ? ['urn:ietf:params:oauth:grant-type:device_code'] :
                     ['authorization_code']

  const allowedCaps: Cap[] = capURIs.map(uri => ({ uri }))

  return (
    <div className="bg-white rounded-lg shadow p-6 mb-6">
      <h3 className="text-lg font-bold mb-4">Register New Agent</h3>

      <div className="flex gap-4 mb-6">
        {['Identity', 'Auth Method', 'Capabilities', 'Review'].map((label, i) => (
          <div key={label} className="flex items-center gap-2">
            <div className={`w-6 h-6 rounded-full flex items-center justify-center text-xs
              ${step > i + 1 ? 'bg-green-100 text-green-700' :
                step === i + 1 ? 'bg-brand-600 text-white' :
                'bg-gray-100 text-gray-400'}`}>
              {step > i + 1 ? '✓' : i + 1}
            </div>
            <span className={`text-sm ${step === i + 1 ? 'font-medium' : 'text-gray-400'}`}>
              {label}
            </span>
          </div>
        ))}
      </div>

      {step === 1 && (
        <div className="space-y-3">
          <div>
            <label className="block text-sm font-medium mb-1">Client ID</label>
            <input
              value={clientId}
              onChange={e => setClientId(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-'))}
              className="w-full border rounded px-3 py-2 text-sm font-mono"
              placeholder="tutor-agent"
            />
            <p className="text-xs text-gray-400 mt-1">Unique identifier. Lowercase, hyphens only.</p>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1">Display Name</label>
            <input
              value={displayName}
              onChange={e => setDisplayName(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder="Tutor Agent"
            />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1">Description (optional)</label>
            <textarea
              value={description}
              onChange={e => setDescription(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
              rows={2}
              placeholder="What does this agent do?"
            />
          </div>
          <button
            disabled={!clientId || !displayName}
            onClick={() => setStep(2)}
            className="px-4 py-2 bg-brand-600 text-white text-sm rounded disabled:opacity-50"
          >
            Next
          </button>
        </div>
      )}

      {step === 2 && (
        <div className="space-y-3">
          <p className="text-sm text-gray-600">How will this agent authenticate?</p>
          <div className="grid gap-2">
            {AUTH_METHODS.map(m => (
              <button
                key={m.value}
                onClick={() => setAuthMethod(m.value)}
                className={`text-left p-3 border-2 rounded-lg ${
                  authMethod === m.value ? 'border-brand-500 bg-brand-50' : 'border-gray-200'}`}
              >
                <div className="text-sm font-medium">{m.label}</div>
                <div className="text-xs text-gray-500">{m.desc}</div>
              </button>
            ))}
          </div>
          <div className="flex gap-2">
            <button onClick={() => setStep(1)} className="px-4 py-2 border text-sm rounded">Back</button>
            <button onClick={() => setStep(3)} className="px-4 py-2 bg-brand-600 text-white text-sm rounded">
              Next
            </button>
          </div>
        </div>
      )}

      {step === 3 && (
        <div className="space-y-3">
          <p className="text-sm text-gray-600">
            Set the maximum capabilities this agent can ever request (Dimension A ceiling).
            Users can further restrict these at consent time.
          </p>
          <ScopeSelector selected={capURIs} onChange={setCapURIs} />
          <div className="flex gap-2">
            <button onClick={() => setStep(2)} className="px-4 py-2 border text-sm rounded">Back</button>
            <button onClick={() => setStep(4)} className="px-4 py-2 bg-brand-600 text-white text-sm rounded">
              Review
            </button>
          </div>
        </div>
      )}

      {step === 4 && (
        <div className="space-y-3">
          <div className="bg-gray-50 rounded p-4 text-sm space-y-2">
            <div className="flex justify-between">
              <span className="text-gray-500">Client ID</span>
              <span className="font-mono">{clientId}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500">Name</span>
              <span>{displayName}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500">Auth</span>
              <span>{authMethod}</span>
            </div>
            <div>
              <span className="text-gray-500">Capabilities ({capURIs.length}):</span>
              <div className="flex flex-wrap gap-1 mt-1">
                {capURIs.map(s => (
                  <span key={s} className="px-2 py-0.5 bg-gray-200 rounded text-xs">{s}</span>
                ))}
                {capURIs.length === 0 && <span className="text-gray-400 text-xs">No capabilities selected</span>}
              </div>
            </div>
          </div>

          <details className="text-sm">
            <summary className="cursor-pointer text-gray-500">View as YAML</summary>
            <pre className="mt-2 bg-gray-900 text-gray-100 p-3 rounded text-xs">
{`agents:
  - client_id: ${clientId}
    display_name: ${displayName}${description ? `\n    description: ${description}` : ''}
    auth_method: ${authMethod}
    grant_types: [${grantTypes.join(', ')}]
    allowed_caps:
${capURIs.map(s => `      - {uri: ${s}}`).join('\n') || '      # none selected'}`}
            </pre>
          </details>

          {error && <p className="text-sm text-red-600">{error}</p>}

          <div className="flex gap-2">
            <button onClick={() => setStep(3)} className="px-4 py-2 border text-sm rounded">Back</button>
            <button
              onClick={() => register.mutate({
                client_id: clientId,
                display_name: displayName,
                description: description || undefined,
                auth_method: authMethod,
                grant_types: grantTypes,
                allowed_caps: allowedCaps,
              })}
              disabled={register.isPending}
              className="px-4 py-2 bg-brand-600 text-white text-sm rounded disabled:opacity-50"
            >
              {register.isPending ? 'Registering...' : 'Register Agent'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
