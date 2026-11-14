import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { OPERATOR_REASON, useAccess } from '../hooks/useAccess'
import { errorStatus, isRevisionConflict } from '../api/roleBindings'
import {
  deleteHookPolicy, listHookPolicies, putHookPolicy, testHookPolicy, validationMessage,
  type HookPhase, type HookPlane, type HookPolicyRecord, type HookResult, type HookTestResult,
} from '../api/hooks'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50'
const primary = 'px-3 py-1.5 text-sm bg-brand-600 text-brand-contrast rounded disabled:opacity-50'
const field = 'border rounded px-3 py-2 text-sm w-full'
const mono = 'border rounded px-3 py-2 text-xs w-full font-mono'
const msg = (e: unknown) => (e instanceof Error ? e.message : String(e))

const CHANGED = 'This hook policy changed since this page read it. Nothing was saved or deleted. Reload to see the current version, then try again.'
const STARTER = JSON.stringify({
  id: 'pii-guard', description: 'Mask email addresses.', scope: { kind: 'tenant' },
  hooks: [{ id: 'redact', type: 'redact', phases: ['request'], redact: { rules: [{ id: 'email', builtin: 'email', action: 'mask' }] } }],
}, null, 2)

// The stored spec is the policy document. Any part of it may be absent, and a null list is a missing list.
const obj = (v: unknown): Record<string, unknown> | null => (v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null)
const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined)
function scopeLabel(spec: unknown): string {
  const scope = obj(obj(spec)?.scope)
  const kind = str(scope?.kind)
  if (!kind) return 'unknown'
  const id = str(scope?.id)
  return id ? `${kind}:${id}` : kind
}
function hookSummaries(spec: unknown): { key: string; text: string }[] | null {
  const hooks = obj(spec)?.hooks
  if (!Array.isArray(hooks)) return null
  return hooks.map((h, i) => {
    const hook = obj(h)
    const phases = Array.isArray(hook?.phases) ? hook.phases.filter((p): p is string => typeof p === 'string').join('+') : ''
    return { key: `${i}`, text: `${str(hook?.id) ?? 'unknown'} (${str(hook?.type) ?? 'unknown'}: ${phases || 'unknown'})` }
  })
}

