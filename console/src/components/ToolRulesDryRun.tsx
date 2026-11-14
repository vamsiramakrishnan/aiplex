import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { errorCode, errorMessage, parseRulesInput, testToolRules, type RulesTestSample } from '../api/toolConnections'
import { notValidJson, splitList } from '../api/toolAuth'

const inputClass = 'w-full border rounded px-3 py-2 text-sm'
const primary = 'px-3 py-1.5 text-sm bg-brand-600 text-brand-contrast rounded disabled:opacity-50'
const ANNOTATIONS = ['read_only', 'destructive', 'idempotent', 'open_world'] as const

/**
 * Dry run: the server evaluates the connection's rules (or candidate rules typed
 * here) against a sample call and says which rule matched. Nothing is stored and
 * nothing is evaluated in the browser. Operators only, as the route requires.
 */
export default function ToolRulesDryRun({ connectionId }: { connectionId: string }) {
  const [f, setFRaw] = useState({ tool: '', args: '', kind: '', principal: '', groups: '', groupsUnknown: false, agent: '', client: '', accessPoint: '', time: '', candidate: '' })
  const [ann, setAnnRaw] = useState<Record<string, string>>({})
  const [localError, setLocalError] = useState<string | null>(null)
  const run = useMutation({ mutationFn: (sample: RulesTestSample) => testToolRules(connectionId, sample) })
  // A result answers the input it was run with: any change to the input, or a new attempt, clears it.
  const setF: typeof setFRaw = (v) => { setFRaw(v); run.reset() }
  const setAnn: typeof setAnnRaw = (v) => { setAnnRaw(v); run.reset() }

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    setLocalError(null)
    run.reset()
    const sample: RulesTestSample = { tool: f.tool.trim(), subject: {} }
    if (!sample.tool) return setLocalError('A tool name is required')
    if (f.args.trim()) {
      try {
        const args: unknown = JSON.parse(f.args)
        if (!args || typeof args !== 'object' || Array.isArray(args)) return setLocalError('The sample arguments must be a JSON object')
        sample.args = args
      } catch { return setLocalError(`The sample arguments: ${notValidJson(f.args)}`) }
    }
    if (f.kind) sample.subject.kind = f.kind
    if (f.principal.trim()) sample.subject.principal = f.principal.trim()
    const groups = splitList(f.groups, /[\s,]+/)
    if (groups.length) sample.subject.groups = groups
    if (f.groupsUnknown) sample.subject.groups_known = false
    if (f.agent.trim()) sample.subject.agent = f.agent.trim()
    if (f.client.trim()) sample.subject.client = f.client.trim()
    if (f.accessPoint.trim()) sample.access_point = f.accessPoint.trim()
    const annotations: NonNullable<RulesTestSample['annotations']> = {}
    for (const name of ANNOTATIONS) if (ann[name] === 'true' || ann[name] === 'false') annotations[name] = ann[name] === 'true'
    if (Object.keys(annotations).length) sample.annotations = annotations
    if (f.time.trim()) sample.time = f.time.trim()
    if (f.candidate.trim()) {
      const parsed = parseRulesInput(f.candidate)
      if (!parsed.ok) return setLocalError(`Candidate rules: ${parsed.error}`)
      sample.rules = parsed.rules
    }
    run.mutate(sample)
  }

  const result = run.data
  return (
    <form onSubmit={submit} aria-label="Rules dry run" noValidate className="border-t pt-3 space-y-3">
      <h4 className="text-sm font-medium">Dry run</h4>
      <p className="text-xs text-gray-600 dark:text-gray-400">
        The gateway evaluates the rules against a sample call and shows which rule decided. Only rules are evaluated: the tool lists, the caller&rsquo;s scope, read-only, quota and hooks are not part of it.
        Nothing is stored or recorded. Use sample values, not real data or credentials.
      </p>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div><label htmlFor="dr-tool" className="block text-sm font-medium mb-1">Tool name</label>
          <input id="dr-tool" className={inputClass} value={f.tool} onChange={(e) => setF({ ...f, tool: e.target.value })} autoComplete="off" spellCheck={false} /></div>
        <div><label htmlFor="dr-kind" className="block text-sm font-medium mb-1">Caller kind</label>
          <select id="dr-kind" className={inputClass} value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}>
            <option value="">Not set</option><option value="user">user</option><option value="agent">agent</option><option value="client">client</option></select></div>
      </div>
      <div><label htmlFor="dr-args" className="block text-sm font-medium mb-1">Sample arguments (JSON object)</label>
        <textarea id="dr-args" className={`${inputClass} font-mono`} rows={3} value={f.args} onChange={(e) => setF({ ...f, args: e.target.value })} spellCheck={false} /></div>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div><label htmlFor="dr-principal" className="block text-sm font-medium mb-1">Caller principal</label>
          <input id="dr-principal" className={inputClass} value={f.principal} onChange={(e) => setF({ ...f, principal: e.target.value })} autoComplete="off" /></div>
        <div><label htmlFor="dr-groups" className="block text-sm font-medium mb-1">Caller groups (comma separated)</label>
          <input id="dr-groups" className={inputClass} value={f.groups} onChange={(e) => setF({ ...f, groups: e.target.value })} autoComplete="off" />
          <label className="flex items-center gap-2 text-xs mt-1"><input type="checkbox" checked={f.groupsUnknown} onChange={(e) => setF({ ...f, groupsUnknown: e.target.checked })} /> Group membership unknown</label></div>
        <div><label htmlFor="dr-agent" className="block text-sm font-medium mb-1">Caller agent</label>
          <input id="dr-agent" className={inputClass} value={f.agent} onChange={(e) => setF({ ...f, agent: e.target.value })} autoComplete="off" /></div>
        <div><label htmlFor="dr-client" className="block text-sm font-medium mb-1">Caller client</label>
          <input id="dr-client" className={inputClass} value={f.client} onChange={(e) => setF({ ...f, client: e.target.value })} autoComplete="off" /></div>
        <div><label htmlFor="dr-ap" className="block text-sm font-medium mb-1">Access point (optional)</label>
          <input id="dr-ap" className={inputClass} value={f.accessPoint} onChange={(e) => setF({ ...f, accessPoint: e.target.value })} autoComplete="off" /></div>
        <div><label htmlFor="dr-time" className="block text-sm font-medium mb-1">request.time (RFC 3339, default now)</label>
          <input id="dr-time" className={inputClass} value={f.time} onChange={(e) => setF({ ...f, time: e.target.value })} autoComplete="off" placeholder="2026-11-10T12:00:00Z" /></div>
      </div>
      <fieldset className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <legend className="text-sm font-medium">Simulated tool annotations</legend>
        {ANNOTATIONS.map((name) => (
          <div key={name}><label htmlFor={`dr-ann-${name}`} className="block text-xs mb-1">{name}</label>
            <select id={`dr-ann-${name}`} className={inputClass} value={ann[name] ?? ''} onChange={(e) => setAnn({ ...ann, [name]: e.target.value })}>
              <option value="">Not set (use observed)</option><option value="true">true</option><option value="false">false</option></select></div>
        ))}
      </fieldset>
      <div><label htmlFor="dr-candidate" className="block text-sm font-medium mb-1">Candidate rules instead of the saved ones (optional JSON)</label>
        <textarea id="dr-candidate" className={`${inputClass} font-mono`} rows={4} value={f.candidate} onChange={(e) => setF({ ...f, candidate: e.target.value })} spellCheck={false} />
        <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">Try rules before saving them. They replace the saved rules for this test only.</p></div>
      {localError && <p role="alert" className="text-xs text-status-denied-fg">{localError}</p>}
      <button type="submit" className={primary} disabled={run.isPending}>{run.isPending ? 'Testing…' : 'Run dry run'}</button>
      {run.isError && (
        <div role="alert" className="text-status-denied-fg text-sm">
          <p className="font-medium">The dry run failed. The server said:</p>
          <pre className="whitespace-pre-wrap font-mono text-xs mt-1">{errorMessage(run.error)}</pre>
          {errorCode(run.error) && <p className="text-xs mt-1">Code: {errorCode(run.error)}</p>}
        </div>
      )}
      {result && (
        <div aria-label="Dry run result" role="region" className="border rounded p-3 text-sm space-y-2">
          <p>Decision: <strong>{result.decision}</strong>
            {result.error_class ? ` (${result.error_class}${result.rule ? `, rule ${result.rule}` : ''}${result.reason ? `, ${result.reason}` : ''})` : ''}</p>
          {!result.error_class && result.rule && <p>Decided by rule <code>{result.rule}</code></p>}
          {result.message && <p>Caller sees: {result.message}</p>}
          {result.list_hidden === true && <p>tools/list hides this tool for this caller.</p>}
          {typeof result.list_hidden !== 'boolean' && <p>Whether tools/list hides this tool: not reported.</p>}
          {!Array.isArray(result.trace) ? <p>Rule results: not reported.</p> : result.trace.length === 0 ? <p>No rules to evaluate.</p> : (
            <table className="w-full text-sm">
              <caption className="sr-only">Result of each rule</caption>
              <thead><tr><th scope="col" className="text-left pr-3">Set</th><th scope="col" className="text-left pr-3">Rule</th><th scope="col" className="text-left pr-3">Effect</th><th scope="col" className="text-left pr-3">Result</th><th scope="col" className="text-left">Error</th></tr></thead>
              <tbody>
                {result.trace.map((t, i) => (
                  <tr key={`${t.set}-${t.id}-${i}`} className="border-t align-top">
                    <td className="pr-3">{t.set}</td><td className="pr-3 font-mono text-xs">{t.id}</td><td className="pr-3">{t.effect}</td>
                    <td className="pr-3">{t.result}</td><td className="whitespace-pre-wrap">{t.error || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {result.note && <p className="text-xs text-gray-600 dark:text-gray-400">{result.note}</p>}
        </div>
      )}
    </form>
  )
}
