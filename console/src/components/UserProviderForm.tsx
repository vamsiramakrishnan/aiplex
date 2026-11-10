import { useId, useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  overlapConflict, providerRequest, putUserProvider, unknownProviderFields,
  type DirectoryKind, type DirectorySpec, type ProviderFormValues, type UserProvider, type UserProviderPreset, type UserProviderRequest,
} from '../api/userIdentity'

const PROVIDER_ID = /^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$/
const input = 'w-full border rounded px-2 py-1 text-sm bg-transparent border-gray-300 dark:border-gray-700'

export function describeProviderError(error: unknown, action: string): string {
  const message = error instanceof Error ? error.message : 'Unexpected error'
  const code = (error as { code?: string } | null)?.code
  switch (code) {
    case 'REVISION_CONFLICT':
      return `${action}: this provider already exists or was changed by someone else. Reload the list and try again.`
    case 'SOURCE_ALREADY_CONFIGURED':
      return `${action}: another provider already trusts that issuer and audience. ${message}`
    default:
      return (error as { status?: number } | null)?.status === 403 ? `${action}: configuring providers requires the admin role.` : `${action}: ${message}`
  }
}

type ClaimMode = 'default' | 'none' | 'custom'

// Credential fields per directory kind. Every value is a reference to a tenant
// secret or identity profile, never the secret itself.
const CREDENTIAL_FIELDS: Record<DirectoryKind, { name: keyof DirectorySpec['credential']; label: string; help: string }[]> = {
  'entra-graph': [
    { name: 'client_id', label: 'App registration client ID', help: 'Application (client) ID. Not a secret.' },
    { name: 'client_secret_ref', label: 'Client secret reference', help: 'Name of the stored secret holding the client secret. Enter the reference, never the secret value.' },
  ],
  'google-cloud-identity': [
    { name: 'identity_ref', label: 'Identity profile reference', help: 'Name of a gcp-adc identity profile of this deployment.' },
  ],
  okta: [
    { name: 'api_token_ref', label: 'API token reference', help: 'Name of the stored secret holding an Okta API token. Use this OR the OAuth service app fields below.' },
    { name: 'client_id', label: 'OAuth service app client ID', help: 'For an OAuth service app instead of an API token.' },
    { name: 'private_key_ref', label: 'Private key reference', help: 'Name of the stored secret holding the service app private key.' },
    { name: 'key_id', label: 'Key ID (optional)', help: 'kid of the service app key.' },
  ],
}

