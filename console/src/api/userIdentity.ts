import { authenticatedRequest } from './client'
import type { IdentityField, IdentityStep, IdentityCheck } from './agentIdentity'

// Workforce user identity providers. Contract: internal/api/user_identity_providers.go.

export type { IdentityField, IdentityStep, IdentityCheck }

export interface UserProviderPreset {
  id: string
  display_name: string
  vendor: string
  description: string
  inputs: IdentityField[]
  issuer_shape: string
  subject_shape: string
  tenant_pinning?: string
  group_sources: string[]
  allows_required_claims: boolean
  algorithms: string[]
  notes?: string[]
  operator_supplied?: string[]
}
export interface UserPresetCatalog { presets: UserProviderPreset[]; live_authentication: boolean }

/** Known fields are typed; anything else the server adds is carried through untouched. */
export interface UserProvider {
  id: string
  preset: string
  inputs: Record<string, string>
  display_name?: string
  disabled: boolean
  issuer: string
  jwks_url: string
  audience: string
  subject_claim: string
  required_claims?: Record<string, string>
  extra_claims?: Record<string, string>
  revision: number
  principal_pool: string
  [extra: string]: unknown
}

/** PUT body: the request fields the server accepts, plus any extra fields carried through. */
export interface UserProviderRequest {
  preset: string
  inputs: Record<string, string>
  required_claims?: Record<string, string>
  display_name?: string
  disabled?: boolean
  [extra: string]: unknown
}

// Fields the server derives or owns. Everything else on a provider is treated
// as request data and sent back unchanged on edit, so optional fields added to
// the contract later (directory, client_claim, client_ids, acknowledgements)
// are not silently dropped by a console that has not learned them yet.
const SERVER_OWNED = new Set(['id', 'revision', 'principal_pool', 'issuer', 'jwks_url', 'audience', 'subject_claim', 'required_claims', 'extra_claims'])
const FORM_OWNED = new Set(['preset', 'inputs', 'display_name', 'disabled'])

/** Fields on a provider that this console has no form control for. */
export function unknownProviderFields(p: UserProvider): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(p)) if (!SERVER_OWNED.has(k) && !FORM_OWNED.has(k)) out[k] = v
  return out
}

export interface UserPlan {
  provider: { id: string; preset: string }
  vendor: string
  issuer: string
  audience: string
  subject_claim: string
  groups_from: string
  group_claim?: string
  principal_pool: string
  steps: IdentityStep[]
  token_snippet: IdentityStep
  aiplex_steps: IdentityStep[]
  notes?: string[]
}

export interface UserVerifyResult {
  valid: boolean
  checks: IdentityCheck[]
  issuer?: string
  subject?: string
  audience?: string[]
  provider?: { id: string; preset: string }
  groups_available: boolean
  groups?: string[]
  groups_note?: string
  principal_id?: string
  principal_pool?: string
  is_workforce: boolean
  live_authentication: boolean
}

const base = '/api/v1/identity'
const ifMatch = (revision: number) => ({ 'If-Match': `"${revision}"` })

export const listUserProviderPresets = () => authenticatedRequest<UserPresetCatalog>(`${base}/user-provider-presets`)
export async function listUserProviders(): Promise<UserProvider[]> {
  const r = await authenticatedRequest<UserProvider[] | null>(`${base}/user-providers`)
  return r ?? []
}
export const putUserProvider = (id: string, body: UserProviderRequest, revision?: number) =>
  authenticatedRequest<UserProvider>(`${base}/user-providers/${encodeURIComponent(id)}`, {
    method: 'PUT', body: JSON.stringify(body), headers: revision ? ifMatch(revision) : undefined,
  })
export const deleteUserProvider = (id: string, revision: number) =>
  authenticatedRequest<void>(`${base}/user-providers/${encodeURIComponent(id)}`, { method: 'DELETE', headers: ifMatch(revision) })
export const planUserProvider = (id: string) =>
  authenticatedRequest<UserPlan>(`${base}/user-providers/${encodeURIComponent(id)}/plan`, { method: 'POST' })

/** Dry-runs a user token. The token is sent once in the request body and not retained here. */
export const verifyUserToken = (token: string, providerId?: string) =>
  authenticatedRequest<UserVerifyResult>(`${base}/user-providers/verify`, {
    method: 'POST', body: JSON.stringify({ token, ...(providerId ? { provider_id: providerId } : {}) }),
  })

/** Builds the PUT body for an edit: form values win, unknown fields are preserved. */
export function editRequest(existing: UserProvider, form: { inputs: Record<string, string>; display_name: string; disabled: boolean; required_claims: Record<string, string> }): UserProviderRequest {
  return {
    ...unknownProviderFields(existing),
    preset: existing.preset,
    inputs: form.inputs,
    ...(Object.keys(form.required_claims).length ? { required_claims: form.required_claims } : {}),
    ...(form.display_name ? { display_name: form.display_name } : {}),
    disabled: form.disabled,
  }
}