/** Request-hook policies: list, edit as a JSON document, delete, and dry-run. Operators only, as on the server. */
export default function Hooks() {
  const access = useAccess()
  const cache = useQueryClient()
  const [editor, setEditor] = useState<{ mode: 'create' } | { mode: 'edit'; record: HookPolicyRecord } | null>(null)
  const [dryId, setDryId] = useState<string | null>(null)
  const policies = useQuery({ queryKey: ['hook-policies'], queryFn: listHookPolicies, enabled: access.isOperator })
  if (!access.isOperator) {
    return <section className="space-y-2"><p role="status" className="text-sm text-gray-600 dark:text-gray-400">{OPERATOR_REASON} Hook policies are visible to operators only.</p></section>
  }
  const refresh = () => cache.invalidateQueries({ queryKey: ['hook-policies'] })
  const rows = policies.data
  const loadFailed = policies.isError
  const dryTarget = dryId !== null && rows?.some((p) => p.id === dryId) ? dryId : null
  return <section className="space-y-4" aria-label="Hooks">
    <p className="text-sm text-gray-600 dark:text-gray-400">A hook policy binds an ordered list of hooks (redact, size limit, HTTP sidecar) to a scope: the tenant, an access point, a model surface or a tool connection. Narrower scopes add hooks; they never remove a broader one.</p>
    <ul className="text-xs text-gray-600 dark:text-gray-400 list-disc pl-5 space-y-0.5" aria-label="Limits">
      <li>Streaming: when a response hook applies and the request streams, the policy&apos;s <code>streaming</code> setting decides. <code>deny</code> (the default) refuses the request before it is sent, with a message asking for a non-streaming request. <code>buffer</code> reads the whole response (up to 8 MiB) before re-emitting it, so time to first token becomes the time to the whole response, and only OpenAI Chat, Anthropic Messages, OpenAI Responses and Gemini streams are accepted: any other streamed format is refused. <code>passthrough_uninspected</code> relays the stream unchanged and uninspected.</li>
      <li>Hook decisions are not part of QuotaPlex replay: a retried request with the same idempotency key runs the hooks again.</li>
      <li>Redaction is pattern matching, not a guarantee that personal data or secrets are removed.</li>
    </ul>
    <div className="flex gap-2">
      <button type="button" className={primary} disabled={!!editor} onClick={() => setEditor({ mode: 'create' })}>Add hook policy</button>
      <button type="button" className={btn} disabled={policies.isFetching} onClick={() => void policies.refetch()}>{policies.isFetching ? 'Loading…' : 'Reload'}</button>
    </div>
    {editor && <PolicyEditor key={editor.mode === 'edit' ? `${editor.record.id}@${editor.record.revision ?? '?'}` : 'create'} editor={editor}
      onClose={() => setEditor(null)} onSaved={() => { setEditor(null); void refresh() }} onReload={() => { setEditor(null); void policies.refetch() }} />}
    {policies.isPending && <p role="status">Loading hook policies…</p>}
    {loadFailed && !rows && <div role="alert" className="text-status-denied-fg">
      <p>Could not load hook policies: {msg(policies.error)}. This is an error, not an empty list.</p>
      <button type="button" className={`${btn} mt-2`} onClick={() => void policies.refetch()}>Retry</button>
    </div>}
    {loadFailed && rows && <div role="alert" className="text-status-denied-fg">
      <p>Could not refresh hook policies: {msg(policies.error)}. The rows below were read earlier and may be out of date.</p>
      <button type="button" className={`${btn} mt-2`} onClick={() => void policies.refetch()}>Retry</button>
    </div>}
    {rows && rows.length === 0 && !loadFailed && <p role="status">No hook policies. No exchange is inspected by hooks.</p>}
    {rows && rows.length > 0 && <div className="overflow-x-auto"><table className="w-full text-sm text-left">
      <caption className="sr-only">{loadFailed ? 'Hook policies (possibly out of date)' : 'Hook policies'}</caption>
      <thead><tr><th className="py-2 pr-3">Id</th><th className="pr-3">Scope</th><th className="pr-3">Hooks</th><th className="pr-3">Revision</th><th><span className="sr-only">Actions</span></th></tr></thead>
      <tbody>{rows.map((p) => <Row key={p.id} record={p} stale={loadFailed} editing={editor?.mode === 'edit' && editor.record.id === p.id}
        onEdit={() => setEditor({ mode: 'edit', record: p })} onTest={() => setDryId(p.id)} onChanged={() => void refresh()} onReload={() => void policies.refetch()} />)}</tbody>
    </table></div>}
    {rows && rows.length > 0 && <div className="border rounded-lg p-4 space-y-3" role="group" aria-label="Dry run">
      <h2 className="font-semibold">Dry run</h2>
      <div><label htmlFor="hk-dry-policy" className="block text-sm font-medium mb-1">Policy</label>
        <select id="hk-dry-policy" className={field} value={dryTarget ?? ''} onChange={(e) => setDryId(e.target.value || null)}>
          <option value="">Choose a policy…</option>
          {rows.map((p) => <option key={p.id} value={p.id}>{p.id}</option>)}
        </select></div>
      {dryTarget ? <DryRun key={dryTarget} id={dryTarget} /> : <p className="text-sm text-gray-600 dark:text-gray-400">Choose a policy to try it on a sample.</p>}
    </div>}
  </section>
}

