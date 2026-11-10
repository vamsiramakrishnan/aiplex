import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  deleteAgentIdentity, identityErrorCode, listAgentIdentities, listIdentityPresets, planAgentIdentity, putAgentIdentity,
  verifyAgentIdentityToken,
  type IdentityBindingRequest,
  type IdentityBinding, type IdentityPlan, type IdentityPreset, type IdentityStep, type IdentityVerifyResult,
} from '../api/agentIdentity'

import { overlapConflict } from '../api/userIdentity'

const BINDING_ID = /^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$/

function describeError(error: unknown, action: string): string {
  const message = error instanceof Error ? error.message : 'Unexpected error'
  switch (identityErrorCode(error)) {
    case 'REVISION_CONFLICT':
      return `${action}: this binding already exists or was changed by someone else. Refresh the list and try again.`
    case 'SUBJECT_ALREADY_BOUND':
      return `${action}: that external identity is already bound. ${message}`
    case 'AGENT_NOT_ACTIVE':
      return `${action}: the agent registration is not active. Activate it before binding an identity.`
    case 'IDENTITY_OVERLAPS_USER_PROVIDER':
      return `${action}: ${message}`
    case 'AGENT_NOT_FOUND':
      return `${action}: the agent is not registered.`
    default:
      return `${action}: ${message}`
  }
}

