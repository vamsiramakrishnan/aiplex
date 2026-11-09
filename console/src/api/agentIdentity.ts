import { authenticatedRequest } from './client'

// Cross-cloud agent identity federation. Contract: internal/api/agent_federation.go.

export interface IdentityField {
  name: string
  label: string
  description: string
  required: boolean
  example?: string
  enum?: string[]
  default?: string
}

export interface IdentityPreset {
  id: string
  display_name: string
  cloud: string
  description: string
  inputs: IdentityField[]
  subject_shape: string
  issuer_shape: string
  allows_required_claims: boolean
  algorithms: string[]
  notes?: string[]
}

export interface IdentityPresetCatalog {
  presets: IdentityPreset[]
  default_audience?: string
  live_authentication: boolean
}

export interface IdentityBinding {
  id: string
  client_id: string
  preset: string
  inputs: Record<string, string>
  display_name?: string
  disabled: boolean
  issuer: string
  jwks_url: string
  audience: string
  subject_claim: string
  subject: string
  required_claims?: Record<string, string>
  extra_claims?: Record<string, string>
  revision: number
  principal_id: string
  actor_id?: string
}

export interface IdentityBindingRequest {
  preset: string
  inputs: Record<string, string>
  required_claims?: Record<string, string>
  display_name?: string
  disabled?: boolean
}

export interface IdentityStep { title: string; description?: string; language?: string; content?: string }

export interface IdentityPlan {
  binding: { client_id: string; id: string; preset: string }
  cloud: string
  issuer: string
  audience: string
  subject: string
  steps: IdentityStep[]
  token_snippet: IdentityStep
  aiplex_steps: IdentityStep[]
  notes?: string[]
}

export interface IdentityCheck { name: string; status: 'pass' | 'fail' | 'skipped'; reason: string }

export interface IdentityVerifyResult {
  valid: boolean
  checks: IdentityCheck[]
  issuer?: string
  subject?: string
  audience?: string[]
  binding?: { client_id: string; id: string; preset: string }
  actor_subject?: string
  principal_id?: string
  actor_id?: string
  live_authentication: boolean
}

const agentBase = (agent: string) => `/api/v1/agents/${encodeURIComponent(agent)}/identities`
const ifMatch = (revision: number) => ({ 'If-Match': `"${revision}"` })

/** API error code (for example REVISION_CONFLICT) attached by the shared transport, if any. */
export function identityErrorCode(error: unknown): string | undefined {
  const code = (error as { code?: unknown } | null)?.code
  return typeof code === 'string' ? code : undefined
}

export const listIdentityPresets = () => authenticatedRequest<IdentityPresetCatalog>('/api/v1/agents/identity-presets')
export const listAgentIdentities = (agent: string) => authenticatedRequest<IdentityBinding[]>(agentBase(agent))
export const putAgentIdentity = (agent: string, id: string, body: IdentityBindingRequest, expectedRevision?: number) =>
  authenticatedRequest<IdentityBinding>(`${agentBase(agent)}/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
    headers: expectedRevision ? ifMatch(expectedRevision) : undefined,
  })
export const deleteAgentIdentity = (agent: string, id: string, revision: number) =>
  authenticatedRequest<void>(`${agentBase(agent)}/${encodeURIComponent(id)}`, { method: 'DELETE', headers: ifMatch(revision) })
export const planAgentIdentity = (agent: string, id: string) =>
  authenticatedRequest<IdentityPlan>(`${agentBase(agent)}/${encodeURIComponent(id)}/plan`, { method: 'POST' })

/** Dry-runs a workload token. The token is sent once in the request body and not retained here. */
export const verifyAgentIdentityToken = (token: string, pin?: { client_id: string; binding_id: string }) =>
  authenticatedRequest<IdentityVerifyResult>('/api/v1/agents/identities/verify', {
    method: 'POST',
    body: JSON.stringify({ token, ...(pin ?? {}) }),
  })