function Row({ record: p, stale, editing, onEdit, onTest, onChanged, onReload }: { record: HookPolicyRecord; stale: boolean; editing: boolean; onEdit: () => void; onTest: () => void; onChanged: () => void; onReload: () => void }) {
  const [confirming, setConfirming] = useState(false)
  const known = typeof p.revision === 'number'
  const remove = useMutation({
    mutationFn: () => deleteHookPolicy(p.id, p.revision as number),
    onSuccess: () => { setConfirming(false); onChanged() },
  })
  const conflict = remove.isError && (isRevisionConflict(remove.error) || errorStatus(remove.error) === 404)
  const why = known ? undefined : 'The revision is unknown, so a change cannot be checked against it. Reload first.'
  const staleWhy = stale ? 'Reload first: the list may be out of date.' : undefined
  const hooks = hookSummaries(p.spec)
  return <>
    <tr className="border-t align-top">
      <td className="py-2 pr-3 break-all font-mono text-xs">{p.id}</td>
      <td className="pr-3 break-all">{scopeLabel(p.spec)}</td>
      <td className="pr-3">{hooks === null ? 'unknown' : hooks.length === 0 ? 'none' : <ul className="space-y-0.5">{hooks.map((h) => <li key={h.key}>{h.text}</li>)}</ul>}</td>
      <td className="pr-3">{known ? p.revision : 'unknown'}</td>
      <td className="whitespace-nowrap space-x-2">
        <button type="button" className={btn} aria-label={`Test ${p.id}`} onClick={onTest}>Test</button>
        <button type="button" className={btn} disabled={!known || stale || editing} title={why ?? staleWhy} aria-label={`Edit ${p.id}`} onClick={onEdit}>Edit</button>
        <button type="button" className={btn} disabled={!known || stale || confirming} title={why ?? staleWhy} aria-label={`Delete ${p.id}`} onClick={() => { remove.reset(); setConfirming(true) }}>Delete</button>
      </td>
    </tr>
    {(confirming || remove.isError) && <tr><td colSpan={5} className="pb-3"><div className="border rounded p-3 space-y-2">
      {confirming && !conflict && <p>Delete hook policy <strong>{p.id}</strong>? Traffic it covers is no longer inspected by its hooks.</p>}
      {conflict && <div role="alert" className="text-status-denied-fg"><p>{errorStatus(remove.error) === 404 ? 'This hook policy no longer exists. Nothing was deleted.' : CHANGED}</p>
        <button type="button" className={`${btn} mt-2`} onClick={() => { setConfirming(false); remove.reset(); onReload() }}>Reload latest version</button></div>}
      {remove.isError && !conflict && <p role="alert" className="text-status-denied-fg">Could not delete: {msg(remove.error)}</p>}
      {confirming && !conflict && <div className="flex gap-2">
        <button type="button" className={primary} disabled={remove.isPending} onClick={() => remove.mutate()}>{remove.isPending ? 'Deleting…' : 'Confirm delete'}</button>
        <button type="button" className={btn} disabled={remove.isPending} onClick={() => { setConfirming(false); remove.reset() }}>Cancel</button>
      </div>}
    </div></td></tr>}
  </>
}

/** Parse the document locally only to learn its id; the text itself is what is sent, so the server sees every field. */
const YAML_HINT = 'To apply a YAML file, use `aiplex hooks policies set -f FILE`.'
function documentId(text: string): { id: string } | { problem: string } {
  let parsed: unknown
  // The parser's own message can quote the text, so it is never shown.
  try { parsed = JSON.parse(text) } catch {
    const first = text.split('\n').find((l) => l.trim() !== '') ?? ''
    const yaml = /^\s*[A-Za-z_][\w-]*\s*:(\s|$)/.test(first)
    return { problem: `${yaml ? 'This looks like YAML, but the' : 'This is not valid JSON. The'} Console editor takes JSON only. ${YAML_HINT}` }
  }
  const id = str(obj(parsed)?.id)
  return id ? { id } : { problem: 'The document must be a JSON object with a string "id".' }
}