export default function AgentIdentities({ agentId }: { agentId: string }) {
  const queryClient = useQueryClient()
  const presets = useQuery({ queryKey: ['agent-identity-presets'], queryFn: listIdentityPresets })
  const bindings = useQuery({ queryKey: ['agent-identities', agentId], queryFn: () => listAgentIdentities(agentId) })
  const [adding, setAdding] = useState(false)
  const [plan, setPlan] = useState<IdentityPlan | null>(null)
  const [testing, setTesting] = useState<string | null>(null) // '' = any binding, otherwise a binding id
  const [notice, setNotice] = useState('')
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['agent-identities', agentId] })

  const planMutation = useMutation({
    mutationFn: (id: string) => planAgentIdentity(agentId, id),
    onSuccess: setPlan,
  })
  const remove = useMutation({
    mutationFn: (b: IdentityBinding) => deleteAgentIdentity(agentId, b.id, b.revision),
    onSuccess: (_data, b) => { setNotice(`Removed identity ${b.id}.`); if (plan?.binding.id === b.id) setPlan(null); refresh() },
    onError: () => refresh(),
  })

  const catalog = presets.data
  const presetFor = (id: string) => catalog?.presets.find(p => p.id === id)

  return (
    <section aria-label={`Identities for ${agentId}`} className="mt-4 border-t pt-3">
      <div className="flex items-center justify-between mb-2">
        <h4 className="text-sm font-semibold">Identities</h4>
        <div className="flex gap-2">
          <button type="button" className="text-xs text-brand-600 hover:underline" onClick={() => setTesting(testing === null ? '' : null)}>
            {testing === null ? 'Test a token' : 'Close token test'}
          </button>
          <button type="button" className="px-3 py-1 text-xs bg-brand-600 text-white rounded disabled:opacity-50" disabled={!catalog} onClick={() => setAdding(!adding)}>
            {adding ? 'Cancel' : 'Add identity'}
          </button>
        </div>
      </div>
      <p className="text-xs text-gray-600 dark:text-gray-400 mb-2">
        Let this agent authenticate from another cloud with its native workload identity instead of a stored secret.
      </p>

      {catalog && !catalog.live_authentication && (
        <p role="status" className="text-xs bg-status-warning-bg border border-status-warning-fg text-status-warning-fg rounded p-2 mb-2">
          Live authentication is not enabled on this server. You can create bindings and test tokens against them, but they do not yet authenticate real requests.
        </p>
      )}
      {presets.isLoading && <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Loading identity presets…</p>}
      {presets.error && <p role="alert" className="text-xs text-status-denied-fg">Could not load identity presets: {presets.error.message}</p>}
      {notice && <p role="status" className="text-xs text-status-ok-fg mb-2">{notice}</p>}

      {adding && catalog && (
        <AddIdentityForm
          agentId={agentId}
          presets={catalog.presets}
          onDone={(binding) => { setAdding(false); setNotice(`Added identity ${binding.id}.${binding.warnings?.length ? ' Warning: ' + binding.warnings.join(' ') : ''}`); refresh() }}
        />
      )}

      {bindings.isLoading && <p role="status" className="text-xs text-gray-600 dark:text-gray-400">Loading identities…</p>}
      {bindings.error && <p role="alert" className="text-xs text-status-denied-fg">Could not load identities: {bindings.error.message}</p>}
      {remove.error && <p role="alert" className="text-xs text-status-denied-fg">{describeError(remove.error, 'Could not remove identity')}</p>}
      {planMutation.error && <p role="alert" className="text-xs text-status-denied-fg">{describeError(planMutation.error, 'Could not build the setup plan')}</p>}
      {!bindings.isLoading && !bindings.error && bindings.data?.length === 0 && (
        <p className="text-xs text-gray-600 dark:text-gray-400">No identities bound yet. Add one to let this agent authenticate from GCP, AWS, Azure, GitHub Actions, Kubernetes or any OIDC issuer.</p>
      )}
      {!!bindings.data?.length && (
        <table className="w-full text-xs mb-2">
          <thead>
            <tr className="text-left text-gray-600 dark:text-gray-400">
              <th className="py-1 pr-2">Binding</th><th className="pr-2">Preset</th><th className="pr-2">Cloud</th>
              <th className="pr-2">Issuer</th><th className="pr-2">Subject</th><th className="pr-2">State</th><th />
            </tr>
          </thead>
          <tbody>
            {bindings.data.map(b => {
              const preset = presetFor(b.preset)
              return (
                <tr key={b.id} className="border-t align-top">
                  <td className="py-1 pr-2 font-mono">{b.display_name ? `${b.display_name} (${b.id})` : b.id}</td>
                  <td className="pr-2">{preset?.display_name ?? b.preset}</td>
                  <td className="pr-2">{preset?.cloud ?? '—'}</td>
                  <td className="pr-2 font-mono break-all">{b.issuer}</td>
                  <td className="pr-2 font-mono break-all">{b.subject}</td>
                  <td className="pr-2">{b.disabled ? <span className="text-gray-600 dark:text-gray-400">Disabled</span> : <span className="text-status-ok-fg">Enabled</span>}</td>
                  <td className="whitespace-nowrap">
                    <button type="button" className="mr-2 text-brand-600 hover:underline" aria-label={`Show setup plan for ${b.id}`} onClick={() => planMutation.mutate(b.id)}>Plan</button>
                    <button type="button" className="mr-2 text-brand-600 hover:underline" aria-label={`Test a token against ${b.id}`} onClick={() => setTesting(b.id)}>Test</button>
                    <button
                      type="button"
                      className="text-status-denied-fg hover:text-status-denied-fg"
                      aria-label={`Remove identity ${b.id}`}
                      onClick={() => { if (confirm(`Remove identity ${b.id} from ${agentId}?`)) remove.mutate(b) }}
                    >Remove</button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}

      {plan && <PlanView plan={plan} onClose={() => setPlan(null)} />}
      {testing !== null && <TokenTest agentId={agentId} bindings={bindings.data ?? []} initialBinding={testing} />}
    </section>
  )
}

function AddIdentityForm({ agentId, presets, onDone }: { agentId: string; presets: IdentityPreset[]; onDone: (b: IdentityBinding) => void }) {
  const uid = useId()
  const [presetId, setPresetId] = useState('')
  const [bindingId, setBindingId] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [claims, setClaims] = useState('')
  const [error, setError] = useState('')
  // A 409 overlap names the user identity provider that trusts the same issuer
  // and audience. Saving anyway is an explicit second step that sends
  // acknowledge_overlap for this one request only.
  const [overlap, setOverlap] = useState<ReturnType<typeof overlapConflict>>()
  const [pending, setPending] = useState<IdentityBindingRequest | null>(null)
  const preset = presets.find(p => p.id === presetId)

  const choose = (id: string) => {
    setPresetId(id)
    const next: Record<string, string> = {}
    for (const f of presets.find(p => p.id === id)?.inputs ?? []) if (f.default) next[f.name] = f.default
    setInputs(next)
    setClaims('')
    setError('')
  }

  const save = useMutation({
    mutationFn: (body: Parameters<typeof putAgentIdentity>[2]) => putAgentIdentity(agentId, bindingId, body),
    onSuccess: onDone,
    onError: (e) => {
      const o = overlapConflict(e)
      if (o && o.code === 'IDENTITY_OVERLAPS_USER_PROVIDER') { setOverlap(o); setError(''); return }
      setOverlap(undefined)
      setError(describeError(e, 'Could not add identity'))
    },
  })

  const missing = preset?.inputs.filter(f => f.required && !(inputs[f.name] ?? '').trim()) ?? []
  const idValid = BINDING_ID.test(bindingId)

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!preset) return
    const requiredClaims: Record<string, string> = {}
    for (const line of claims.split('\n').map(l => l.trim()).filter(Boolean)) {
      const [k, ...rest] = line.split('=')
      if (!k.trim() || rest.length === 0) { setError(`Required claim "${line}" must be written key=value.`); return }
      requiredClaims[k.trim()] = rest.join('=')
    }
    const clean: Record<string, string> = {}
    for (const [k, v] of Object.entries(inputs)) if (v.trim()) clean[k] = v.trim()
    setError('')
    setOverlap(undefined)
    const body: IdentityBindingRequest = {
      preset: preset.id,
      inputs: clean,
      ...(Object.keys(requiredClaims).length ? { required_claims: requiredClaims } : {}),
      ...(displayName.trim() ? { display_name: displayName.trim() } : {}),
    }
    setPending(body)
    save.mutate(body)
  }

  return (
    <form onSubmit={submit} aria-label="Add identity" className="bg-white border rounded p-3 mb-3 space-y-3">
      <div>
        <label htmlFor={`${uid}-preset`} className="block text-xs font-medium mb-1">Identity preset</label>
        <select id={`${uid}-preset`} value={presetId} onChange={e => choose(e.target.value)} className="w-full border rounded px-2 py-1 text-sm">
          <option value="">Choose where the agent runs…</option>
          {presets.map(p => <option key={p.id} value={p.id}>{p.display_name} ({p.cloud})</option>)}
        </select>
      </div>
      {preset && (
        <>
          <p className="text-xs text-gray-600">{preset.description}</p>
          <p className="text-xs text-gray-600 dark:text-gray-400">Subject: <code>{preset.subject_shape}</code> · Issuer: <code>{preset.issuer_shape}</code></p>
          {preset.notes?.map(n => <p key={n} className="text-xs text-status-warning-fg bg-status-warning-bg rounded p-1">{n}</p>)}
          <div>
            <label htmlFor={`${uid}-id`} className="block text-xs font-medium mb-1">Binding ID <span aria-hidden="true" className="text-status-denied-fg">*</span></label>
            <input id={`${uid}-id`} value={bindingId} required aria-required="true" onChange={e => setBindingId(e.target.value)} placeholder="github-ci" className="w-full border rounded px-2 py-1 text-sm font-mono" />
            {bindingId && !idValid && <p className="text-xs text-status-denied-fg mt-1">Use lower-case letters, digits and hyphens (a DNS label, at most 64 characters).</p>}
          </div>
          <div>
            <label htmlFor={`${uid}-name`} className="block text-xs font-medium mb-1">Display name (optional)</label>
            <input id={`${uid}-name`} value={displayName} onChange={e => setDisplayName(e.target.value)} className="w-full border rounded px-2 py-1 text-sm" />
          </div>
          {preset.inputs.map(f => {
            const id = `${uid}-in-${f.name}`
            const label = <>{f.label}{f.required && <span aria-hidden="true" className="text-status-denied-fg"> *</span>}</>
            return (
              <div key={f.name}>
                <label htmlFor={id} className="block text-xs font-medium mb-1">{label}</label>
                {f.enum?.length ? (
                  <select id={id} required={f.required} aria-required={f.required} value={inputs[f.name] ?? ''} onChange={e => setInputs({ ...inputs, [f.name]: e.target.value })} className="w-full border rounded px-2 py-1 text-sm">
                    <option value="">{f.required ? 'Select…' : 'Not set'}</option>
                    {f.enum.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                ) : (
                  <input id={id} required={f.required} aria-required={f.required} value={inputs[f.name] ?? ''} placeholder={f.example} onChange={e => setInputs({ ...inputs, [f.name]: e.target.value })} className="w-full border rounded px-2 py-1 text-sm font-mono" />
                )}
                {f.description && <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">{f.description}</p>}
              </div>
            )
          })}
          {preset.allows_required_claims && (
            <div>
              <label htmlFor={`${uid}-claims`} className="block text-xs font-medium mb-1">Extra required claims (optional, one key=value per line)</label>
              <textarea id={`${uid}-claims`} rows={2} value={claims} onChange={e => setClaims(e.target.value)} className="w-full border rounded px-2 py-1 text-sm font-mono" />
            </div>
          )}
          {error && <p role="alert" className="text-xs text-status-denied-fg">{error}</p>}
          {overlap && (
            <div role="alert" className="text-xs border border-status-warning-fg bg-status-warning-bg text-status-warning-fg rounded p-2 space-y-1">
              <p>{overlap.message}</p>
              {overlap.conflict.id && <p>Conflicting {overlap.conflict.kind ?? 'resource'}: <code>{overlap.conflict.id}</code>{overlap.conflict.issuer && <> (issuer <code>{overlap.conflict.issuer}</code>)</>}</p>}
              <p>Live authentication rejects tokens claimed by both an agent identity binding and a user identity provider. You can save anyway, knowing those tokens will be rejected.</p>
              <button type="button" disabled={save.isPending || !pending} onClick={() => pending && save.mutate({ ...pending, acknowledge_overlap: true })} className="px-2 py-1 border border-status-warning-fg rounded">Save anyway (acknowledge overlap)</button>
            </div>
          )}
          <button type="submit" disabled={!idValid || missing.length > 0 || save.isPending} className="px-3 py-1 text-sm bg-brand-600 text-white rounded disabled:opacity-50">
            {save.isPending ? 'Saving…' : 'Save identity'}
          </button>
          {missing.length > 0 && <p className="text-xs text-gray-600 dark:text-gray-400">Still required: {missing.map(f => f.label).join(', ')}</p>}
        </>
      )}
    </form>
  )
}

function CodeBlock({ step }: { step: IdentityStep }) {
  const [copied, setCopied] = useState(false)
  if (!step.content) return null
  const copy = async () => {
    try { await navigator.clipboard.writeText(step.content ?? ''); setCopied(true); setTimeout(() => setCopied(false), 2000) } catch { setCopied(false) }
  }
  return (
    <div className="relative mt-1">
      <pre className="bg-gray-900 text-gray-100 text-xs rounded p-2 overflow-x-auto" data-language={step.language}><code>{step.content}</code></pre>
      <button type="button" onClick={copy} aria-label={`Copy code for ${step.title}`} className="absolute top-1 right-1 text-xs bg-gray-700 text-white rounded px-2 py-0.5">
        {copied ? 'Copied' : 'Copy'}
      </button>
    </div>
  )
}

function StepList({ steps, numbered }: { steps: IdentityStep[]; numbered?: boolean }) {
  return (
    <ol className={`space-y-3 ${numbered ? 'list-decimal ml-5' : 'list-none'}`}>
      {steps.map((s, i) => (
        <li key={`${i}-${s.title}`}>
          <div className="text-sm font-medium">{s.title}</div>
          {s.description && <p className="text-xs text-gray-600">{s.description}</p>}
          <CodeBlock step={s} />
        </li>
      ))}
    </ol>
  )
}

function PlanView({ plan, onClose }: { plan: IdentityPlan; onClose: () => void }) {
  return (
    <section aria-label={`Setup plan for ${plan.binding.id}`} className="bg-white border rounded p-3 mb-3 space-y-3">
      <div className="flex justify-between">
        <h5 className="text-sm font-semibold">Setup plan: {plan.binding.id} ({plan.cloud})</h5>
        <button type="button" className="text-xs text-gray-600 dark:text-gray-400" onClick={onClose}>Close plan</button>
      </div>
      <dl className="text-xs grid grid-cols-[auto,1fr] gap-x-3 gap-y-1">
        <dt className="text-gray-600 dark:text-gray-400">Issuer</dt><dd className="font-mono break-all">{plan.issuer}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Subject</dt><dd className="font-mono break-all">{plan.subject}</dd>
        <dt className="text-gray-600 dark:text-gray-400">Audience</dt><dd className="font-mono break-all">{plan.audience}</dd>
      </dl>
      <div><h6 className="text-xs font-semibold uppercase text-gray-600 dark:text-gray-400 mb-1">In the other cloud</h6><StepList steps={plan.steps} numbered /></div>
      {plan.token_snippet?.title && <div><h6 className="text-xs font-semibold uppercase text-gray-600 dark:text-gray-400 mb-1">Getting a token</h6><StepList steps={[plan.token_snippet]} /></div>}
      {!!plan.aiplex_steps?.length && <div><h6 className="text-xs font-semibold uppercase text-gray-600 dark:text-gray-400 mb-1">In AIPlex</h6><StepList steps={plan.aiplex_steps} numbered /></div>}
      {!!plan.notes?.length && <ul className="list-disc ml-5 text-xs text-gray-600">{plan.notes.map(n => <li key={n}>{n}</li>)}</ul>}
    </section>
  )
}

const CHECK_LABEL = { pass: 'Pass', fail: 'Fail', skipped: 'Skipped' } as const
const CHECK_STYLE = { pass: 'text-status-ok-fg', fail: 'text-status-denied-fg', skipped: 'text-gray-600 dark:text-gray-400' } as const

function TokenTest({ agentId, bindings, initialBinding }: { agentId: string; bindings: IdentityBinding[]; initialBinding: string }) {
  const uid = useId()
  // The token lives only in this state and the request body: never in storage,
  // the URL, query/mutation caches, or logs. It is cleared as soon as it is sent.
  const [token, setToken] = useState('')
  const [binding, setBinding] = useState(initialBinding)
  const [result, setResult] = useState<IdentityVerifyResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const run = useRef(0)
  useEffect(() => setBinding(initialBinding), [initialBinding])
  useEffect(() => () => { run.current++ }, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const submitted = token.trim()
    if (!submitted || busy) return
    setToken('')
    setResult(null)
    setError('')
    setBusy(true)
    const id = ++run.current
    try {
      const res = await verifyAgentIdentityToken(submitted, binding ? { client_id: agentId, binding_id: binding } : undefined)
      if (id === run.current) setResult(res)
    } catch (err) {
      if (id === run.current) setError(describeError(err, 'Could not test the token'))
    } finally {
      if (id === run.current) setBusy(false)
    }
  }

  return (
    <section aria-label="Test a token" className="bg-white border rounded p-3 mb-3 space-y-2">
      <h5 className="text-sm font-semibold">Test a token</h5>
      <p className="text-xs text-gray-600 dark:text-gray-400">Paste a token acquired in the other cloud to see which checks pass. It is sent once for this test, never stored, and the box is cleared on submit.</p>
      <form onSubmit={submit} className="space-y-2">
        <div>
          <label htmlFor={`${uid}-binding`} className="block text-xs font-medium mb-1">Check against</label>
          <select id={`${uid}-binding`} value={binding} onChange={e => setBinding(e.target.value)} className="border rounded px-2 py-1 text-sm">
            <option value="">Any binding</option>
            {bindings.map(b => <option key={b.id} value={b.id}>{b.id}</option>)}
          </select>
        </div>
        <div>
          <label htmlFor={`${uid}-token`} className="block text-xs font-medium mb-1">Workload token</label>
          <textarea
            id={`${uid}-token`}
            rows={3}
            value={token}
            onChange={e => setToken(e.target.value)}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            spellCheck={false}
            className="w-full border rounded px-2 py-1 text-xs font-mono"
          />
        </div>
        <button type="submit" disabled={!token.trim() || busy} className="px-3 py-1 text-sm bg-brand-600 text-white rounded disabled:opacity-50">
          {busy ? 'Testing…' : 'Test token'}
        </button>
      </form>
      {error && <p role="alert" className="text-xs text-status-denied-fg">{error}</p>}
      {result && (
        <div aria-label="Token test result" className="space-y-2">
          <p className={`text-sm font-medium ${result.valid ? 'text-status-ok-fg' : 'text-status-denied-fg'}`}>
            {result.valid ? 'Token is valid for this identity.' : 'Token is not valid.'}
          </p>
          <ul className="text-xs space-y-1">
            {result.checks.map((c, i) => (
              <li key={`${i}-${c.name}`} className="flex gap-2">
                <span className={`w-14 font-semibold ${CHECK_STYLE[c.status] ?? ''}`}>{CHECK_LABEL[c.status] ?? c.status}</span>
                <span className="font-mono">{c.name}</span>
                <span className="text-gray-600">{c.reason}</span>
              </li>
            ))}
          </ul>
          {result.valid && result.principal_id && <p className="text-xs text-gray-600">Authenticates as <code>{result.principal_id}</code>{result.actor_id && <> acting for <code>{result.actor_id}</code></>}.</p>}
          {!result.live_authentication && <p className="text-xs text-status-warning-fg">Live authentication is off: this is a dry run and the token would not yet authenticate real requests.</p>}
        </div>
      )}
    </section>
  )
}
