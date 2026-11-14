import { useId, useState } from 'react'
import { splitList, validateAuthValues, type AuthValues, type ToolAuthCatalog, type ToolConnectionRecord } from '../api/toolAuth'
import {
  FORM_AUTH_TYPES, buildConnectionSpec, createToolConnection, errorStatus, errorWithCode, isAlreadyExists, parsePastedSpec, validateConnectionId, validateServerUrl,
  type TransportMode,
} from '../api/toolConnections'
import { FieldInput } from './ToolAuthEditor'

const inputClass = 'w-full border rounded px-3 py-2 text-sm'
const btn = 'px-4 py-2 bg-gray-100 text-sm rounded dark:bg-gray-800 disabled:opacity-50'
const primary = 'px-4 py-2 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700 disabled:opacity-50'

// Sensible starting values for enum fields, as the CLI defaults them.
const DEFAULTS: Record<string, string> = { in: 'header', client_auth_method: 'client_secret_basic' }

interface Props {
  catalog: ToolAuthCatalog
  onCreated: (record: ToolConnectionRecord) => void
  onCancel: () => void
}

/**
 * Create a connection. A credential is a secret reference: there is no field in
 * this form that accepts a secret value, and every reference field is checked
 * against the reference shape (and against the look of a credential) before any
 * request is made. Auth types the form does not offer go through "Paste a full
 * specification", which applies the same checks.
 */