function PolicyEditor({ editor, onClose, onSaved, onReload }: { editor: { mode: 'create' } | { mode: 'edit'; record: HookPolicyRecord }; onClose: () => void; onSaved: () => void; onReload: () => void }) {
  const record = editor.mode === 'edit' ? editor.record : null
  const [text, setText] = useState(record ? (record.spec === undefined ? '' : JSON.stringify(record.spec, null, 2)) : STARTER)
  const [local, setLocal] = useState<string | null>(null)
  const [conflict, setConflict] = useState(false)
  const save = useMutation({
    mutationFn: (v: { id: string; revision: number }) => putHookPolicy(v.id, v.revision, text),
    onSuccess: onSaved,
    // For a create the server answers 409 REVISION_CONFLICT when the id exists (store.go Put, expected 0 against a live record).
    // That is not a stale edit: the text stays editable so the id can be changed.
    onError: (e) => { setConflict(record !== null && (isRevisionConflict(e) || errorStatus(e) === 404)) },
  })
  const known = record === null || typeof record.revision === 'number'
  const locked = save.isPending || conflict
  const submit = () => {
    setConflict(false); save.reset()
    const doc = documentId(text)
    if ('problem' in doc) { setLocal(doc.problem); return }
    if (record && doc.id !== record.id) { setLocal(`The document id "${doc.id}" differs from the policy being edited ("${record.id}"). Change it back, or add a new policy instead.`); return }
    setLocal(null)
    save.mutate({ id: doc.id, revision: record ? (record.revision as number) : 0 })
  }
  const exists = save.isError && record === null && isRevisionConflict(save.error)
  const refused = save.isError && !conflict && !exists && errorStatus(save.error) === 400
  return <div className="border rounded-lg p-4 space-y-3" role="group" aria-label={record ? `Edit hook policy ${record.id}` : 'Add hook policy'}>
    <h2 className="font-semibold">{record ? `Edit hook policy ${record.id}` : 'Add hook policy'}</h2>
    {record && record.spec === undefined && <p role="alert" className="text-status-denied-fg text-sm">The stored document is unknown. Write the complete policy below; nothing is kept implicitly.</p>}
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-3">
      <div>
        <label htmlFor="hk-doc" className="block text-sm font-medium mb-1">Policy document (JSON)</label>
        <textarea id="hk-doc" className={mono} rows={18} spellCheck={false} value={text} disabled={locked} onChange={(e) => { setText(e.target.value); setLocal(null); if (save.isError) save.reset() }} />
        <p className="text-xs mt-1">The same document <code>aiplex hooks policies set -f</code> takes, as JSON (that command also takes YAML). The server checks it when you save.</p>
      </div>
      <div aria-live="polite" className="space-y-2">
        <h3 className="text-sm font-medium">Server validation</h3>
        {!local && !refused && <p className="text-xs text-gray-600 dark:text-gray-400">Nothing yet. The server&apos;s answer appears here when you save.</p>}
        {local && <p role="alert" className="text-status-denied-fg text-sm whitespace-pre-wrap break-words">{local}</p>}
        {refused && <p role="alert" className="text-status-denied-fg text-sm whitespace-pre-wrap break-words">{validationMessage(save.error)}</p>}
        {exists && <p role="alert" className="text-status-denied-fg text-sm">A hook policy with this id already exists. Nothing was saved. Change the id here, or edit the existing policy from the list.</p>}
        {save.isError && !conflict && !refused && !exists && <p role="alert" className="text-status-denied-fg text-sm">Could not save: {msg(save.error)}</p>}
        {conflict && <div role="alert" className="text-status-denied-fg text-sm"><p>{errorStatus(save.error) === 404 ? 'This hook policy no longer exists. Nothing was saved.' : CHANGED}</p>
          <button type="button" className={`${btn} mt-2`} onClick={onReload}>Reload latest version</button></div>}
      </div>
    </div>
    <div className="flex gap-2">
      <button type="button" className={primary} disabled={!known || locked || text.trim() === ''} title={known ? undefined : 'The revision is unknown. Reload first.'} onClick={submit}>{save.isPending ? 'Saving…' : record ? 'Save changes' : 'Create hook policy'}</button>
      <button type="button" className={btn} disabled={save.isPending} onClick={onClose}>Cancel</button>
    </div>
  </div>
}

const count = (n: number | undefined) => (typeof n === 'number' ? String(n) : 'unknown')
const hookName = (r: HookResult) => r.id || 'unknown hook'
/**
 * The sample can be a real prompt or a secret someone is testing redaction on. It lives in this
 * component's state and in the mutation's own state, nowhere else: not storage, not the URL, not
 * the query cache, not the log. Both are cleared when the component unmounts, which happens on
 * leaving the screen and, because the parent keys it by policy id, on choosing another policy.
 */
function DryRun({ id }: { id: string }) {
  const [text, setText] = useState('')
  const [phase, setPhase] = useState<HookPhase>('request')
  const [plane, setPlane] = useState<HookPlane>('llmplex')
  // gcTime 0: the mutation (and the sample in its variables) is dropped as soon as nothing observes it.
  const test = useMutation({ mutationFn: (v: { texts: string[]; phase: HookPhase; plane: HookPlane }) => testHookPolicy(id, v), gcTime: 0 })
  const { reset } = test
  useEffect(() => () => reset(), [reset])
  const result: HookTestResult | undefined = test.data
  return <div className="space-y-3">
    <p className="text-xs text-gray-600 dark:text-gray-400">The sample is sent only to this policy&apos;s test route and the server does not store or log it. The Console keeps it in memory only, and clears it when you leave this screen or choose another policy. An <code>http</code> hook sends the sample to its sidecar.</p>
    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
      <div><label htmlFor="hk-phase" className="block text-sm font-medium mb-1">Phase</label>
        <select id="hk-phase" className={field} value={phase} onChange={(e) => { setPhase(e.target.value as HookPhase); reset() }}><option value="request">request</option><option value="response">response</option></select></div>
      <div><label htmlFor="hk-plane" className="block text-sm font-medium mb-1">Plane</label>
        <select id="hk-plane" className={field} value={plane} onChange={(e) => { setPlane(e.target.value as HookPlane); reset() }}><option value="llmplex">llmplex</option><option value="mcplex">mcplex</option></select>
        <p className="text-xs mt-1">The plane is passed to <code>http</code> sidecar hooks; the test route accepts exactly these two values whether or not the module is enabled.</p></div>
    </div>
    <div><label htmlFor="hk-sample" className="block text-sm font-medium mb-1">Sample text</label>
      <textarea id="hk-sample" className={mono} rows={5} spellCheck={false} autoComplete="off" value={text} onChange={(e) => { setText(e.target.value); reset() }} /></div>
    <button type="button" className={primary} disabled={test.isPending || text === ''} onClick={() => test.mutate({ texts: [text], phase, plane })}>{test.isPending ? 'Running…' : 'Run test'}</button>
    {test.isError && <p role="alert" className="text-status-denied-fg text-sm whitespace-pre-wrap break-words">Test failed: {msg(test.error)}</p>}
    {result && <Decision result={result} />}
  </div>
}

