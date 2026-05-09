import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { getCatalog } from '../api/client'
import StatusBadge from '../components/StatusBadge'

interface SkillServer {
  instance_id: string
  name: string
  url: string
  skill_bundle?: string
  skills: string[]
  status: string
}

interface SkillInvocation {
  id: string
  agent_id: string
  instance_id: string
  skill_name: string
  user_id?: string
  status: string
  started_at: string
  duration_ms?: number
  trace_id?: string
}

const getServers = () =>
  fetch('/api/v1/skills/servers').then((r) => r.json()) as Promise<SkillServer[]>
const getInvocations = () =>
  fetch('/api/v1/skills/invocations').then((r) => r.json()) as Promise<SkillInvocation[]>

export default function SkillsPlex() {
  const navigate = useNavigate()
  const [tab, setTab] = useState<'servers' | 'catalog' | 'invocations'>('servers')
  const [search, setSearch] = useState('')

  const catalog = useQuery({ queryKey: ['catalog', 'skill'], queryFn: () => getCatalog('skill') })
  const servers = useQuery({ queryKey: ['skill-servers'], queryFn: getServers })
  const invocations = useQuery({ queryKey: ['skill-invocations'], queryFn: getInvocations })

  const filteredServers =
    servers.data?.filter(
      (s) =>
        s.name?.toLowerCase().includes(search.toLowerCase()) ||
        s.instance_id?.toLowerCase().includes(search.toLowerCase()),
    ) || []

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h2 className="text-2xl font-bold">SkillsPlex</h2>
        <button
          onClick={() => navigate('/deploy?kind=skill')}
          className="px-3 py-1.5 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
        >
          Deploy Skill Server
        </button>
      </div>
      <p className="text-gray-500 mb-6">Agent &harr; Skill (skill bundles served by skill servers)</p>

      <div className="flex gap-2 mb-6">
        {(['servers', 'catalog', 'invocations'] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 rounded text-sm capitalize ${
              tab === t ? 'bg-brand-600 text-white' : 'bg-gray-100'
            }`}
          >
            {t === 'servers'
              ? `Servers (${servers.data?.length ?? 0})`
              : t === 'catalog'
              ? `Catalog (${catalog.data?.total ?? 0})`
              : `Invocations (${invocations.data?.length ?? 0})`}
          </button>
        ))}
      </div>

      {tab === 'servers' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Running Skill Servers</h3>
          <div className="mb-4">
            <input
              type="text"
              placeholder="Search skill servers..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
            />
          </div>
          {filteredServers.length > 0 ? (
            <div className="grid grid-cols-2 gap-4">
              {filteredServers.map((s) => (
                <div key={s.instance_id} className="bg-white rounded-lg shadow p-4">
                  <div className="flex items-center justify-between mb-2">
                    <h4 className="font-semibold">{s.name || s.instance_id}</h4>
                    <StatusBadge status={s.status} />
                  </div>
                  <p className="text-xs text-gray-400 mb-2 font-mono">{s.url}</p>
                  {s.skill_bundle && (
                    <p className="text-xs text-gray-500 mb-2">
                      Bundle: <span className="font-mono">{s.skill_bundle}</span>
                    </p>
                  )}
                  <div className="flex gap-1 flex-wrap">
                    {s.skills?.map((skill: string) => (
                      <span
                        key={skill}
                        className="px-1.5 py-0.5 bg-amber-50 text-amber-700 text-xs rounded"
                      >
                        {skill}
                      </span>
                    ))}
                  </div>
                  <a
                    href={`${s.url}/.well-known/skills.json`}
                    className="text-xs text-brand-600 hover:underline mt-2 block"
                  >
                    View Skills Manifest
                  </a>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-gray-500 text-sm">
              {servers.data?.length === 0 ? (
                <div className="bg-white rounded-lg shadow p-6 text-center">
                  <p className="mb-3">No skill servers deployed yet.</p>
                  <button
                    onClick={() => setTab('catalog')}
                    className="px-3 py-1.5 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
                  >
                    Browse catalog
                  </button>
                </div>
              ) : (
                <p>No servers match your search.</p>
              )}
            </div>
          )}
        </div>
      )}

      {tab === 'catalog' && (
        <div className="grid grid-cols-2 gap-4">
          {catalog.data?.templates?.map((t: { id: string; name: string; description?: string; verified?: boolean }) => (
            <div key={t.id} className="bg-white rounded-lg shadow p-4">
              <div className="flex items-center justify-between mb-2">
                <h3 className="font-semibold">{t.name}</h3>
                {t.verified && <span className="text-xs text-green-600">Verified</span>}
              </div>
              <p className="text-sm text-gray-500 mb-3">{t.description}</p>
              <button
                onClick={() => navigate(`/deploy?kind=skill&template=${t.id}`)}
                className="px-3 py-1.5 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
              >
                Deploy
              </button>
            </div>
          ))}
        </div>
      )}

      {tab === 'invocations' && (
        <div>
          <h3 className="font-semibold text-lg mb-3">Recent Invocations</h3>
          {invocations.data && invocations.data.length > 0 ? (
            <div className="bg-white rounded-lg shadow">
              <table className="w-full text-sm">
                <thead className="bg-gray-50">
                  <tr>
                    <th className="text-left px-4 py-3">Agent</th>
                    <th className="text-left px-4 py-3">Skill</th>
                    <th className="text-left px-4 py-3">Server</th>
                    <th className="text-left px-4 py-3">Status</th>
                    <th className="text-left px-4 py-3">Duration</th>
                    <th className="text-left px-4 py-3">Trace</th>
                    <th className="text-left px-4 py-3">User</th>
                  </tr>
                </thead>
                <tbody>
                  {invocations.data.map((inv) => (
                    <tr key={inv.id} className="border-t">
                      <td className="px-4 py-3 font-mono text-xs">{inv.agent_id}</td>
                      <td className="px-4 py-3">
                        <span className="px-1.5 py-0.5 bg-amber-50 text-amber-700 text-xs rounded">
                          {inv.skill_name}
                        </span>
                      </td>
                      <td className="px-4 py-3 font-mono text-xs">{inv.instance_id}</td>
                      <td className="px-4 py-3">
                        <StatusBadge status={inv.status} />
                      </td>
                      <td className="px-4 py-3 text-gray-500">
                        {inv.duration_ms ? `${inv.duration_ms}ms` : '-'}
                      </td>
                      <td className="px-4 py-3 text-gray-500 font-mono text-xs">
                        {inv.trace_id ? inv.trace_id.slice(0, 8) : '-'}
                      </td>
                      <td className="px-4 py-3 text-gray-500 text-xs">{inv.user_id ?? '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="text-gray-400 text-sm">No skill invocations recorded.</p>
          )}
        </div>
      )}
    </div>
  )
}
