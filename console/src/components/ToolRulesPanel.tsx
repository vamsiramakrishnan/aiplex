import { useState } from 'react'
import { isRevisionConflict, type ToolConnectionRecord } from '../api/toolAuth'
import { errorCode, errorMessage, knownRevision, parseRulesInput, putRules, rulesOf, type ToolRule } from '../api/toolConnections'
import ToolRulesDryRun from './ToolRulesDryRun'

const inputClass = 'w-full border rounded px-3 py-2 text-sm'
const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50'
const primary = 'px-3 py-1.5 text-sm bg-brand-600 text-brand-contrast rounded disabled:opacity-50'

interface Props {
  record: ToolConnectionRecord
  /** The list this record came from may be out of date: no writes until a reload succeeds. */
  stale: boolean
  onSaved: (record: ToolConnectionRecord) => void
  onReload: () => Promise<void>
  onClose: () => void
}

/**
 * Policy rules of one connection: list in evaluation order, add, remove, replace
 * the whole set, and a dry run. Expressions are compiled and evaluated by the
 * server only. When it refuses a rule, its message is shown unchanged and nothing
 * is saved. Every write sends the whole connection with the revision read, so a
 * concurrent change is a conflict and members this page does not know are kept.
 * The parent keys this panel by connection id.
 */
