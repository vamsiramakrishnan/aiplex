import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { verifyUserToken, type UserProvider, type UserVerifyResult } from '../api/userIdentity'

const CHECK_LABEL = { pass: 'Pass', fail: 'Fail', skipped: 'Skipped' } as const
const CHECK_STYLE = { pass: 'text-green-700', fail: 'text-red-700', skipped: 'text-gray-500' } as const

function describe(error: unknown): string {
  const status = (error as { status?: number } | null)?.status
  if (status === 403) return 'Could not test the token: testing requires the admin or deployer role.'
  return `Could not test the token: ${error instanceof Error ? error.message : 'unexpected error'}`
}

/**
 * Dry-runs a user token against the configured providers.
 *
 * The token lives only in this component's state and the request body. It is
 * cleared the moment it is submitted and is never written to storage, the URL,
 * react-query's mutation cache or the console; the call is deliberately made
 * outside useMutation, which would retain its variables.
 */
export default function UserTokenTest({ providers, initialProvider = '' }: { providers: UserProvider[]; initialProvider?: string }) {
  const uid = useId()
  const [token, setToken] = useState('')
  const [provider, setProvider] = useState(initialProvider)
  const [result, setResult] = useState<UserVerifyResult | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const run = useRef(0)
  useEffect(() => setProvider(initialProvider), [initialProvider])
  useEffect(() => () => { run.current++ }, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const submitted = token.trim()
    if (!submitted || busy) return
    setToken('')
    setResult(null)
    setError('')
    setBusy(true)
    const id = ++run.current
    try {
      const res = await verifyUserToken(submitted, provider || undefined)
      if (id === run.current) setResult(res)
    } catch (err) {
      if (id === run.current) setError(describe(err))
    } finally {
      if (id === run.current) setBusy(false)
    }
  }

  return (
    <section aria-label="Test a token" className="rounded border border-gray-200 dark:border-gray-800 p-3 space-y-2">
      <h3 className="text-sm font-semibold">Test a token</h3>
      <p className="text-xs text-gray-500">Paste a user&apos;s ID or access token to see which checks pass and who it would authenticate as. It is sent once for this test, never stored, and the box is cleared on submit.</p>
      <form onSubmit={submit} className="space-y-2">
        <div>
          <label htmlFor={`${uid}-provider`} className="block text-xs font-medium mb-1">Check against</label>
          <select id={`${uid}-provider`} value={provider} onChange={e => setProvider(e.target.value)} className="border rounded px-2 py-1 text-sm bg-transparent">
            <option value="">Any provider (match by issuer)</option>
            {providers.map(p => <option key={p.id} value={p.id}>{p.display_name ? `${p.display_name} (${p.id})` : p.id}</option>)}
          </select>
        </div>
        <div>
          <label htmlFor={`${uid}-token`} className="block text-xs font-medium mb-1">User token</label>
          <textarea id={`${uid}-token`} rows={3} value={token} onChange={e => setToken(e.target.value)}
            autoComplete="off" autoCorrect="off" autoCapitalize="off" spellCheck={false} name="user-token-test"
            className="w-full border rounded px-2 py-1 text-xs font-mono bg-transparent" />
        </div>
        <button type="submit" disabled={!token.trim() || busy} className="px-3 py-1 text-sm bg-brand-600 text-white rounded disabled:opacity-50">{busy ? 'Testing…' : 'Test token'}</button>
      </form>
      {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
      {result && <VerifyResult result={result} />}
    </section>
  )
}

export function VerifyResult({ result }: { result: UserVerifyResult }) {
  return (
    <div aria-label="Token test result" className="space-y-2">
      <p className={`text-sm font-medium ${result.valid ? 'text-green-700' : 'text-red-700'}`}>{result.valid ? 'Token is valid for this provider.' : 'Token is not valid.'}</p>
      <ul className="text-xs space-y-1">
        {result.checks.map((c, i) => (
          <li key={`${i}-${c.name}`} className="flex flex-wrap gap-2">
            <span className={`w-14 font-semibold ${CHECK_STYLE[c.status] ?? ''}`}>{CHECK_LABEL[c.status] ?? c.status}</span>
            <span className="font-mono">{c.name}</span>
            <span className="text-gray-600 dark:text-gray-400">{c.reason}</span>
          </li>
        ))}
      </ul>
      {result.valid && (
        <dl className="text-xs grid grid-cols-[auto,1fr] gap-x-3 gap-y-1">
          <dt className="text-gray-500">Principal</dt>
          <dd>{result.principal_id ? <code className="break-all">{result.principal_id}</code> : 'unknown'}</dd>
          <dt className="text-gray-500">Groups</dt>
          <dd>
            {result.groups_available
              ? (result.groups && result.groups.length > 0
                ? <ul className="list-disc ml-4">{result.groups.map(g => <li key={g}><code>{g}</code></li>)}</ul>
                : 'The token carries an empty group list (verified: this user is in no groups).')
              : <span role="note" className="text-amber-800 dark:text-amber-300">Groups unavailable. {result.groups_note || 'This token did not provide verified group membership.'} Unavailable is not the same as empty: group-based access will not match.</span>}
          </dd>
        </dl>
      )}
      {!result.live_authentication && <p className="text-xs text-yellow-800 dark:text-yellow-300">Live authentication is off: this is a dry run and the token would not yet authenticate real requests.</p>}
    </div>
  )
}