function Decision({ result }: { result: HookTestResult }) {
  const decision = result.decision
  const hookResults = result.results ?? []
  const failedOpen = hookResults.filter((r) => r.fail_open_used)
  const outputs = result.texts ?? []
  const names = failedOpen.map(hookName).join(', ')
  return <div role="status" aria-label="Dry run result" className="border rounded p-3 space-y-2 text-sm">
    <p className="text-xs">Policy {result.policy || 'unknown'}, phase {result.phase || 'unknown'}.</p>
    {decision === 'allow' && failedOpen.length === 0 && <p><strong>Allowed.</strong> The sample passed unchanged.</p>}
    {decision === 'allow' && failedOpen.length > 0 && <p><strong>Allowed only because a hook failed open: {names}.</strong> That hook could not decide, so its check did not apply to this sample. Do not treat this policy as working until it is fixed.</p>}
    {decision === 'transform' && <p><strong>Redacted.</strong> The policy changed the sample.</p>}
    {decision === 'deny' && <p><strong>Refused.</strong></p>}
    {decision !== 'allow' && decision !== 'transform' && decision !== 'deny' && <p><strong>Decision: {decision || 'unknown'}.</strong></p>}
    {decision !== 'allow' && failedOpen.length > 0 && <p role="alert" className="text-status-denied-fg">A hook failed open on this sample and its check did not apply: {names}.</p>}
    {decision === 'deny' && <p>Reason: {result.message || 'unknown'}{result.code ? ` (${result.code})` : ''}</p>}
    {decision !== 'deny' && (outputs.length > 0
      ? <div><p className="font-medium">Output text</p>{outputs.map((t, i) => <pre key={i} className="font-mono text-xs whitespace-pre-wrap break-words border rounded p-2">{t}</pre>)}</div>
      : <p>Output text: unknown</p>)}
    <div><p className="font-medium">Hooks that ran</p>
      {hookResults.length === 0 ? <p>none reported</p> : <div className="overflow-x-auto"><table className="w-full text-xs text-left">
        <thead><tr><th className="pr-3">Hook</th><th className="pr-3">Type</th><th className="pr-3">Phase</th><th className="pr-3">Decision</th><th className="pr-3">Time</th><th className="pr-3">Rule matches</th><th>Note</th></tr></thead>
        <tbody>{hookResults.map((r, i) => <tr key={i} className="border-t align-top">
          <td className="pr-3 font-mono">{r.id || 'unknown'}</td><td className="pr-3">{r.type || 'unknown'}</td><td className="pr-3">{r.phase || 'unknown'}</td>
          <td className="pr-3">{r.decision || 'unknown'}</td><td className="pr-3">{typeof r.duration_ms === 'number' ? `${r.duration_ms} ms` : 'unknown'}</td>
          <td className="pr-3 font-mono">{(r.rules ?? []).length === 0 ? 'none' : (r.rules ?? []).map((rule, j) => <span key={j} className="block">{rule.id}: {count(rule.count)}</span>)}</td>
          <td>{r.fail_open_used ? 'Failed open: its decision was not applied.' : r.decision === 'error' ? 'This hook could not decide (error or timeout).' : r.decision === 'skipped' ? 'This hook did not run.' : ''}</td>
        </tr>)}</tbody>
      </table></div>}</div>
    <p>Parts not inspected: {count(result.uninspected_parts)}</p>
  </div>
}