export default function ToolRulesPanel({ record, stale, onSaved, onReload, onClose }: Props) {
  const rules = rulesOf(record.spec)
  const revisionKnown = knownRevision(record.revision)
  const writable = !stale && revisionKnown
  const why = stale ? 'Reload first: the connection may be out of date.' : !revisionKnown ? 'The revision is unknown, so a change cannot be checked against it. Reload first.' : undefined

  const [conflictAt, setConflictAt] = useState<number | null>(null)
  const conflict = conflictAt === record.revision
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<{ message: string; code?: string } | null>(null)
  const [removing, setRemoving] = useState<string | null>(null)
  const [replacing, setReplacing] = useState<{ text: string; parsed: Record<string, unknown>[] | null } | null>(null)
  const [replaceError, setReplaceError] = useState<string | null>(null)
  const [draft, setDraft] = useState({ id: '', effect: 'deny', when: '', message: '', description: '', before: '', replace: false })
  const [draftError, setDraftError] = useState<string | null>(null)

  const write = async (next: Record<string, unknown>[]): Promise<boolean> => {
    if (busy || !writable) return false
    setBusy(true)
    setFailure(null)
    try {
      onSaved(await putRules(record, next))
      return true
    } catch (e) {
      if (isRevisionConflict(e)) setConflictAt(record.revision)
      else setFailure({ message: errorMessage(e), code: errorCode(e) })
      return false
    } finally { setBusy(false) }
  }

  const reload = async () => {
    setBusy(true)
    // A reload ends the attempt: the failure banner and an open confirmation belong to the version that was replaced.
    setFailure(null)
    setRemoving(null)
    try { await onReload() } catch (e) { setFailure({ message: errorMessage(e), code: errorCode(e) }) } finally { setBusy(false) }
  }

  const add = async (event: React.FormEvent) => {
    event.preventDefault()
    setDraftError(null)
    const id = draft.id.trim()
    if (!id) return setDraftError('A rule id is required')
    if (!draft.when.trim()) return setDraftError('An expression is required')
    const at = rules.findIndex((r) => r.id === id)
    if (at >= 0 && !draft.replace) return setDraftError(`A rule ${id} already exists. Tick "Replace the rule with this id", or remove it first.`)
    // A replaced rule keeps the members of the stored rule the Console does not model; only the optional fields the user emptied are cleared.
    const rule: ToolRule = { ...(at >= 0 ? rules[at] : {}), id, effect: draft.effect, when: draft.when }
    if (draft.message.trim()) rule.message = draft.message
    else delete rule.message
    if (draft.description.trim()) rule.description = draft.description
    else delete rule.description
    const next = [...rules]
    if (at >= 0 && (!draft.before || draft.before === id)) {
      // Replacing with "before itself" keeps its place; the default position moves it to the end as a new rule would go.
      if (draft.before === id) next[at] = rule
      else { next.splice(at, 1); next.push(rule) }
    } else {
      // The position is resolved against the list as shown, then the replaced rule is taken out.
      const pos = draft.before ? rules.findIndex((r) => r.id === draft.before) : -1
      if (draft.before && pos < 0) return setDraftError(`${draft.before} is not a rule of ${record.id}`)
      if (draft.before) next.splice(pos, 0, rule)
      else next.push(rule)
      if (at >= 0) next.splice(at < pos ? at : at + 1, 1)
    }
    if (await write(next)) setDraft({ id: '', effect: 'deny', when: '', message: '', description: '', before: '', replace: false })
  }

  const remove = async (id: string) => {
    if (await write(rules.filter((r) => r.id !== id))) setRemoving(null)
  }

  const startReplace = (event: React.FormEvent) => {
    event.preventDefault()
    const parsed = parseRulesInput(replacing?.text ?? '')
    if (!parsed.ok) { setReplaceError(parsed.error); return }
    setReplaceError(null)
    setReplacing({ text: replacing?.text ?? '', parsed: parsed.rules })
  }
  const confirmReplace = async () => {
    if (replacing?.parsed && await write(replacing.parsed)) { setReplacing(null) }
  }

  return (
    <section aria-label={`Policy rules of ${record.id}`} className="bg-white rounded-lg shadow p-4 mt-4 dark:bg-gray-900 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="font-semibold">Policy rules: {record.id}</h3>
          <p className="text-xs text-gray-600 dark:text-gray-400">Revision {revisionKnown ? record.revision : 'unknown'}</p>
        </div>
        <button type="button" className={btn} onClick={onClose}>Close</button>
      </div>
      <p className="text-xs text-gray-600 dark:text-gray-400">
        Rules decide allow or deny on a tool call from the tool, its arguments, the caller and the time. They run in order after the allow and deny lists, the caller&rsquo;s
        scope and read-only. The first matching deny refuses; once any allow rule exists, a call that matches none is refused. A rule that cannot be evaluated refuses the call.
        Expressions are checked by the server when saved: this page does not evaluate them.
      </p>
      {stale && <p role="alert" className="text-status-denied-fg text-sm">This connection was read earlier and may be out of date. Reload before changing rules.</p>}
      {conflict && (
        <div role="alert" className="border border-status-warning-fg bg-status-warning-bg text-status-warning-fg rounded p-3 text-sm">
          <p className="font-medium">This connection was changed by someone else.</p>
          <p className="mb-2">Your change was not saved. Reload the latest version, review it, then make the change again.</p>
          <button type="button" className={btn} onClick={reload} disabled={busy}>Reload latest version</button>
        </div>
      )}
      {failure && (
        <div role="alert" className="text-status-denied-fg text-sm">
          <p className="font-medium">Could not save. Nothing was changed. The server said:</p>
          <pre className="whitespace-pre-wrap font-mono text-xs mt-1">{failure.message}</pre>
          {failure.code && <p className="text-xs mt-1">Code: {failure.code}</p>}
        </div>
      )}

      {rules.length === 0 ? <p role="status" className="text-sm">No rules on {record.id}.</p> : (
        <table className="w-full text-sm">
          <caption className="sr-only">Rules of {record.id} in evaluation order</caption>
          <thead><tr>
            <th scope="col" className="text-left py-2 pr-3">#</th><th scope="col" className="text-left pr-3">Id</th><th scope="col" className="text-left pr-3">Effect</th>
            <th scope="col" className="text-left pr-3">When</th><th scope="col" className="text-left pr-3">Message</th><th scope="col"><span className="sr-only">Actions</span></th>
          </tr></thead>
          <tbody>
            {rules.map((r, i) => (
              <tr key={`${r.id}-${i}`} className="border-t align-top">
                <td className="py-2 pr-3">{i + 1}</td>
                <td className="pr-3 font-mono text-xs">{String(r.id)}</td>
                <td className="pr-3">{String(r.effect)}</td>
                <td className="pr-3"><code className="text-xs whitespace-pre-wrap break-all">{String(r.when)}</code></td>
                <td className="pr-3">{r.message ? String(r.message) : '-'}</td>
                <td className="text-right whitespace-nowrap">
                  <button type="button" className={btn} disabled={!writable || busy} title={why} aria-label={`Remove rule ${r.id}`} onClick={() => setRemoving(String(r.id))}>Remove</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {removing && (
        <div role="group" aria-label="Confirm rule removal" className="border rounded p-3 text-sm space-y-2">
          <p>Remove rule <strong>{removing}</strong> from {record.id}? Calls it refused are no longer refused.</p>
          <div className="flex gap-2">
            <button type="button" className={primary} disabled={busy || !writable} onClick={() => void remove(removing)}>{busy ? 'Removing…' : 'Confirm remove'}</button>
            <button type="button" className={btn} disabled={busy} onClick={() => setRemoving(null)}>Cancel</button>
          </div>
        </div>
      )}

      {(
        <form onSubmit={add} aria-label="Add a rule" noValidate className="border-t pt-3 space-y-3">
          <h4 className="text-sm font-medium">Add a rule</h4>
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <div><label htmlFor="rule-id" className="block text-sm font-medium mb-1">Rule id</label>
              <input id="rule-id" className={inputClass} value={draft.id} onChange={(e) => setDraft({ ...draft, id: e.target.value })} autoComplete="off" spellCheck={false} /></div>
            <div><label htmlFor="rule-effect" className="block text-sm font-medium mb-1">Effect</label>
              <select id="rule-effect" className={inputClass} value={draft.effect} onChange={(e) => setDraft({ ...draft, effect: e.target.value })}><option value="deny">deny</option><option value="allow">allow</option></select></div>
            <div><label htmlFor="rule-before" className="block text-sm font-medium mb-1">Position</label>
              <select id="rule-before" className={inputClass} value={draft.before} onChange={(e) => setDraft({ ...draft, before: e.target.value })}>
                <option value="">At the end</option>
                {rules.map((r) => <option key={String(r.id)} value={String(r.id)}>Before {String(r.id)}</option>)}
              </select></div>
          </div>
          <div><label htmlFor="rule-when" className="block text-sm font-medium mb-1">Expression (when)</label>
            <textarea id="rule-when" className={`${inputClass} font-mono`} rows={3} value={draft.when} onChange={(e) => setDraft({ ...draft, when: e.target.value })} spellCheck={false} aria-describedby="rule-when-hint" />
            <p id="rule-when-hint" className="text-xs text-gray-600 dark:text-gray-400 mt-1">
              A CEL boolean over tool, args, caller, access_point and request, for example <code>path_under(args.path, &#39;/etc&#39;)</code>. The server checks it when you save and refuses one that does not type-check.
            </p></div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div><label htmlFor="rule-message" className="block text-sm font-medium mb-1">Message to the caller (optional)</label>
              <input id="rule-message" className={inputClass} value={draft.message} onChange={(e) => setDraft({ ...draft, message: e.target.value })} autoComplete="off" />
              <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">Shown to the caller when this rule refuses a call. Write it as something safe to publish.</p></div>
            <div><label htmlFor="rule-description" className="block text-sm font-medium mb-1">Description (optional, operators only)</label>
              <input id="rule-description" className={inputClass} value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} autoComplete="off" /></div>
          </div>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={draft.replace} onChange={(e) => setDraft({ ...draft, replace: e.target.checked })} /> Replace the rule with this id</label>
          {draftError && <p role="alert" className="text-xs text-status-denied-fg">{draftError}</p>}
          <button type="submit" className={primary} disabled={!writable || busy} title={why}>{busy && !removing && !replacing?.parsed ? 'Saving…' : 'Add rule'}</button>
        </form>
      )}

      {(
        <div className="border-t pt-3 space-y-2">
          <h4 className="text-sm font-medium">Replace all rules</h4>
          {!replacing ? (
            <div className="flex gap-2">
              <button type="button" className={btn} disabled={!writable || busy} title={why} onClick={() => setReplacing({ text: JSON.stringify(rules, null, 2), parsed: null })}>Replace the whole set…</button>
            </div>
          ) : !replacing.parsed ? (
            <form onSubmit={startReplace} aria-label="Replace rules" noValidate className="space-y-2">
              <label htmlFor="rules-set" className="block text-sm font-medium">Rules (JSON list, or an object with a &ldquo;rules&rdquo; list)</label>
              <textarea id="rules-set" className={`${inputClass} font-mono`} rows={10} value={replacing.text} onChange={(e) => setReplacing({ text: e.target.value, parsed: null })} spellCheck={false} />
              {replaceError && <p role="alert" className="text-xs text-status-denied-fg">{replaceError}</p>}
              <div className="flex gap-2">
                <button type="submit" className={primary}>Review replacement</button>
                <button type="button" className={btn} onClick={() => { setReplacing(null); setReplaceError(null) }}>Cancel</button>
              </div>
            </form>
          ) : (
            <div role="group" aria-label="Confirm replacement" className="border rounded p-3 text-sm space-y-2">
              <p>Replace all {rules.length} rules of {record.id} with the {replacing.parsed.length} you entered? Rules that are not in the list are removed{replacing.parsed.length === 0 ? ' (an empty list removes every rule)' : ''}.</p>
              <div className="flex gap-2">
                <button type="button" className={primary} disabled={busy || !writable} onClick={() => void confirmReplace()}>{busy ? 'Replacing…' : 'Confirm replace'}</button>
                <button type="button" className={btn} disabled={busy} onClick={() => setReplacing({ text: replacing.text, parsed: null })}>Back</button>
              </div>
            </div>
          )}
        </div>
      )}

      <ToolRulesDryRun connectionId={record.id} />
    </section>
  )
}