/** Add (preset chosen here) or edit (preset fixed) a user identity provider. */
export default function UserProviderForm({ presets, existing, onDone, onCancel }: {
  presets: UserProviderPreset[]
  existing?: UserProvider
  onDone: (saved: UserProvider) => void
  onCancel: () => void
}) {
  const uid = useId()
  const editing = !!existing
  const [presetId, setPresetId] = useState(existing?.preset ?? '')
  const [providerId, setProviderId] = useState(existing?.id ?? '')
  const [displayName, setDisplayName] = useState(existing?.display_name ?? '')
  const [disabled, setDisabled] = useState(existing?.disabled ?? false)
  const [inputs, setInputs] = useState<Record<string, string>>(existing?.inputs ?? {})
  const [claims, setClaims] = useState(Object.entries(existing?.extra_claims ?? {}).map(([k, v]) => `${k}=${v}`).join('\n'))
  // The response carries the effective client claim, so an existing provider
  // with none must send "none" or the preset default would silently return.
  const [claimMode, setClaimMode] = useState<ClaimMode>(existing ? (existing.client_claim ? 'custom' : 'none') : 'default')
  const [claimValue, setClaimValue] = useState(existing?.client_claim ?? '')
  const [clientIds, setClientIds] = useState((existing?.client_ids ?? []).join('\n'))
  const [dirOn, setDirOn] = useState(!!existing?.directory)
  const [dir, setDir] = useState<DirectorySpec | undefined>(existing?.directory)
  const [error, setError] = useState('')
  const [overlap, setOverlap] = useState<ReturnType<typeof overlapConflict>>()
  const [pending, setPending] = useState<UserProviderRequest | null>(null)
  const preset = presets.find(p => p.id === presetId)
  const carried = existing ? Object.keys(unknownProviderFields(existing)) : []
  const dirKinds = (preset?.directory_kinds ?? []) as DirectoryKind[]
  const groupsFromAuthority = inputs.groups_from === 'membership-authority'

  const choose = (id: string) => {
    setPresetId(id)
    const next: Record<string, string> = {}
    for (const f of presets.find(p => p.id === id)?.inputs ?? []) if (f.default) next[f.name] = f.default
    setInputs(next); setClaims(''); setError(''); setOverlap(undefined)
    setClaimMode('default'); setClaimValue(''); setClientIds(''); setDirOn(false); setDir(undefined)
  }

  const save = useMutation({
    mutationFn: (body: UserProviderRequest) => putUserProvider(providerId, body, existing?.revision),
    onSuccess: onDone,
    onError: (e) => {
      const o = overlapConflict(e)
      if (o && o.code === 'IDENTITY_OVERLAPS_AGENT_BINDING') { setOverlap(o); setError(''); return }
      setOverlap(undefined)
      setError(describeProviderError(e, editing ? 'Could not save provider' : 'Could not add provider'))
    },
  })

  const missing = preset?.inputs.filter(f => f.required && !(inputs[f.name] ?? '').trim()) ?? []
  const idValid = PROVIDER_ID.test(providerId) && providerId !== 'verify'
  const claimOk = claimMode !== 'custom' || claimValue.trim() !== ''
  const dirValue = (): DirectorySpec | undefined => {
    if (!dirOn || !dir) return undefined
    const credential = Object.fromEntries(Object.entries(dir.credential).filter(([, v]) => typeof v === 'string' && v.trim()).map(([k, v]) => [k, (v as string).trim()]))
    const out: DirectorySpec = { kind: dir.kind, mode: groupsFromAuthority ? 'always' : dir.mode ?? 'fallback', credential }
    if (dir.org_url?.trim()) out.org_url = dir.org_url.trim()
    if (dir.group_id) out.group_id = dir.group_id
    return out
  }
  const dirOk = !dirOn || (!!dir && !!dir.kind)

  const build = (acknowledge: boolean): UserProviderRequest | null => {
    if (!preset) return null
    const requiredClaims: Record<string, string> = {}
    for (const line of claims.split('\n').map(l => l.trim()).filter(Boolean)) {
      const [k, ...rest] = line.split('=')
      if (!k.trim() || rest.length === 0) { setError(`Required claim "${line}" must be written key=value.`); return null }
      requiredClaims[k.trim()] = rest.join('=')
    }
    const clean: Record<string, string> = {}
    for (const [k, v] of Object.entries(inputs)) if (v.trim()) clean[k] = v.trim()
    const form: ProviderFormValues = {
      inputs: clean, display_name: displayName.trim(), disabled, required_claims: requiredClaims,
      directory: dirValue(),
      client_claim: claimMode === 'none' ? 'none' : claimMode === 'custom' ? claimValue.trim() : undefined,
      client_ids: clientIds.split('\n').map(s => s.trim()).filter(Boolean),
      acknowledge_overlap: acknowledge,
    }
    return providerRequest(preset.id, form, existing)
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const body = build(false)
    if (!body) return
    setError(''); setOverlap(undefined); setPending(body)
    save.mutate(body)
  }

  const chooseKind = (kind: DirectoryKind) => setDir({ kind, mode: 'fallback', credential: {} })
  const setCred = (name: string, value: string) => setDir(d => d && ({ ...d, credential: { ...d.credential, [name]: value } }))

  return (
    <form onSubmit={submit} aria-label={editing ? `Edit provider ${existing!.id}` : 'Add provider'} className="rounded border border-gray-200 dark:border-gray-800 p-3 space-y-3">
      <div>
        <label htmlFor={`${uid}-preset`} className="block text-xs font-medium mb-1">Provider preset</label>
        <select id={`${uid}-preset`} value={presetId} disabled={editing} onChange={e => choose(e.target.value)} className={input}>
          <option value="">Choose your identity provider…</option>
          {presets.map(p => <option key={p.id} value={p.id}>{p.display_name} ({p.vendor})</option>)}
        </select>
      </div>
      {preset && (
        <>
          <p className="text-xs text-gray-600 dark:text-gray-400">{preset.description}</p>
          <p className="text-xs text-gray-600 dark:text-gray-400">Issuer: <code>{preset.issuer_shape}</code> · Subject: <code>{preset.subject_shape}</code></p>
          {preset.tenant_pinning && <p className="text-xs text-gray-600 dark:text-gray-400">Organisation pinned by claim: <code>{preset.tenant_pinning}</code></p>}
          <p className="text-xs text-gray-600 dark:text-gray-400">Group source: {preset.group_sources.join(' or ')}</p>
          {preset.notes?.map(n => <p key={n} className="text-xs text-status-warning-fg bg-status-warning-bg rounded p-1">{n}</p>)}
          <div>
            <label htmlFor={`${uid}-id`} className="block text-xs font-medium mb-1">Provider ID <span aria-hidden="true" className="text-status-denied-fg">*</span></label>
            <input id={`${uid}-id`} value={providerId} required aria-required="true" readOnly={editing} onChange={e => setProviderId(e.target.value)} placeholder="corp-okta" className={`${input} font-mono`} />
            {providerId && !idValid && <p role="alert" className="text-xs text-status-denied-fg mt-1">Use lower-case letters, digits and hyphens (a DNS label, at most 64 characters).</p>}
          </div>
          <div>
            <label htmlFor={`${uid}-name`} className="block text-xs font-medium mb-1">Display name (optional)</label>
            <input id={`${uid}-name`} value={displayName} onChange={e => setDisplayName(e.target.value)} className={input} />
          </div>
          {preset.inputs.map(f => {
            const id = `${uid}-in-${f.name}`
            return (
              <div key={f.name}>
                <label htmlFor={id} className="block text-xs font-medium mb-1">{f.label}{f.required && <span aria-hidden="true" className="text-status-denied-fg"> *</span>}</label>
                {f.enum?.length ? (
                  <select id={id} required={f.required} aria-required={f.required} value={inputs[f.name] ?? ''} onChange={e => setInputs({ ...inputs, [f.name]: e.target.value })} className={input}>
                    <option value="">{f.required ? 'Select…' : 'Not set'}</option>
                    {f.enum.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                ) : (
                  <input id={id} required={f.required} aria-required={f.required} value={inputs[f.name] ?? ''} placeholder={f.example} onChange={e => setInputs({ ...inputs, [f.name]: e.target.value })} className={`${input} font-mono`} />
                )}
                {f.description && <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">{f.description}</p>}
              </div>
            )
          })}
          {preset.allows_required_claims && (
            <div>
              <label htmlFor={`${uid}-claims`} className="block text-xs font-medium mb-1">Extra required claims (optional, one key=value per line)</label>
              <textarea id={`${uid}-claims`} rows={2} value={claims} onChange={e => setClaims(e.target.value)} className={`${input} font-mono`} />
            </div>
          )}

          <fieldset className="border border-gray-200 dark:border-gray-800 rounded p-2 space-y-2">
            <legend className="text-xs font-semibold px-1">Client application</legend>
            <p className="text-xs text-gray-600 dark:text-gray-400">A client claim attributes requests to the application the token was issued to (for example an IDE). {preset.client_claim_default && <>Preset default: {preset.client_claim_default}</>}</p>
            <div>
              <label htmlFor={`${uid}-cmode`} className="block text-xs mb-1">Client claim</label>
              <select id={`${uid}-cmode`} value={claimMode} onChange={e => setClaimMode(e.target.value as ClaimMode)} className={input}>
                <option value="default">Use the preset default</option>
                <option value="none">None (do not attribute to a client)</option>
                <option value="custom">Custom claim name</option>
              </select>
            </div>
            {claimMode === 'custom' && (
              <div>
                <label htmlFor={`${uid}-cname`} className="block text-xs mb-1">Claim name</label>
                <input id={`${uid}-cname`} value={claimValue} onChange={e => setClaimValue(e.target.value)} placeholder="azp" className={`${input} font-mono`} />
              </div>
            )}
            <div>
              <label htmlFor={`${uid}-cids`} className="block text-xs mb-1">Allowed client IDs (optional, one per line)</label>
              <textarea id={`${uid}-cids`} rows={2} value={clientIds} onChange={e => setClientIds(e.target.value)} className={`${input} font-mono`} />
              <p className="text-xs text-gray-600 dark:text-gray-400">When set, only tokens issued to these applications are accepted; a token with no client claim is rejected.</p>
            </div>
          </fieldset>

          {dirKinds.length > 0 && (
            <fieldset className="border border-gray-200 dark:border-gray-800 rounded p-2 space-y-2">
              <legend className="text-xs font-semibold px-1">Directory groups</legend>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={dirOn} onChange={e => { setDirOn(e.target.checked); if (e.target.checked && !dir) chooseKind(dirKinds[0]) }} /> Resolve groups from the identity provider&apos;s directory
              </label>
              {dirOn && dir && (
                <div className="space-y-2">
                  <div>
                    <label htmlFor={`${uid}-dkind`} className="block text-xs mb-1">Directory kind</label>
                    <select id={`${uid}-dkind`} value={dir.kind} onChange={e => chooseKind(e.target.value as DirectoryKind)} className={input}>
                      {dirKinds.map(k => <option key={k} value={k}>{k}</option>)}
                    </select>
                  </div>
                  <div>
                    <label htmlFor={`${uid}-dmode`} className="block text-xs mb-1">Mode</label>
                    <select id={`${uid}-dmode`} value={groupsFromAuthority ? 'always' : dir.mode ?? 'fallback'} disabled={groupsFromAuthority} onChange={e => setDir({ ...dir, mode: e.target.value as 'fallback' | 'always' })} className={input}>
                      <option value="fallback">Fallback: use the token&apos;s groups, ask the directory when they are missing</option>
                      <option value="always">Always: the directory is the group authority</option>
                    </select>
                    {groupsFromAuthority && <p className="text-xs text-gray-600 dark:text-gray-400">Groups come from the membership authority, so the directory is always consulted.</p>}
                  </div>
                  {dir.kind === 'okta' && (
                    <>
                      <div>
                        <label htmlFor={`${uid}-orgurl`} className="block text-xs mb-1">Okta org URL</label>
                        <input id={`${uid}-orgurl`} value={dir.org_url ?? ''} onChange={e => setDir({ ...dir, org_url: e.target.value })} placeholder="https://acme.okta.com" className={`${input} font-mono`} />
                      </div>
                      <div>
                        <label htmlFor={`${uid}-gid`} className="block text-xs mb-1">Group identifier</label>
                        <select id={`${uid}-gid`} value={dir.group_id ?? ''} onChange={e => setDir({ ...dir, group_id: (e.target.value || undefined) as 'id' | 'name' | undefined })} className={input}>
                          <option value="">Default (immutable group ID)</option>
                          <option value="id">id: immutable group ID</option>
                          <option value="name">name: group name (matches Okta&apos;s default groups claim)</option>
                        </select>
                        <p className="text-xs text-gray-600 dark:text-gray-400">In fallback mode you must choose one.</p>
                      </div>
                    </>
                  )}
                  <p className="text-xs text-gray-600 dark:text-gray-400">Credentials are references to stored secrets or identity profiles. Never type a secret value here.</p>
                  {CREDENTIAL_FIELDS[dir.kind].map(f => (
                    <div key={f.name}>
                      <label htmlFor={`${uid}-cred-${f.name}`} className="block text-xs mb-1">{f.label}</label>
                      <input id={`${uid}-cred-${f.name}`} value={dir.credential[f.name] ?? ''} onChange={e => setCred(f.name, e.target.value)} autoComplete="off" className={`${input} font-mono`} />
                      <p className="text-xs text-gray-600 dark:text-gray-400">{f.help}</p>
                    </div>
                  ))}
                </div>
              )}
            </fieldset>
          )}

          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={disabled} onChange={e => setDisabled(e.target.checked)} /> Disabled (keep configured but do not trust)</label>
          {carried.length > 0 && (
            <p role="note" className="text-xs text-gray-600 dark:text-gray-400">This provider has additional settings this console does not edit ({carried.join(', ')}). They are kept unchanged when you save.</p>
          )}
          {error && <p role="alert" className="text-xs text-status-denied-fg">{error}</p>}
          {overlap && (
            <div role="alert" className="text-xs border border-status-warning-fg bg-status-warning-bg text-status-warning-fg rounded p-2 space-y-1">
              <p>{overlap.message}</p>
              {overlap.conflict.id && <p>Conflicting {overlap.conflict.kind ?? 'resource'}: <code>{overlap.conflict.id}</code>{overlap.conflict.issuer && <> (issuer <code>{overlap.conflict.issuer}</code>)</>}</p>}
              <p>Live authentication rejects tokens claimed by both an agent identity binding and a user identity provider. You can save anyway, knowing those tokens will be rejected.</p>
              <button type="button" disabled={save.isPending || !pending} className="px-2 py-1 border border-status-warning-fg rounded"
                onClick={() => { const body = build(true); if (body) { setOverlap(undefined); save.mutate(body) } }}>Save anyway (acknowledge overlap)</button>
            </div>
          )}
          <div className="flex gap-2">
            <button type="submit" disabled={!idValid || missing.length > 0 || !claimOk || !dirOk || save.isPending} className="px-3 py-1 text-sm bg-brand-600 text-brand-contrast rounded disabled:opacity-50">{save.isPending ? 'Saving…' : editing ? 'Save changes' : 'Save provider'}</button>
            <button type="button" onClick={onCancel} className="px-3 py-1 text-sm rounded border border-gray-300 dark:border-gray-700">Cancel</button>
          </div>
          {missing.length > 0 && <p className="text-xs text-gray-600 dark:text-gray-400">Still required: {missing.map(f => f.label).join(', ')}</p>}
        </>
      )}
    </form>
  )
}
