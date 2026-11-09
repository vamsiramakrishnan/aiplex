import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import QuotaPlex from '../pages/QuotaPlex'
import * as api from '../api/quota'
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: { sub: 'principal-a', actor_id: 'agent-a' } }) }))
vi.mock('../api/quota', () => ({ quotaPolicy: vi.fn(), quotaInventory: vi.fn(), quotaAttempt: vi.fn(), quotaReconciliation: vi.fn(), quotaMutation: vi.fn(), validateQuotaPolicy: vi.fn(), quotaOperationKey: vi.fn(), safeQuotaJSON: (s: string) => JSON.parse(s) }))
const policy = { policy: { tenant_id: 'tenant', id: 'team', revision: 2, sharing: [{ id: 'lending-rule' }] }, reference: { digest: 'current' }, active: { digest: 'current' }, as_of: new Date().toISOString() }
const plan = { id: 'review', digest: 'exact-reviewed-digest', attempt_id: 'attempt', reason: 'native lookup', evidence_ref: 'native-receipt', independent_approval: true, expires_at: new Date(Date.now() + 600000).toISOString(), settlement: {} }
beforeEach(() => { vi.resetAllMocks(); sessionStorage.clear(); vi.mocked(api.quotaPolicy).mockResolvedValue(policy); vi.mocked(api.quotaInventory).mockResolvedValue({ kind: 'budgets', records: [], done: true, as_of: policy.as_of }); vi.mocked(api.quotaReconciliation).mockResolvedValue({ plan }); vi.mocked(api.quotaOperationKey).mockResolvedValue('stable-operation-id') })
it('loads actual sharing rules and retains the same loan ID after a lost response', async () => {
  vi.mocked(api.quotaMutation).mockRejectedValueOnce(new Error('Response lost')).mockResolvedValueOnce({ id: 'stable-operation-id' })
  render(<QuotaPlex />); await screen.findByText('team · revision 2')
  fireEvent.change(screen.getByLabelText('Lending rule'), { target: { value: 'lending-rule' } }); fireEvent.change(screen.getByLabelText('Amount in policy units'), { target: { value: '7' } }); fireEvent.change(screen.getByLabelText('Loan expiry'), { target: { value: '2026-11-10T12:00' } })
  fireEvent.click(screen.getByText('Grant policy-authorized loan')); await screen.findByText('Response lost'); fireEvent.click(screen.getByText('Grant policy-authorized loan'))
  await waitFor(() => expect(api.quotaMutation).toHaveBeenCalledTimes(2))
  expect(vi.mocked(api.quotaMutation).mock.calls[0]).toEqual(vi.mocked(api.quotaMutation).mock.calls[1])
})
it('requires the exact reviewed digest and inspects failed apply once without retry', async () => {
  vi.mocked(api.quotaMutation).mockRejectedValue(new Error('Connection lost'))
  render(<QuotaPlex />); await screen.findByText('team · revision 2'); fireEvent.change(screen.getByLabelText('Reconciliation plan ID'), { target: { value: 'review' } }); fireEvent.click(screen.getByText('Load saved plan')); await screen.findByText('A different authorized principal must approve this plan.')
  expect(screen.getByText('Apply reviewed reconciliation')).toBeDisabled(); fireEvent.change(screen.getByLabelText('Reviewed reconciliation digest'), { target: { value: plan.digest } }); fireEvent.click(screen.getByText('Apply reviewed reconciliation')); await screen.findByText('Connection lost')
  expect(screen.getByText('Apply reviewed reconciliation')).toBeDisabled(); expect(api.quotaMutation).toHaveBeenCalledTimes(1); expect(api.quotaReconciliation).toHaveBeenCalledTimes(2)
})
it('surfaces denied quota administration without inventing inventory', async () => {
  vi.mocked(api.quotaPolicy).mockRejectedValue(new Error('Quota read grant required')); render(<QuotaPlex />); expect(await screen.findByRole('alert')).toHaveTextContent('Quota read grant required'); expect(screen.getByRole('status')).toHaveTextContent('unavailable')
})
