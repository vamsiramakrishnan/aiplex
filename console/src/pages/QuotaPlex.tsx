import { useEffect, useState } from 'react'
import * as api from '../api/quota'
import { useAuth } from '../hooks/useAuth'

const input = 'w-full rounded border border-gray-300 bg-transparent px-3 py-2 dark:border-gray-700'
const button = 'rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900'
function Details({ value }: { value: unknown }) { return <pre className="max-h-96 overflow-auto rounded bg-gray-100 p-4 text-xs dark:bg-gray-900">{JSON.stringify(value, null, 2)}</pre> }
export default function QuotaPlex() {
  const { user } = useAuth()
  const [operationNonce, setOperationNonce] = useState('initial')
  const [policy, setPolicy] = useState<api.QuotaPolicy | null>(null)
  const [kind, setKind] = useState('budgets')
  const [page, setPage] = useState<api.Inventory | null>(null)
  const [after, setAfter] = useState('')
  const [detail, setDetail] = useState<unknown>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [attemptID, setAttemptID] = useState('')
  const [draft, setDraft] = useState('')
  const [artifact, setArtifact] = useState<unknown>(null)
  const [loanRule, setLoanRule] = useState('')
  const [loanAmount, setLoanAmount] = useState('')
  const [loanExpiry, setLoanExpiry] = useState('')
  const [returnID, setReturnID] = useState('')
  const [reason, setReason] = useState('')
  const [evidence, setEvidence] = useState('')
  const [usage, setUsage] = useState('{}')
  const [version, setVersion] = useState('')
  const [resourceID, setResourceID] = useState('')
  const [expires, setExpires] = useState('')
  const [planID, setPlanID] = useState('')
  const [review, setReview] = useState<api.Inspection | null>(null)
  const [confirmation, setConfirmation] = useState('')
  const [applyStarted, setApplyStarted] = useState(false)
  useEffect(() => {
    let active = true
    setPage(null)
    Promise.all([api.quotaPolicy(), api.quotaInventory(kind, after)]).then(([p, rows]) => {
      if (!active) return
      setPolicy(p); setPage(rows); setError('')
    }).catch(e => { if (active) setError(String(e instanceof Error ? e.message : e)) })
    return () => { active = false }
  }, [kind, after, revision])
  const operationOwner = JSON.stringify([user?.sub, user?.actor_id, policy?.policy.tenant_id])
  useEffect(() => { try { setOperationNonce(sessionStorage.getItem(`aiplex-quota-nonce:${operationOwner}`) ?? 'initial') } catch { setError('Browser operation storage is unavailable. Use the CLI for quota changes.') } }, [operationOwner])
  const operationScope = JSON.stringify([operationOwner, operationNonce])
  async function run(action: () => Promise<void>) {
    if (busy) return
    setBusy(true); setError('')
    try { await action() } catch (e) { setError(e instanceof Error ? e.message : String(e)) } finally { setBusy(false) }
  }
  async function inspectPlan(id: string) {
    const inspection = await api.quotaReconciliation(id)
    setReview(inspection); setPlanID(id); setConfirmation(''); setApplyStarted(Boolean(inspection.receipt))
  }
  return <div className="space-y-8">
    <header><h1 className="text-2xl font-semibold">QuotaPlex</h1><p className="mt-2 text-sm text-gray-500">Allocations, held exposure and reviewed reconciliation for your tenant.</p></header>
    {error && <p role="alert" className="rounded border border-red-300 p-3 text-red-700">{error}</p>}
    {policy && <section aria-label="Active quota policy" className="grid gap-4 sm:grid-cols-4">
      {[['Policy', `${policy.policy.id} · revision ${policy.policy.revision}`], ['Budget rules', policy.policy.budgets?.length ?? 0], ['Rate rules', policy.policy.rates?.length ?? 0], ['Concurrency rules', policy.policy.concurrency?.length ?? 0]].map(([label, value]) => <div key={String(label)} className="rounded border border-gray-200 p-4 dark:border-gray-800"><p className="text-xs text-gray-500">{label}</p><p className="mt-1 font-medium">{value}</p></div>)}
    </section>}
    <section className="space-y-3"><div className="flex flex-wrap gap-3"><label>Inventory<select aria-label="Quota inventory" className={input} value={kind} onChange={e => { setKind(e.target.value); setAfter('') }}>{['budgets', 'loans', 'attempts', 'terminal-work', 'reconciliations', 'schedulers', 'scheduled-jobs', 'audit'].map(k => <option key={k}>{k}</option>)}</select></label><button className={button} disabled={busy} onClick={() => setRevision(v => v + 1)}>Refresh</button></div>
      {!page ? <p role="status">{error ? 'Quota inventory unavailable.' : 'Loading quota inventory…'}</p> : <><p className="text-xs text-gray-500">As of {new Date(page.as_of).toLocaleString()}</p><table className="w-full text-left text-sm"><thead><tr><th className="py-2">Record</th><th>Inspect</th></tr></thead><tbody>{page.records.map(record => <tr key={record.id} className="border-t border-gray-200 dark:border-gray-800"><td className="max-w-sm truncate py-2 font-mono text-xs">{record.id}</td><td><button className="underline" onClick={() => { setDetail(record.value); if (kind === 'reconciliations') void run(() => inspectPlan(record.id)) }}>Details</button></td></tr>)}</tbody></table>{page.records.length === 0 && <p>No records in this page.</p>}<button className={button} disabled={page.done || busy} onClick={() => setAfter(page.next_after_id ?? '')}>Next page</button></>}
      {detail !== null && <Details value={detail} />}
    </section>
    <section className="space-y-3"><h2 className="text-lg font-medium">Inspect an attempt</h2><div className="flex gap-3"><input className={input} aria-label="Attempt ID" value={attemptID} onChange={e => setAttemptID(e.target.value)} /><button className={button} disabled={!attemptID || busy} onClick={() => void run(async () => setDetail(await api.quotaAttempt(attemptID)))}>Inspect attempt</button></div><p className="text-xs text-gray-500">Unknown work retains its hold. A timeout does not establish zero usage.</p></section>
    <section className="space-y-3"><h2 className="text-lg font-medium">Review a policy revision</h2><p className="text-sm text-gray-500">Validate a draft against the active policy. The resulting artifact requires a coordinated deployment.</p><button className={button} disabled={!policy || busy} onClick={() => { if (policy) setDraft(JSON.stringify(policy.policy, null, 2)) }}>Start from active policy</button><textarea className={`${input} h-48 font-mono text-xs`} aria-label="Draft quota policy" value={draft} onChange={e => { setDraft(e.target.value); setArtifact(null) }} /><button className={button} disabled={!draft || !policy || busy} onClick={() => void run(async () => setArtifact(await api.validateQuotaPolicy(api.safeQuotaJSON(draft), policy!.active.digest)))}>Validate policy artifact</button>{artifact !== null && <Details value={artifact} />}</section>
    <section className="space-y-3"><h2 className="text-lg font-medium">Share unused allocation</h2><p className="text-xs text-gray-500">Repeated submissions reuse the operation ID. Inspect uncertain outcomes before starting another operation.</p><button className={button} disabled={busy} onClick={() => void run(async () => { const nonce = crypto.randomUUID(); sessionStorage.setItem(`aiplex-quota-nonce:${operationOwner}`, nonce); setOperationNonce(nonce) })}>Start a new operation</button><p className="text-sm text-gray-500">Loans follow configured lending rules and original budget windows.</p><div className="grid gap-3 sm:grid-cols-3"><label>Lending rule<select className={input} value={loanRule} onChange={e => setLoanRule(e.target.value)}><option value="">Select a rule</option>{policy?.policy.sharing?.map(rule => <option key={rule.id}>{rule.id}</option>)}</select></label><label>Amount in policy units<input className={input} value={loanAmount} onChange={e => setLoanAmount(e.target.value)} /></label><label>Loan expiry<input className={input} type="datetime-local" value={loanExpiry} onChange={e => setLoanExpiry(e.target.value)} /></label></div><button className={button} disabled={!policy || !loanRule || !loanAmount || !loanExpiry || busy} onClick={() => void run(async () => { const amount = Number(loanAmount); if (!/^[1-9]\d*$/.test(loanAmount) || !Number.isSafeInteger(amount) || amount <= 0) throw new Error('Enter a positive exact integer amount.'); const fields = { rule_id: loanRule, amount, expires_at: new Date(loanExpiry).toISOString() }; const id = await api.quotaOperationKey(operationScope, 'loan', fields); setDetail(await api.quotaMutation('/loans', { id, ...fields }, id)); setRevision(v => v + 1) })}>Grant policy-authorized loan</button><div className="flex gap-3"><input aria-label="Loan ID to return" className={input} value={returnID} onChange={e => setReturnID(e.target.value)} /><button className={button} disabled={!policy || !returnID || busy} onClick={() => void run(async () => { setDetail(await api.quotaMutation(`/loans/${encodeURIComponent(returnID)}/return`, {}, await api.quotaOperationKey(operationScope, 'return-loan', returnID))); setRevision(v => v + 1) })}>Return unused amount</button></div></section>
    <section className="space-y-3"><h2 className="text-lg font-medium">Plan a reconciliation</h2><p className="text-sm text-gray-500">Use verified terminal raw counters. Original evidence and prices remain recorded; correcting one attempt does not reopen a quarantined binding.</p><div className="grid gap-3 sm:grid-cols-2"><label>Attempt<input className={input} value={attemptID} onChange={e => setAttemptID(e.target.value)} /></label><label>Evidence reference<input className={input} value={evidence} onChange={e => setEvidence(e.target.value)} /></label><label>Observed model version<input className={input} value={version} onChange={e => setVersion(e.target.value)} /></label><label>Provider resource ID<input className={input} value={resourceID} onChange={e => setResourceID(e.target.value)} /></label><label>Plan expiry<input className={input} type="datetime-local" value={expires} onChange={e => setExpires(e.target.value)} /></label><label>Reason<input className={input} value={reason} onChange={e => setReason(e.target.value)} /></label></div><label className="block">Raw usage by policy meter<textarea aria-label="Reconciliation usage" className={`${input} h-24 font-mono`} value={usage} onChange={e => setUsage(e.target.value)} /></label><button className={button} disabled={!policy || !attemptID || !reason || !evidence || !expires || busy} onClick={() => void run(async () => { const fields = { attempt_id: attemptID, reason, evidence_ref: evidence, usage: api.safeQuotaJSON(usage), model_version: version, provider_resource_id: resourceID, expires_at: new Date(expires).toISOString() }; const id = await api.quotaOperationKey(operationScope, 'reconciliation-plan', fields); const plan = await api.quotaMutation<api.Reconciliation>('/reconciliations', { id, ...fields }, id); setPlanID(plan.id); setReview({ plan }); setConfirmation(''); setApplyStarted(false) })}>Create reconciliation plan</button></section>
    <section className="space-y-3"><h2 className="text-lg font-medium">Review and apply reconciliation</h2><div className="flex gap-3"><input aria-label="Reconciliation plan ID" className={input} value={planID} onChange={e => { setPlanID(e.target.value); setReview(null); setConfirmation('') }} /><button className={button} disabled={!planID || busy} onClick={() => void run(() => inspectPlan(planID))}>Load saved plan</button></div>{review && <><Details value={review} /><p className="text-sm">{review.plan.independent_approval ? 'A different authorized principal must approve this plan.' : 'Approval follows this deployment’s configured policy.'}</p><label className="block">Enter the reviewed reconciliation digest<input aria-label="Reviewed reconciliation digest" className={`${input} font-mono`} value={confirmation} onChange={e => setConfirmation(e.target.value)} /></label><button className={button} disabled={busy || applyStarted || Boolean(review.receipt) || confirmation !== review.plan.digest || new Date(review.plan.expires_at).getTime() <= Date.now()} onClick={() => void run(async () => { setApplyStarted(true); try { await api.quotaMutation(`/reconciliations/${encodeURIComponent(review.plan.id)}/apply`, { digest: confirmation }, await api.quotaOperationKey(operationScope, 'reconciliation-apply', [review.plan.id, confirmation])); setReview(await api.quotaReconciliation(review.plan.id)); setRevision(v => v + 1) } catch (e) { try { setReview(await api.quotaReconciliation(review.plan.id)) } catch { /* Keep the reviewed plan; never automatically retry a write. */ } throw e } })}>Apply reviewed reconciliation</button></>}</section>
  </div>
}