export default function ToolConnectionCreate({ catalog, onCreated, onCancel }: Props) {
  const offered = FORM_AUTH_TYPES.flatMap((name) => catalog.types.filter((t) => t.type === name))
  const [mode, setMode] = useState<'form' | 'paste'>('form')
  const [id, setId] = useState('')
  const [url, setUrl] = useState('')
  const [transport, setTransport] = useState<TransportMode>('streamable-http')
  const [protocolVersion, setProtocolVersion] = useState('2025-06-18')
  const [authType, setAuthType] = useState(offered.find((t) => t.type === 'none')?.type ?? offered[0]?.type ?? '')
  const [values, setValues] = useState<AuthValues>({})
  const [tools, setTools] = useState('')
  const [pasted, setPasted] = useState('')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [specErrors, setSpecErrors] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const ids = { id: useId(), url: useId(), transport: useId(), version: useId(), type: useId(), tools: useId(), paste: useId() }

  const type = catalog.types.find((t) => t.type === authType)
  const changeType = (next: string) => {
    setAuthType(next)
    const t = catalog.types.find((x) => x.type === next)
    const start: AuthValues = {}
    for (const f of t?.fields ?? []) if (DEFAULTS[f.name] && f.enum?.includes(DEFAULTS[f.name])) start[f.name] = DEFAULTS[f.name]
    setValues(start)
    setErrors({})
    setFailure(null)
  }

  const write = async (cid: string, spec: Parameters<typeof createToolConnection>[1]) => {
    setSaving(true)
    setFailure(null)
    try { onCreated(await createToolConnection(cid, spec)) } catch (e) {
      const refused = errorStatus(e) === 409 || errorStatus(e) === 412
      setFailure(isAlreadyExists(e) ? errorWithCode(e)
        : refused ? `Nothing was saved: the server refused the create because a connection with this id exists now (it may have been created meanwhile). What you typed is kept. Server: ${errorWithCode(e)}`
        : `Could not create the connection: ${errorWithCode(e)}`)
    } finally { setSaving(false) }
  }

  const submitForm = async (event: React.FormEvent) => {
    event.preventDefault()
    if (saving || !type) return
    const found: Record<string, string> = {}
    const idProblem = validateConnectionId(id)
    if (idProblem) found.id = idProblem
    const urlProblem = validateServerUrl(url)
    if (urlProblem) found.url = urlProblem
    const names = splitList(tools, /[\n,]+/)
    if (names.length === 0) found.tools = 'At least one tool is required. The Console cannot discover tools from the server; list them here, or use: aiplex mcp connections add'
    if (!protocolVersion.trim()) found.version = 'Required'
    Object.assign(found, validateAuthValues(catalog, type, values))
    setErrors(found)
    if (Object.keys(found).length) return
    await write(id, buildConnectionSpec(catalog, { id, url, mode: transport, protocolVersion, authType, values, tools: names }))
  }

  const submitPaste = async (event: React.FormEvent) => {
    event.preventDefault()
    if (saving) return
    const parsed = parsePastedSpec(pasted, catalog)
    if (!parsed.ok) { setSpecErrors(parsed.errors); return }
    setSpecErrors([])
    await write(parsed.spec.id, parsed.spec)
  }

  const referenceHelp = (
    <p className="text-xs text-gray-600 dark:text-gray-400 mb-3">
      Credentials are entered as <strong>secret references</strong>: the name of a secret that already exists in the tenant secret store
      {catalog.secret_references.namespace ? ` (${catalog.secret_references.namespace})` : ''}. This form never takes a secret value. Store the secret first
      (with <code>aiplex dev</code>, set <code>AIPLEX_TOOL_SECRET_&lt;NAME&gt;</code> in the environment of the API process); the gateway checks the reference exists when
      the connection is saved and resolves it only when a tool is called.
    </p>
  )

  return (
    <section aria-label="Add tool connection" className="bg-white rounded-lg shadow p-4 mb-4 dark:bg-gray-900">
      <h3 className="font-semibold mb-2">Add a tool connection</h3>
      <div role="group" aria-label="How to describe the connection" className="flex gap-2 mb-3">
        <button type="button" aria-pressed={mode === 'form'} className={`${btn} ${mode === 'form' ? 'ring-2 ring-brand-600' : ''}`} onClick={() => { setMode('form'); setFailure(null) }}>Guided form</button>
        <button type="button" aria-pressed={mode === 'paste'} className={`${btn} ${mode === 'paste' ? 'ring-2 ring-brand-600' : ''}`} onClick={() => { setMode('paste'); setFailure(null) }}>Paste a full specification</button>
      </div>
      {failure && <p role="alert" className="text-status-denied-fg text-sm mb-3 whitespace-pre-wrap">{failure}</p>}

      {mode === 'form' ? (
        <form onSubmit={submitForm} aria-label="New tool connection form" noValidate>
          {referenceHelp}
          <div className="mb-3">
            <label htmlFor={ids.id} className="block text-sm font-medium mb-1">Connection id <span aria-hidden="true">*</span></label>
            <input id={ids.id} className={inputClass} value={id} onChange={(e) => setId(e.target.value)} autoComplete="off" spellCheck={false} aria-invalid={!!errors.id} />
            {errors.id && <p role="alert" className="text-xs text-status-denied-fg mt-1">id: {errors.id}</p>}
          </div>
          <div className="mb-3">
            <label htmlFor={ids.url} className="block text-sm font-medium mb-1">MCP server URL <span aria-hidden="true">*</span></label>
            <input id={ids.url} className={inputClass} value={url} onChange={(e) => setUrl(e.target.value)} autoComplete="off" spellCheck={false} placeholder="https://mcp.example.com/mcp" aria-invalid={!!errors.url} />
            <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">The streamable HTTP endpoint. No credentials in the URL.</p>
            {errors.url && <p role="alert" className="text-xs text-status-denied-fg mt-1">url: {errors.url}</p>}
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-3">
            <div>
              <label htmlFor={ids.transport} className="block text-sm font-medium mb-1">Transport</label>
              <select id={ids.transport} className={inputClass} value={transport} onChange={(e) => setTransport(e.target.value as TransportMode)}>
                <option value="streamable-http">streamable-http</option>
                <option value="json">json</option>
              </select>
            </div>
            <div>
              <label htmlFor={ids.version} className="block text-sm font-medium mb-1">MCP protocol version</label>
              <input id={ids.version} className={inputClass} value={protocolVersion} onChange={(e) => setProtocolVersion(e.target.value)} autoComplete="off" aria-invalid={!!errors.version} />
              {errors.version && <p role="alert" className="text-xs text-status-denied-fg mt-1">protocol version: {errors.version}</p>}
            </div>
          </div>
          <div className="mb-3">
            <label htmlFor={ids.type} className="block text-sm font-medium mb-1">Auth type</label>
            <select id={ids.type} className={inputClass} value={authType} onChange={(e) => changeType(e.target.value)}>
              {offered.map((t) => <option key={t.type} value={t.type}>{t.title} ({t.type})</option>)}
            </select>
            <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">
              {type?.description}
              {' '}Other types (delegated user OAuth, token exchange, Google ID token) go through &ldquo;Paste a full specification&rdquo;.
            </p>
          </div>
          {offered.length === 0 && <p role="alert" className="text-status-denied-fg text-sm mb-3">The auth type catalog offers none of the types this form supports. Use &ldquo;Paste a full specification&rdquo;.</p>}
          {type?.fields.map((field) => (
            <FieldInput key={`${type.type}.${field.name}`} field={field} value={values[field.name] ?? ''} error={errors[field.name]}
              onChange={(v) => setValues((prev) => ({ ...prev, [field.name]: v }))}
              pattern={catalog.secret_references.pattern} namespace={catalog.secret_references.namespace} />
          ))}
          <div className="mb-3">
            <label htmlFor={ids.tools} className="block text-sm font-medium mb-1">Allowed tools <span aria-hidden="true">*</span></label>
            <textarea id={ids.tools} className={inputClass} rows={3} value={tools} onChange={(e) => setTools(e.target.value)} spellCheck={false} aria-invalid={!!errors.tools} />
            <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">The tools callers may use (execution.allowed_tools), one per line or comma separated.</p>
            {errors.tools && <p role="alert" className="text-xs text-status-denied-fg mt-1">tools: {errors.tools}</p>}
          </div>
          <div className="flex gap-2">
            <button type="submit" className={primary} disabled={saving || !type}>{saving ? 'Creating…' : 'Create connection'}</button>
            <button type="button" className={btn} onClick={onCancel} disabled={saving}>Cancel</button>
          </div>
        </form>
      ) : (
        <form onSubmit={submitPaste} aria-label="Paste tool connection specification" noValidate>
          {referenceHelp}
          <label htmlFor={ids.paste} className="block text-sm font-medium mb-1">Full specification (JSON)</label>
          <textarea id={ids.paste} className={`${inputClass} font-mono`} rows={12} value={pasted} onChange={(e) => setPasted(e.target.value)} spellCheck={false} />
          <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">
            Credentials in the auth block must be references (<code>secret_ref</code>, <code>client_secret_ref</code>). A specification that carries a secret value is refused here, before anything is sent.
          </p>
          {specErrors.length > 0 && (
            <ul role="alert" className="text-xs text-status-denied-fg mt-2 list-disc pl-5">
              {specErrors.map((e) => <li key={e} className="whitespace-pre-wrap">{e}</li>)}
            </ul>
          )}
          <div className="flex gap-2 mt-3">
            <button type="submit" className={primary} disabled={saving}>{saving ? 'Creating…' : 'Create connection'}</button>
            <button type="button" className={btn} onClick={onCancel} disabled={saving}>Cancel</button>
          </div>
        </form>
      )}
    </section>
  )
}
