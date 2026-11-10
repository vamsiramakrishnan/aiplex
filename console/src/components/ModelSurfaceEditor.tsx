import { useEffect, useId, useState, type FormEvent, type ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import * as api from '../api/modelSurfaces'

const input = 'w-full rounded border border-gray-300 bg-transparent px-3 py-2 text-sm dark:border-gray-700'
const muted = 'text-xs text-gray-500 dark:text-gray-400'
const button = 'rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900'

function Labeled({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  // The hint sits beside the label, not inside it, so the control's accessible name is just the label.
  return <div className="text-sm"><label className="block"><span className="mb-1 block">{label}</span>{children}</label>{hint && <p className={`${muted} mt-1`}>{hint}</p>}</div>
}

const STREAM_USAGE_TEXT: Record<string, string> = {
  require: 'require: reject a streaming request that did not ask for usage',
  inject: 'inject: add stream_options.include_usage so streaming usage is reported',
  off: 'off: forward unchanged; streaming usage may stay unknown',
}

/**
 * Create or edit a provider model surface. The form is driven by the provider
 * descriptor the server publishes: protocols, credential schemes and the
 * operation catalog come from it. Credentials are references to a stored
 * secret or an operator identity; no secret value can be entered here.
 */
export default function ModelSurfaceEditor({ record, onClose }: { record?: api.SurfaceRecord; onClose: () => void }) {
  const uid = useId()
  const client = useQueryClient()
  const [current, setCurrent] = useState<api.SurfaceRecord | undefined>(record)
  const [draft, setDraft] = useState<api.SurfaceDraft>(() => (record ? api.draftFromSpec(record.spec) : api.blankDraft()))
  const [problems, setProblems] = useState<string[]>([])
  const [message, setMessage] = useState('')
  const [conflict, setConflict] = useState(false)
  const [busy, setBusy] = useState(false)
  const editing = !!current

  const descriptor = useQuery({ queryKey: ['model-provider', draft.provider], queryFn: () => api.getProviderDescriptor(draft.provider), retry: false })
  const d = descriptor.data
  useEffect(() => {
    if (d && !d.protocols.includes(draft.protocol)) setDraft(x => ({ ...x, protocol: d.protocols[0] ?? '' }))
  }, [d, draft.protocol])

  const set = <K extends keyof api.SurfaceDraft>(name: K, value: api.SurfaceDraft[K]) => setDraft(x => ({ ...x, [name]: value }))
  const bind = (name: keyof api.SurfaceDraft) => ({ value: draft[name] as string, onChange: (e: { target: { value: string } }) => set(name, e.target.value as never) })
  const chooseProvider = (provider: api.ProviderId) => setDraft(x => ({ ...api.blankDraft(provider), id: x.id }))
  const toggleOperation = (id: string) => setDraft(x => {
    const operationIds = x.operationIds.includes(id) ? x.operationIds.filter(o => o !== id) : [...x.operationIds, id]
    // A selection that makes token metering invalid falls back to session metering.
    return { ...x, operationIds, wsMetering: x.wsMetering === 'token' && api.tokenMeteringBlocked(operationIds) ? 'session' : x.wsMetering }
  })

  const fields = api.PROVIDER_FIELDS[draft.provider]
  const modes = api.authModes(draft.provider, d)
  const wsApplies = api.hasWebSocketOperation(d, draft.operationIds)
  const tokenBlocked = api.tokenMeteringBlocked(draft.operationIds)

  async function save(e: FormEvent) {
    e.preventDefault()
    if (busy || !d) return
    const { spec, errors } = api.specFromDraft(draft, d, current?.spec)
    setProblems(errors)
    if (!spec) return
    setBusy(true); setMessage(''); setConflict(false)
    try {
      await api.putSurface(spec, current?.revision ?? 0)
      await client.invalidateQueries({ queryKey: ['model-surfaces'] })
      onClose()
    } catch (err) {
      const status = (err as { status?: number } | null)?.status
      if (status === 409) setConflict(true)
      else setMessage(status === 403 ? 'Only administrators can change model surfaces.' : err instanceof Error ? err.message : String(err))
    } finally { setBusy(false) }
  }

  async function loadLatest() {
    setBusy(true)
    try {
      const latest = await api.getSurface(draft.id.trim())
      await client.invalidateQueries({ queryKey: ['model-surfaces'] })
      setCurrent(latest); setDraft(api.draftFromSpec(latest.spec)); setConflict(false); setProblems([])
      setMessage(`Loaded revision ${latest.revision}. Re-apply your changes and save.`)
    } catch (err) {
      if ((err as { status?: number } | null)?.status === 404) { setCurrent(undefined); setConflict(false); setMessage('This surface was deleted by someone else. Saving will create it again.') }
      else setMessage(err instanceof Error ? err.message : String(err))
    } finally { setBusy(false) }
  }

  return <form aria-label={editing ? `Edit model surface ${draft.id}` : 'Add model surface'} onSubmit={e => void save(e)} className="space-y-5 rounded border border-gray-200 p-4 dark:border-gray-800">
    <h3 className="text-lg font-medium">{editing ? `Edit model surface (revision ${current!.revision})` : 'Add model surface'}</h3>
    {problems.length > 0 && <ul role="alert" className="list-disc pl-5 text-sm text-red-700 dark:text-red-400">{problems.map(p => <li key={p}>{p}</li>)}</ul>}
    {message && <p role="alert" className="rounded border border-red-300 p-3 text-sm text-red-700 dark:border-red-800 dark:text-red-400">{message}</p>}
    {conflict && <div role="alert" className="rounded border border-amber-400 p-3 text-sm text-amber-800 dark:text-amber-300"><p>This surface was changed by someone else (or already exists), so nothing was saved.</p><button type="button" className="mt-2 underline" disabled={busy} onClick={() => void loadLatest()}>Load latest revision</button></div>}

    <div className="grid gap-3 sm:grid-cols-2">
      <Labeled label="Surface ID"><input className={input} {...bind('id')} readOnly={editing} /></Labeled>
      <Labeled label="Provider" hint="The form below follows the provider's catalog from the server.">
        <select className={input} value={draft.provider} disabled={editing} onChange={e => chooseProvider(e.target.value as api.ProviderId)}>{api.PROVIDER_IDS.map(p => <option key={p} value={p}>{api.PROVIDER_LABELS[p]}</option>)}</select>
      </Labeled>
    </div>

    {descriptor.isPending && <p role="status" className="text-sm text-gray-500">Loading the {draft.provider} catalog…</p>}
    {descriptor.isError && <p role="alert" className="text-sm text-red-700 dark:text-red-400">Could not load the provider catalog: {(descriptor.error as Error).message}</p>}

    {d && <>
      <div className="grid gap-3 sm:grid-cols-2">
        <Labeled label="Protocol"><select className={input} value={draft.protocol} onChange={e => set('protocol', e.target.value)}>{d.protocols.map(p => <option key={p}>{p}</option>)}</select></Labeled>
        <Labeled label="Origin" hint="Scheme and host only. Use the provider block for path settings."><input className={input} placeholder={api.ORIGIN_HINTS[draft.provider]} {...bind('origin')} /></Labeled>
        <Labeled label="Allowed models (one per line)"><textarea className={input} rows={3} {...bind('models')} /></Labeled>
        <Labeled label="Network profile reference (optional)"><input className={input} {...bind('networkProfileRef')} /></Labeled>
      </div>

      <fieldset className="space-y-3"><legend className="text-sm font-medium">Provider settings</legend>
        <div className="grid gap-3 sm:grid-cols-2">
          {fields.includes('base_path') && <Labeled label="Base path" hint="Path prefix of the server's OpenAI-style routes. Defaults to /v1."><input className={input} placeholder="/v1" {...bind('base_path')} /></Labeled>}
          {fields.includes('api_version') && <Labeled label="API version" hint={api.azureDeploymentSelected(draft.operationIds) ? 'Required for deployment-scoped operations.' : 'Used only by deployment-scoped operations (select one below to enable it).'}><input className={input} disabled={!api.azureDeploymentSelected(draft.operationIds)} placeholder="2024-10-21" {...bind('api_version')} /></Labeled>}
          {fields.includes('region') && <Labeled label="Signing region" hint="Must match the Bedrock Runtime origin."><input className={input} placeholder="us-east-1" {...bind('region')} /></Labeled>}
          {fields.includes('organization') && <Labeled label="Organization (optional)" hint="Pinned as OpenAI-Organization on every request."><input className={input} {...bind('organization')} /></Labeled>}
          {fields.includes('project') && <Labeled label="Project (optional)" hint="Pinned as OpenAI-Project on every request."><input className={input} {...bind('project')} /></Labeled>}
          {fields.includes('anthropic_version') && <Labeled label="Anthropic version" hint="Pinned as the anthropic-version header."><input className={input} placeholder="2023-06-01" {...bind('anthropic_version')} /></Labeled>}
          {fields.includes('anthropic_betas') && <Labeled label="Allowed beta flags (one per line)" hint="Callers may send only these anthropic-beta values."><textarea className={input} rows={2} {...bind('anthropic_betas')} /></Labeled>}
          {fields.includes('usage_semantics') && <Labeled label="Usage semantics" hint="Declare OpenAI usage semantics only if this server reports usage exactly as OpenAI does; otherwise requests are counted, not metered by tokens."><select className={input} {...bind('usage_semantics')}><option value="">Not declared (request-count metering)</option><option value="openai">openai</option></select></Labeled>}
          {fields.includes('stream_usage') && <Labeled label="Streaming usage (stream_usage)" hint={STREAM_USAGE_TEXT[draft.stream_usage || d.default_stream_usage || ''] ?? undefined}>
            <select className={input} {...bind('stream_usage')}><option value="">Provider default{d.default_stream_usage ? ` (${d.default_stream_usage})` : ''}</option>{['require', 'inject', 'off'].map(v => <option key={v} value={v}>{v}</option>)}</select>
          </Labeled>}
        </div>
        <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={draft.limit_header_passthrough} onChange={e => set('limit_header_passthrough', e.target.checked)} /><span>Pass the provider&apos;s rate-limit headers to callers<span className={`${muted} block`}>Off by default: those headers describe the shared upstream credential, not the caller.</span></span></label>
      </fieldset>

      <fieldset className="space-y-2"><legend className="text-sm font-medium">Operations</legend>
        <p className={muted}>The operations this provider offers. Callers can use only the ones selected here.</p>
        <ul className="grid gap-1 sm:grid-cols-2">{d.operations.map(o => <li key={o.id}><label className="flex items-start gap-2 text-sm">
          <input type="checkbox" checked={draft.operationIds.includes(o.id)} onChange={() => toggleOperation(o.id)} />
          <span><span className="font-mono text-xs">{o.id}</span><span className={`${muted} block`}>{o.method} {o.path} · {o.transport}</span></span>
        </label></li>)}</ul>
        {draft.operationIds.filter(id => !d.operations.some(o => o.id === id)).map(id => <p key={id} className="text-xs text-amber-700 dark:text-amber-400">{id} is selected but is not in this provider&apos;s catalog; the server will reject it unless you clear it.</p>)}
      </fieldset>

      <fieldset className="space-y-2"><legend className="text-sm font-medium">Credential (a reference, never a value)</legend>
        <p className={muted}>AIPlex looks the credential up when it calls the provider. Store the secret in your secret manager and enter only its reference name here; this form has no field for a key or token.</p>
        <div role="radiogroup" aria-label="Credential reference type" className="flex flex-wrap gap-4 text-sm">
          {modes.includes('secret') && <label className="flex items-center gap-1"><input type="radio" name={`${uid}-auth`} checked={draft.authMode === 'secret'} onChange={() => set('authMode', 'secret')} />Secret reference</label>}
          {modes.includes('identity') && <label className="flex items-center gap-1"><input type="radio" name={`${uid}-auth`} checked={draft.authMode === 'identity'} onChange={() => set('authMode', 'identity')} />Identity reference</label>}
          {modes.includes('none') && <label className="flex items-center gap-1"><input type="radio" name={`${uid}-auth`} checked={draft.authMode === 'none'} onChange={() => set('authMode', 'none')} />No credential</label>}
        </div>
        {draft.authMode === 'secret' && <Labeled label="Secret reference name" hint={d.credential_schemes.includes('api-key') && d.api_key_header ? `Sent upstream in the ${d.api_key_header} header.` : 'Sent upstream as a bearer token.'}><input className={input} autoComplete="off" {...bind('secretRef')} /></Labeled>}
        {draft.authMode === 'identity' && <Labeled label="Identity reference name" hint={`Names ${api.IDENTITY_HINT[draft.provider] ?? 'an operator identity profile'}. The profile must approve this origin.`}><input className={input} autoComplete="off" {...bind('identityRef')} /></Labeled>}
        {draft.authMode === 'none' && <p className={muted}>No credential is sent upstream. Use this only for a server inside your network that needs none.</p>}
      </fieldset>

      {wsApplies && <fieldset className="space-y-2" aria-label="WebSocket sessions"><legend className="text-sm font-medium">WebSocket sessions</legend>
        <p className={muted}><strong>Session metering</strong> admits the whole WebSocket session once, as a single request, however long it runs.</p>
        <p className={muted}><strong>Token metering</strong> admits each response (or each renewable token allowance window) by the tokens it uses.</p>
        <div className="grid gap-3 sm:grid-cols-3">
          <Labeled label="Metering" hint={tokenBlocked ? 'Token metering is unavailable for Live voice: it is billed by session seconds.' : undefined}>
            <select className={input} value={draft.wsMetering} onChange={e => set('wsMetering', e.target.value as api.SurfaceDraft['wsMetering'])}><option value="">Default (session)</option><option value="session">session</option><option value="token" disabled={tokenBlocked}>token</option></select>
          </Labeled>
          <Labeled label="Maximum session seconds (optional)" hint="60 to 86400."><input className={input} inputMode="numeric" {...bind('wsMaxSessionSeconds')} /></Labeled>
          {draft.wsMetering === 'token' && <Labeled label="Token allowance (optional)" hint="1,024 to 4,194,304 tokens per window."><input className={input} inputMode="numeric" {...bind('wsTokenAllowance')} /></Labeled>}
        </div>
      </fieldset>}

      <fieldset className="space-y-2"><legend className="text-sm font-medium">Capacity policy</legend>
        <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={draft.capacityEnabled} onChange={e => set('capacityEnabled', e.target.checked)} /><span>Enforce capacity signals from this provider<span className={`${muted} block`}>Off by default. When on, the policy applies per surface and per server replica: each replica acts only on the last provider response it saw itself, so replicas can differ briefly. It is advisory; it stops sending requests the provider just said it would refuse.</span></span></label>
        {draft.capacityEnabled && <div className="space-y-3 pl-6">
          <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={draft.cooldownEnabled} onChange={e => set('cooldownEnabled', e.target.checked)} /><span>Cooldown<span className={`${muted} block`}>Reject new requests while the provider&apos;s last response was a throttle or overload with a retry time.</span></span></label>
          <div className="grid gap-3 sm:grid-cols-3">
            <Labeled label="Maximum cooldown seconds (optional)" hint="1 to 3600; default 60."><input className={input} inputMode="numeric" disabled={!draft.cooldownEnabled} {...bind('maxCooldownSeconds')} /></Labeled>
            <Labeled label="Headroom fraction (optional)" hint="0 to 1. When remaining capacity falls below this share, low-weight callers are shed."><input className={input} inputMode="decimal" {...bind('headroomFraction')} /></Labeled>
            <Labeled label="Signal maximum age seconds (optional)" hint="1 to 3600; default 60."><input className={input} inputMode="numeric" {...bind('signalMaxAgeSeconds')} /></Labeled>
            <Labeled label="Minimum weight" hint="Lowest weight still admitted under low headroom (1 to 1000)."><input className={input} inputMode="numeric" {...bind('minWeight')} /></Labeled>
            <Labeled label="Default weight (optional)" hint="Weight of callers with no access-point weight (default 1)."><input className={input} inputMode="numeric" {...bind('defaultWeight')} /></Labeled>
          </div>
        </div>}
      </fieldset>
    </>}

    <div className="flex gap-3"><button type="submit" className={button} disabled={busy || !d}>Save surface</button><button type="button" className="underline" onClick={onClose}>Cancel</button></div>
  </form>
}
