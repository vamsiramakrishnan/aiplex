import { authenticatedRequest } from './client'

const base = '/api/v1/iam/native'
export interface NativeOperation { name: string; resource: string; payload?: unknown; etag?: string }
export interface NativeProvider { id: string; type: string; status: string; apply_enabled: boolean; operations: { name: string; mutating: boolean; credential: boolean; description: string }[] }
export interface NativePrincipal { provider: string; tenant_id: string; immutable_id: string; kind: string }
export interface NativeBinding {
  id: string; driver_ref: string; tenant_id: string; agent: NativePrincipal
  delegated_user?: NativePrincipal
  registry?: { provider: string; tenant_id: string; native_resource_name: string; discovery_urn?: string; runtime_ref?: string }
  allowed: { operation: string; resource: string }[]
  desired?: { id: string; operation: NativeOperation }[]
}
export interface NativeBindingRecord { id: string; revision: number; spec: NativeBinding }
export interface NativePlan {
  id: string; revision: number; digest: string; binding_id: string; binding_revision: number
  desired_id: string; driver_ref: string; operation: string; resource: string; payload_hash: string
  etag?: string; expires_at: string; created_at: string
}
export interface NativeObservation { operation: string; resource: string; etag?: string; observed_at: string; result_digest: string; body?: unknown }
export interface NativeReceipt { plan_id: string; state: string; readiness: string; etag?: string; result_digest?: string; started_at: string; finished_at?: string }
export const listNativeProviders = () => authenticatedRequest<NativeProvider[]>(`${base}/providers`)
export const listNativeBindings = () => authenticatedRequest<NativeBindingRecord[]>(`${base}/bindings`)
export const readNativeBinding = (id: string, bindingRevision: number, operation: NativeOperation) => authenticatedRequest<NativeObservation>(`${base}/bindings/${encodeURIComponent(id)}/read`, { method: 'POST', body: JSON.stringify({ binding_revision: bindingRevision, operation }) })
export const planNativeBinding = (id: string, bindingRevision: number, desiredId: string) => authenticatedRequest<NativePlan>(`${base}/bindings/${encodeURIComponent(id)}/plan`, { method: 'POST', body: JSON.stringify({ binding_revision: bindingRevision, desired_id: desiredId }) })
export const getNativePlan = (id: string) => authenticatedRequest<NativePlan>(`${base}/plans/${encodeURIComponent(id)}`)
export const applyNativePlan = (plan: NativePlan, digest: string) => authenticatedRequest<NativeReceipt>(`${base}/plans/${encodeURIComponent(plan.id)}/apply`, { method: 'POST', body: JSON.stringify({ plan_revision: plan.revision, approval_digest: digest }) })
export const getNativeReceipt = (id: string) => authenticatedRequest<NativeReceipt>(`${base}/receipts/${encodeURIComponent(id)}`)
