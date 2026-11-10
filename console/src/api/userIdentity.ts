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
  /** Documents the preset's default client claim, or why there is none. */
  client_claim_default?: string
  /** Directory kinds this preset accepts; absent or empty means no directory. */
  directory_kinds?: string[]
}

export type DirectoryKind = 'entra-graph' | 'google-cloud-identity' | 'okta'
export const DIRECTORY_KINDS: readonly DirectoryKind[] = ['entra-graph', 'google-cloud-identity', 'okta']
/** References to tenant secrets / identity profiles only; never secret values. */
export interface DirectoryCredential {
  client_id?: string
  client_secret_ref?: string
  identity_ref?: string
  api_token_ref?: string
  private_key_ref?: string
  key_id?: string
}
export interface DirectorySpec {
  kind: DirectoryKind
  mode?: 'fallback' | 'always'
  credential: DirectoryCredential
  org_url?: string
  group_id?: 'id' | 'name'
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
  groups_from?: string
  group_claim?: string
  directory?: DirectorySpec
  client_claim?: string
  client_ids?: string[]
  revision: number
  principal_pool: string
  /** Response-only: present on writes that succeeded with an acknowledged risk. Never sent back. */
  warnings?: string[]
  [extra: string]: unknown
}

/** PUT body: the request fields the server accepts, plus any extra fields carried through. */
export interface UserProviderRequest {
  preset: string
  inputs: Record<string, string>
  required_claims?: Record<string, string>
  display_name?: string
  disabled?: boolean
  directory?: DirectorySpec
  /** Claim naming the client application; "none" disables the preset default. */
  client_claim?: string
  client_ids?: string[]
  /** Write-only: saves despite an overlap with an agent identity binding. Never persisted. */
  acknowledge_overlap?: boolean
  [extra: string]: unknown
}

// Fields the server derives or owns. Everything else on a provider is treated
// as request data and sent back unchanged on edit, so optional fields added to
// the contract later (directory, client_claim, client_ids, acknowledgements)
// are not silently dropped by a console that has not learned them yet.
const SERVER_OWNED = new Set(['id', 'revision', 'principal_pool', 'issuer', 'jwks_url', 'audience', 'subject_claim', 'required_claims', 'extra_claims', 'groups_from', 'group_claim', 'warnings'])
const FORM_OWNED = new Set(['preset', 'inputs', 'display_name', 'disabled', 'directory', 'client_claim', 'client_ids', 'acknowledge_overlap'])

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
  /** Where groups came from: "token", "directory:<kind>" or "unavailable". */
  groups_source?: string
  /** Verified client application the token was issued to, when the provider has a client claim. */
  client_id?: string
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

export const invalidateMembership = (id: string, subject: string) =>
  authenticatedRequest<{ principal_id: string; not_before: string; scope: string }>(`${base}/user-providers/${encodeURIComponent(id)}/membership/invalidate`, {
    method: 'POST', body: JSON.stringify({ subject }),
  })

export interface ProviderFormValues {
  inputs: Record<string, string>
  display_name: string
  disabled: boolean
  required_claims: Record<string, string>
  directory?: DirectorySpec
  client_claim?: string
  client_ids: string[]
  acknowledge_overlap?: boolean
}

/** Builds a PUT body from form values; fields this console does not know are carried from `existing`. */
export function providerRequest(preset: string, form: ProviderFormValues, existing?: UserProvider): UserProviderRequest {
  return {
    ...(existing ? unknownProviderFields(existing) : {}),
    preset,
    inputs: form.inputs,
    ...(Object.keys(form.required_claims).length ? { required_claims: form.required_claims } : {}),
    ...(form.display_name ? { display_name: form.display_name } : {}),
    disabled: form.disabled,
    ...(form.directory ? { directory: form.directory } : {}),
    ...(form.client_claim ? { client_claim: form.client_claim } : {}),
    ...(form.client_ids.length ? { client_ids: form.client_ids } : {}),
    ...(form.acknowledge_overlap ? { acknowledge_overlap: true } : {}),
  }
}

export interface OverlapConflict { kind?: string; id?: string; issuer?: string; audience?: string }
/** The conflicting resource of a 409 overlap error (user or agent side), if the server named one. */
export function overlapConflict(error: unknown): { code: string; message: string; conflict: OverlapConflict } | undefined {
  const e = error as { code?: string; message?: string; details?: { conflict?: OverlapConflict } } | null
  if (e?.code !== 'IDENTITY_OVERLAPS_AGENT_BINDING' && e?.code !== 'IDENTITY_OVERLAPS_USER_PROVIDER') return undefined
  return { code: e.code, message: e.message ?? 'Overlapping identity', conflict: e.details?.conflict ?? {} }
}
