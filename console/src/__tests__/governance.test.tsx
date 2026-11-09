import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import NativeGovernance from '../pages/NativeGovernance'
import * as api from '../api/governance'
vi.mock('../api/governance', () => ({ listNativeProviders: vi.fn(), listNativeBindings: vi.fn(), readNativeBinding: vi.fn(), planNativeBinding: vi.fn(), getNativePlan: vi.fn(), applyNativePlan: vi.fn(), getNativeReceipt: vi.fn() }))
const resource = 'projects/example/locations/global/services/agent-one'
const binding = { id: 'agent-one', revision: 4, spec: { id: 'agent-one', tenant_id: 'tenant', driver_ref: 'google', agent: { provider: 'google', tenant_id: 'example', immutable_id: 'native-agent', kind: 'agent' }, allowed: [{ operation: 'services.get', resource }, { operation: 'services.create', resource }], desired: [{ id: 'register', operation: { name: 'services.create', resource, payload: { resource: { agentSpec: { type: 'NO_SPEC' } } } } }] } }
const provider = { id: 'google', type: 'google-agent-registry', status: 'configured-unverified', apply_enabled: true, operations: [{ name: 'services.get', mutating: false, credential: false, description: 'Observe' }, { name: 'services.create', mutating: true, credential: false, description: 'Register' }] }
const plan = { id: 'saved-plan', revision: 3, binding_id: 'agent-one', binding_revision: 4, desired_id: 'register', driver_ref: 'google', operation: 'services.create', resource, payload_hash: 'payload-hash', digest: 'reviewed-digest', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 900_000).toISOString() }
function show() { const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } }); return render(<QueryClientProvider client={client}><NativeGovernance /></QueryClientProvider>) }
async function choose() { await screen.findByText('agent-one · revision 4'); fireEvent.change(screen.getByLabelText('Saved agent binding'), { target: { value: 'agent-one' } }) }
async function createPlan() { await choose(); fireEvent.change(screen.getByLabelText('Desired action'), { target: { value: 'register' } }); fireEvent.click(screen.getByText('Create review plan')); await screen.findByText('Saved plan review') }
beforeEach(() => { vi.resetAllMocks(); vi.mocked(api.listNativeProviders).mockResolvedValue([provider]); vi.mocked(api.listNativeBindings).mockResolvedValue([binding]); vi.mocked(api.planNativeBinding).mockResolvedValue(plan); vi.mocked(api.applyNativePlan).mockResolvedValue({ plan_id: 'saved-plan', state: 'succeeded', readiness: 'unverified', started_at: new Date().toISOString() }); vi.mocked(api.readNativeBinding).mockResolvedValue({ operation: 'services.get', resource, observed_at: new Date().toISOString(), result_digest: 'native-hash', etag: 'sha256:observed', body: { displayName: 'observed name' } }) })
describe('Native governance Console', () => {
  it('keeps reading and planning separate from explicit digest approval and native readiness', async () => {
    show(); await choose(); fireEvent.change(screen.getByLabelText('Allowed read operation'), { target: { value: '0' } }); fireEvent.click(screen.getByText('Read native resource')); await screen.findByText(/observed name/)
    expect(api.readNativeBinding).toHaveBeenCalledWith('agent-one', 4, { name: 'services.get', resource })
    fireEvent.change(screen.getByLabelText('Desired action'), { target: { value: 'register' } }); fireEvent.click(screen.getByText('Create review plan')); await screen.findByText('Saved plan review')
    expect(api.planNativeBinding).toHaveBeenCalledWith('agent-one', 4, 'register'); expect(api.applyNativePlan).not.toHaveBeenCalled()
    expect(screen.getByText('Apply reviewed plan')).toBeDisabled(); fireEvent.change(screen.getByLabelText('To approve this exact saved plan, enter its digest'), { target: { value: 'wrong' } }); expect(screen.getByText('Apply reviewed plan')).toBeDisabled()
    fireEvent.change(screen.getByLabelText('To approve this exact saved plan, enter its digest'), { target: { value: plan.digest } }); fireEvent.click(screen.getByText('Apply reviewed plan'))
    await waitFor(() => expect(api.applyNativePlan).toHaveBeenCalledWith(plan, plan.digest)); expect(await screen.findByText('succeeded')).toBeInTheDocument(); expect(screen.getByText('unverified')).toBeInTheDocument(); expect(screen.getByText('Apply reviewed plan')).toBeDisabled()
  })
  it('inspects the receipt after an ambiguous failure and does not retry apply', async () => {
    vi.mocked(api.applyNativePlan).mockRejectedValue(new Error('Connection lost'))
    vi.mocked(api.getNativeReceipt).mockResolvedValue({ plan_id: 'saved-plan', state: 'unknown', readiness: 'unverified', started_at: new Date().toISOString() })
    show(); await createPlan(); fireEvent.change(screen.getByLabelText('To approve this exact saved plan, enter its digest'), { target: { value: plan.digest } }); fireEvent.click(screen.getByText('Apply reviewed plan'))
    expect(await screen.findByText('unknown')).toBeInTheDocument(); expect(screen.getByText('Apply reviewed plan')).toBeDisabled(); expect(api.getNativeReceipt).toHaveBeenCalledWith('saved-plan'); expect(api.applyNativePlan).toHaveBeenCalledTimes(1)
  })
  it('keeps operator-disabled apply disabled and shows discovery failures', async () => {
    vi.mocked(api.listNativeProviders).mockResolvedValue([{ ...provider, apply_enabled: false }]); const view = show(); await createPlan(); fireEvent.change(screen.getByLabelText('To approve this exact saved plan, enter its digest'), { target: { value: plan.digest } }); expect(screen.getByText('Apply reviewed plan')).toBeDisabled(); view.unmount()
    vi.mocked(api.listNativeProviders).mockRejectedValue(new Error('Provider discovery grant required')); show(); expect(await screen.findByRole('alert')).toHaveTextContent('Provider discovery grant required')
  })
})
