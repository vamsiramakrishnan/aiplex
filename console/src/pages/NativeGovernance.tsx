import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listNativeProviders, listNativeBindings, readNativeBinding, planNativeBinding, getNativePlan, applyNativePlan, getNativeReceipt, type NativePlan, type NativeReceipt, type NativeObservation } from '../api/governance'

const button = 'border rounded px-3 py-2 text-sm disabled:opacity-50'

// This panel executes only operations from a saved, revisioned binding. Public
// provider metadata describes configuration; it is never enforcement evidence.
export default function NativeGovernance() {
  const providers = useQuery({ queryKey: ['native-providers'], queryFn: listNativeProviders, retry: false })
  const bindings = useQuery({ queryKey: ['native-bindings'], queryFn: listNativeBindings, retry: false })
  const [bindingId, setBindingId] = useState('')
  const [readIndex, setReadIndex] = useState('')
  const [desiredId, setDesiredId] = useState('')
  const [planId, setPlanId] = useState('')
  const [plan, setPlan] = useState<NativePlan | null>(null)
  const [observation, setObservation] = useState<NativeObservation | null>(null)
  const [receipt, setReceipt] = useState<NativeReceipt | null>(null)
  const [approval, setApproval] = useState('')
  const [busy, setBusy] = useState(false)
  const [attempted, setAttempted] = useState(false)
  const [error, setError] = useState('')
  const [now, setNow] = useState(Date.now())
  const binding = bindings.data?.find((record) => record.id === bindingId)
  const provider = providers.data?.find((entry) => entry.id === binding?.spec.driver_ref)
  const reads = binding?.spec.allowed.filter((grant) => provider?.operations.some((op) => op.name === grant.operation && !op.mutating && !op.credential)) ?? []
  const desired = binding?.spec.desired?.filter((entry) => provider?.operations.some((op) => op.name === entry.operation.name && op.mutating && !op.credential)) ?? []
  const reviewed = desired.find((entry) => entry.id === plan?.desired_id)
  const currentPlan = plan && binding && plan.binding_id === binding.id && plan.binding_revision === binding.revision && plan.driver_ref === binding.spec.driver_ref
  const expired = !!plan && (!Number.isFinite(Date.parse(plan.expires_at)) || Date.parse(plan.expires_at) <= now)

  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer) }, [])
  useEffect(() => { setPlan(null); setReceipt(null); setObservation(null); setApproval(''); setError(''); setReadIndex(''); setDesiredId(''); setAttempted(false) }, [bindingId, binding?.revision])

  async function action(work: () => Promise<void>) { setBusy(true); setError(''); try { await work() } catch (failure) { setError(failure instanceof Error ? failure.message : 'Native action failed.') } finally { setBusy(false) } }
  const discoveryError = providers.error ?? bindings.error
  return <section className="space-y-5">
    <div><h3 className="text-lg font-semibold">Native agent governance</h3><p className="text-sm text-gray-500">Inspect cloud resources and review saved desired actions. Registration and successful API calls do not prove that cloud enforcement or runtime readiness is active.</p></div>
    {discoveryError && <p role="alert" className="text-red-600">Native governance unavailable: {discoveryError.message}</p>}
    {(providers.isLoading || bindings.isLoading) && <p role="status">Loading configured providers and saved bindings…</p>}
    {providers.data && providers.data.length === 0 && <p>No native governance providers are configured for this tenant.</p>}
    {!!providers.data?.length && <div className="grid gap-2 md:grid-cols-2">{providers.data.map((entry) => <div className="border rounded p-3 text-sm" key={entry.id}><strong>{entry.id}</strong><p>{entry.type} · {entry.status}</p><p>Apply {entry.apply_enabled ? 'enabled by operator' : 'disabled by operator'}</p></div>)}</div>}
    <div><label htmlFor="native-binding" className="block text-sm font-medium">Saved agent binding</label><select id="native-binding" value={bindingId} disabled={busy} onChange={(e) => setBindingId(e.target.value)} className="border rounded p-2 w-full mt-1"><option value="">Choose a saved binding</option>{bindings.data?.map((record) => <option key={record.id} value={record.id}>{record.id} · revision {record.revision}</option>)}</select>{bindings.isSuccess && bindings.data.length === 0 && <p className="text-sm mt-2">No saved bindings. Register a tenant-scoped binding through the API or SDK before reading or planning cloud actions.</p>}</div>
    {binding && <>
      <dl className="text-sm space-y-1 break-all"><div><dt className="inline font-medium">Native agent: </dt><dd className="inline">{binding.spec.agent.immutable_id}</dd></div>{binding.spec.delegated_user && <div><dt className="inline font-medium">Delegated user: </dt><dd className="inline">{binding.spec.delegated_user.immutable_id}</dd></div>}{binding.spec.registry && <><div><dt className="inline font-medium">Registry resource: </dt><dd className="inline">{binding.spec.registry.native_resource_name}</dd></div><div><dt className="inline font-medium">Discovery URN: </dt><dd className="inline">{binding.spec.registry.discovery_urn || 'Not recorded'}</dd></div></>}</dl>
      {!provider && <p role="alert">This binding's provider is unavailable to the current session.</p>}
      <div className="border rounded p-4 space-y-3"><h4 className="font-medium">Observe native state</h4><label htmlFor="native-read" className="block text-sm">Allowed read operation</label><select id="native-read" value={readIndex} onChange={(e) => setReadIndex(e.target.value)} disabled={busy} className="border rounded p-2 w-full"><option value="">Choose an allowed read</option>{reads.map((entry, index) => <option key={`${entry.operation}:${entry.resource}`} value={index}>{entry.operation} · {entry.resource}</option>)}</select><button className={button} disabled={busy || readIndex === '' || !reads[Number(readIndex)]} onClick={() => void action(async () => { const read = reads[Number(readIndex)]; setObservation(await readNativeBinding(binding.id, binding.revision, { name: read.operation, resource: read.resource })) })}>Read native resource</button>{observation && <div><p className="text-sm">Observed {new Date(observation.observed_at).toLocaleString()}</p>{observation.etag && <p className="text-sm break-all">Observation token: <code>{observation.etag}</code></p>}<pre className="bg-gray-50 dark:bg-gray-900 p-3 text-xs overflow-auto">{JSON.stringify(observation.body ?? { result_digest: observation.result_digest }, null, 2)}</pre></div>}</div>
      <div className="border rounded p-4 space-y-3"><h4 className="font-medium">Review a saved desired action</h4><label htmlFor="native-desired" className="block text-sm">Desired action</label><select id="native-desired" value={desiredId} onChange={(e) => setDesiredId(e.target.value)} disabled={busy} className="border rounded p-2 w-full"><option value="">Choose a saved desired action</option>{desired.map((entry) => <option key={entry.id} value={entry.id}>{entry.id} · {entry.operation.name}</option>)}</select><button className={button} disabled={busy || !desiredId} onClick={() => void action(async () => { const saved = await planNativeBinding(binding.id, binding.revision, desiredId); setPlan(saved); setPlanId(saved.id); setApproval(''); setReceipt(null); setAttempted(false) })}>Create review plan</button><div className="flex gap-2"><input aria-label="Saved plan ID" className="border rounded p-2 flex-1 min-w-0 text-sm" value={planId} disabled={busy} onChange={(e) => setPlanId(e.target.value)} placeholder="Load an existing saved plan" /><button className={button} disabled={busy || !planId.trim()} onClick={() => void action(async () => { const saved = await getNativePlan(planId.trim()); if (saved.binding_id !== binding.id || saved.binding_revision !== binding.revision) throw new Error('This plan does not match the selected binding revision.'); setPlan(saved); setApproval(''); setAttempted(false); setReceipt(null) })}>Load saved plan</button></div></div>
      {plan && <div className="border rounded p-4 space-y-3"><h4 className="font-medium">Saved plan review</h4><p className="text-sm break-all">{plan.operation} · {plan.resource}</p><p className="text-sm">Binding revision {plan.binding_revision} · Plan revision {plan.revision}</p><p className="text-sm">{expired ? 'Plan expired. Create and review a current plan.' : `Expires ${new Date(plan.expires_at).toLocaleString()}`}</p><p className="text-sm break-all">Digest: <code>{plan.digest}</code></p><p className="text-xs break-all">Payload hash: <code>{plan.payload_hash}</code></p>{reviewed && <pre className="bg-gray-50 dark:bg-gray-900 p-3 text-xs overflow-auto">{JSON.stringify(reviewed.operation, null, 2)}</pre>}{!currentPlan && <p role="alert">The binding changed. Review a new plan.</p>}<label htmlFor="native-approval" className="block text-sm">To approve this exact saved plan, enter its digest</label><input id="native-approval" className="border rounded p-2 w-full font-mono text-sm" value={approval} disabled={busy || attempted} onChange={(e) => setApproval(e.target.value)} autoComplete="off" /><div className="flex gap-2 flex-wrap"><button className={button} disabled={busy || attempted || !provider?.apply_enabled || !currentPlan || expired || approval !== plan.digest} onClick={() => void action(async () => { setAttempted(true); try { setReceipt(await applyNativePlan(plan, approval)) } catch (failure) { try { setReceipt(await getNativeReceipt(plan.id)) } catch { /* outcome remains explicitly unknown */ }; throw failure } })}>Apply reviewed plan</button><button className={button} disabled={busy} onClick={() => void action(async () => { setReceipt(await getNativeReceipt(plan.id)); setAttempted(true) })}>Inspect receipt</button></div>{!provider?.apply_enabled && <p className="text-sm">Native apply is disabled by the operator for this provider.</p>}{attempted && !receipt && <p role="status">Outcome not confirmed. Inspect the saved receipt before taking another action; this panel will not retry apply.</p>}{receipt && <p role="status" className="text-sm">Native API outcome: <strong>{receipt.state}</strong>. Runtime readiness: <strong>{receipt.readiness || 'unverified'}</strong>. {receipt.state === 'pending' || receipt.state === 'unknown' ? 'Inspect the provider outcome; do not repeat the action.' : ''}</p>}</div>}
    </>}
    {error && <p role="alert" className="text-red-600">{error}</p>}
  </section>
}
