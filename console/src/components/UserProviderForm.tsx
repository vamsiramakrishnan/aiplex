import { useId, useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  editRequest, putUserProvider, unknownProviderFields,
  type UserProvider, type UserProviderPreset, type UserProviderRequest,
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
  const [error, setError] = useState('')
  const preset = presets.find(p => p.id === presetId)
  const carried = existing ? Object.keys(unknownProviderFields(existing)) : []

  const choose = (id: string) => {
    setPresetId(id)
    const next: Record<string, string> = {}
    for (const f of presets.find(p => p.id === id)?.inputs ?? []) if (f.default) next[f.name] = f.default
    setInputs(next); setClaims(''); setError('')
  }

  const save = useMutation({
    mutationFn: (body: UserProviderRequest) => putUserProvider(providerId, body, existing?.revision),
    onSuccess: onDone,
    onError: (e) => setError(describeProviderError(e, editing ? 'Could not save provider' : 'Could not add provider')),
  })

  const missing = preset?.inputs.filter(f => f.required && !(inputs[f.name] ?? '').trim()) ?? []
  const idValid = PROVIDER_ID.test(providerId) && providerId !== 'verify'

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
    if (existing) {
      save.mutate(editRequest(existing, { inputs: clean, display_name: displayName.trim(), disabled, required_claims: requiredClaims }))
    } else {
      save.mutate({
        preset: preset.id, inputs: clean, disabled,
        ...(Object.keys(requiredClaims).length ? { required_claims: requiredClaims } : {}),
        ...(displayName.trim() ? { display_name: displayName.trim() } : {}),
      })
    }
  }

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
          <p className="text-xs text-gray-500">Issuer: <code>{preset.issuer_shape}</code> · Subject: <code>{preset.subject_shape}</code></p>
          {preset.tenant_pinning && <p className="text-xs text-gray-500">Organisation pinned by claim: <code>{preset.tenant_pinning}</code></p>}
          <p className="text-xs text-gray-500">Group source: {preset.group_sources.join(' or ')}</p>
          {preset.notes?.map(n => <p key={n} className="text-xs text-yellow-800 bg-yellow-50 dark:bg-yellow-950 dark:text-yellow-200 rounded p-1">{n}</p>)}
          <div>
            <label htmlFor={`${uid}-id`} className="block text-xs font-medium mb-1">Provider ID <span aria-hidden="true" className="text-red-600">*</span></label>
            <input id={`${uid}-id`} value={providerId} required aria-required="true" readOnly={editing} onChange={e => setProviderId(e.target.value)} placeholder="corp-okta" className={`${input} font-mono`} />
            {providerId && !idValid && <p role="alert" className="text-xs text-red-600 mt-1">Use lower-case letters, digits and hyphens (a DNS label, at most 64 characters).</p>}
          </div>
          <div>
            <label htmlFor={`${uid}-name`} className="block text-xs font-medium mb-1">Display name (optional)</label>
            <input id={`${uid}-name`} value={displayName} onChange={e => setDisplayName(e.target.value)} className={input} />
          </div>
          {preset.inputs.map(f => {
            const id = `${uid}-in-${f.name}`
            return (
              <div key={f.name}>
                <label htmlFor={id} className="block text-xs font-medium mb-1">{f.label}{f.required && <span aria-hidden="true" className="text-red-600"> *</span>}</label>
                {f.enum?.length ? (
                  <select id={id} required={f.required} aria-required={f.required} value={inputs[f.name] ?? ''} onChange={e => setInputs({ ...inputs, [f.name]: e.target.value })} className={input}>
                    <option value="">{f.required ? 'Select…' : 'Not set'}</option>
                    {f.enum.map(v => <option key={v} value={v}>{v}</option>)}
                  </select>
                ) : (
                  <input id={id} required={f.required} aria-required={f.required} value={inputs[f.name] ?? ''} placeholder={f.example} onChange={e => setInputs({ ...inputs, [f.name]: e.target.value })} className={`${input} font-mono`} />
                )}
                {f.description && <p className="text-xs text-gray-400 mt-1">{f.description}</p>}
              </div>
            )
          })}
          {preset.allows_required_claims && (
            <div>
              <label htmlFor={`${uid}-claims`} className="block text-xs font-medium mb-1">Extra required claims (optional, one key=value per line)</label>
              <textarea id={`${uid}-claims`} rows={2} value={claims} onChange={e => setClaims(e.target.value)} className={`${input} font-mono`} />
            </div>
          )}
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={disabled} onChange={e => setDisabled(e.target.checked)} /> Disabled (keep configured but do not trust)</label>
          {carried.length > 0 && (
            <p role="note" className="text-xs text-gray-500">This provider has additional settings this console does not edit ({carried.join(', ')}). They are kept unchanged when you save.</p>
          )}
          {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
          <div className="flex gap-2">
            <button type="submit" disabled={!idValid || missing.length > 0 || save.isPending} className="px-3 py-1 text-sm bg-brand-600 text-white rounded disabled:opacity-50">{save.isPending ? 'Saving…' : editing ? 'Save changes' : 'Save provider'}</button>
            <button type="button" onClick={onCancel} className="px-3 py-1 text-sm rounded border border-gray-300 dark:border-gray-700">Cancel</button>
          </div>
          {missing.length > 0 && <p className="text-xs text-gray-500">Still required: {missing.map(f => f.label).join(', ')}</p>}
        </>
      )}
    </form>
  )
}
