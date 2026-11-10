import { useId, useState } from 'react'
import {
  CONNECTION_AUTH,
  applySelection,
  hasManagedExecution,
  isRevisionConflict,
  putToolConnection,
  validateAuthValues,
  valuesFromAuth,
  type AuthValues,
  type ToolAuthCatalog,
  type ToolAuthField,
  type ToolConnectionRecord,
} from '../api/toolAuth'

interface Props {
  record: ToolConnectionRecord
  catalog: ToolAuthCatalog
  onSaved: (record: ToolConnectionRecord) => void
  onReload: () => Promise<void>
  onCancel: () => void
}

const inputClass = 'w-full border rounded px-3 py-2 text-sm'

function FieldInput({ field, value, error, onChange, pattern, namespace }: {
  field: ToolAuthField
  value: string
  error?: string
  onChange: (value: string) => void
  pattern?: string
  namespace?: string
}) {
  const id = useId()
  const hintId = `${id}-hint`
  const errorId = `${id}-error`
  const isSecret = !!field.secret_reference
  const isList = field.type.endsWith('[]')
  const label = isSecret ? `${field.name} (secret reference)` : field.name
  return (
    <div className="mb-3">
      <label htmlFor={id} className="block text-sm font-medium mb-1">
        {label}{field.required && <span aria-hidden="true" className="text-status-denied-fg"> *</span>}
        {field.required && <span className="sr-only"> (required)</span>}
      </label>
      {field.enum ? (
        <select id={id} value={value} onChange={(e) => onChange(e.target.value)} className={inputClass}
          aria-describedby={`${hintId}${error ? ` ${errorId}` : ''}`} aria-invalid={!!error} aria-required={field.required}>
          <option value="">{field.required ? 'Select…' : 'Not set'}</option>
          {field.enum.map((option) => <option key={option} value={option}>{option}</option>)}
        </select>
      ) : (
        <input id={id} type="text" value={value} onChange={(e) => onChange(e.target.value)} className={inputClass}
          autoComplete="off" spellCheck={false} aria-required={field.required} aria-invalid={!!error}
          aria-describedby={`${hintId}${error ? ` ${errorId}` : ''}`}
          placeholder={isSecret ? 'Name of a stored secret, e.g. search-key' : isList ? 'Comma separated' : undefined} />
      )}
      <p id={hintId} className="text-xs text-gray-600 dark:text-gray-400 mt-1">
        {field.description}
        {isSecret && <> Enter a reference name, not the secret value. The value is resolved by the gateway when a tool is called and is never shown here.{namespace ? ` Secrets are looked up in ${namespace}.` : ''}{pattern ? ` Allowed: ${pattern}` : ''}</>}
      </p>
      {error && <p id={errorId} role="alert" className="text-xs text-status-denied-fg mt-1">{field.name}: {error}</p>}
    </div>
  )
}

export default function ToolAuthEditor({ record, catalog, onSaved, onReload, onCancel }: Props) {
  const spec = record.spec
  const managed = hasManagedExecution(spec)
  const lockedToAuth = spec.execution?.credential_mode === CONNECTION_AUTH
  const initialType = spec.auth?.type ?? ''
  const [authType, setAuthType] = useState(initialType)
  const [values, setValues] = useState<AuthValues>(() => valuesFromAuth(catalog.types.find((t) => t.type === initialType), spec.auth))
  const [allow, setAllow] = useState((spec.tools?.allow ?? []).join('\n'))
  const [deny, setDeny] = useState((spec.tools?.deny ?? []).join('\n'))
  const [readOnly, setReadOnly] = useState(!!spec.tools?.read_only)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [conflict, setConflict] = useState(false)
  const [reloading, setReloading] = useState(false)
  const typeId = useId()
  const allowId = useId()
  const denyId = useId()
  const readOnlyId = useId()

  const selected = catalog.types.find((t) => t.type === authType)
  const changeType = (next: string) => {
    setAuthType(next)
    setValues(valuesFromAuth(catalog.types.find((t) => t.type === next), spec.auth))
    setErrors({})
  }

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    if (saving) return
    setFailure(null)
    if (selected) {
      const found = validateAuthValues(catalog, selected, values)
      setErrors(found)
      if (Object.keys(found).length) return
    }
    setSaving(true)
    try {
      const next = applySelection(spec, catalog, { authType, values, allow, deny, readOnly })
      onSaved(await putToolConnection(record.id, next, record.revision))
    } catch (e) {
      if (isRevisionConflict(e)) setConflict(true)
      else setFailure(e instanceof Error ? e.message : 'Could not save the connection')
    } finally {
      setSaving(false)
    }
  }

  const reload = async () => {
    setReloading(true)
    try { await onReload() } catch (e) {
      setFailure(e instanceof Error ? e.message : 'Could not reload the connection')
    } finally { setReloading(false) }
  }

  return (
    <form onSubmit={save} aria-label={`Edit auth for ${record.id}`} className="bg-white rounded-lg shadow p-4 mt-4 dark:bg-gray-900">
      <h3 className="font-semibold mb-1">Auth and tool policy: {record.id}</h3>
      <p className="text-xs text-gray-600 dark:text-gray-400 mb-4">Revision {record.revision}</p>

      {conflict && (
        <div role="alert" className="mb-4 border border-status-warning-fg bg-status-warning-bg text-status-warning-fg rounded p-3 text-sm">
          <p className="font-medium">This connection was changed by someone else.</p>
          <p className="mb-2">Your edits were not saved. Reload the latest version, then review it and make your changes again.</p>
          <button type="button" onClick={reload} disabled={reloading} className="px-3 py-1.5 bg-brand-600 text-brand-contrast text-sm rounded disabled:opacity-50">
            {reloading ? 'Reloading…' : 'Reload latest'}
          </button>
        </div>
      )}
      {failure && <p role="alert" className="text-status-denied-fg text-sm mb-3">Could not save: {failure}</p>}

      <div className="mb-3">
        <label htmlFor={typeId} className="block text-sm font-medium mb-1">Auth type</label>
        <select id={typeId} value={authType} onChange={(e) => changeType(e.target.value)} className={inputClass} disabled={conflict}>
          <option value="" disabled={lockedToAuth}>Default for this connection ({catalog.default_type})</option>
          {catalog.types.map((t) => (
            <option key={t.type} value={t.type} disabled={!managed && t.execution_credential_mode === CONNECTION_AUTH}>{t.title} ({t.type})</option>
          ))}
        </select>
        <p className="text-xs text-gray-600 dark:text-gray-400 mt-1">
          {selected ? selected.description : catalog.default_behaviour}
          {!managed && ' This connection has no managed execution, so only delegated user OAuth can be selected.'}
        </p>
      </div>

      {selected && selected.fields.length === 0 && <p className="text-sm text-gray-600 dark:text-gray-400 mb-3">This auth type has no settings.</p>}
      {selected?.fields.map((field) => (
        <FieldInput key={`${selected.type}.${field.name}`} field={field} value={values[field.name] ?? ''} error={errors[field.name]}
          onChange={(v) => setValues((prev) => ({ ...prev, [field.name]: v }))}
          pattern={catalog.secret_references.pattern} namespace={catalog.secret_references.namespace} />
      ))}

      <fieldset className="border-t pt-3 mt-4" disabled={!managed || conflict}>
        <legend className="text-sm font-medium">Tool policy</legend>
        {!managed && <p className="text-xs text-gray-600 dark:text-gray-400 mb-2">A tool policy needs managed execution, which this connection does not have.</p>}
        <div className="mb-3">
          <label htmlFor={allowId} className="block text-sm font-medium mb-1">Allowed tools</label>
          <textarea id={allowId} value={allow} onChange={(e) => setAllow(e.target.value)} rows={3} className={inputClass} aria-describedby={`${allowId}-hint`} spellCheck={false} />
          <p id={`${allowId}-hint`} className="text-xs text-gray-600 dark:text-gray-400 mt-1">One per line. Exact tool names or prefixes ending in *, such as files.*. Leave empty to allow every tool the caller is granted.</p>
        </div>
        <div className="mb-3">
          <label htmlFor={denyId} className="block text-sm font-medium mb-1">Denied tools</label>
          <textarea id={denyId} value={deny} onChange={(e) => setDeny(e.target.value)} rows={3} className={inputClass} aria-describedby={`${denyId}-hint`} spellCheck={false} />
          <p id={`${denyId}-hint`} className="text-xs text-gray-600 dark:text-gray-400 mt-1">One per line, same format. Deny always wins over allow.</p>
        </div>
        <div className="flex items-start gap-2">
          <input id={readOnlyId} type="checkbox" checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} className="mt-1" aria-describedby={`${readOnlyId}-hint`} />
          <div>
            <label htmlFor={readOnlyId} className="text-sm font-medium">Read-only</label>
            <p id={`${readOnlyId}-hint`} className="text-xs text-gray-600 dark:text-gray-400">Only tools the server annotates as read-only can be called. Tools with an unknown annotation are denied until tools are listed through the gateway.</p>
          </div>
        </div>
      </fieldset>

      <div className="flex gap-2 mt-4">
        <button type="submit" disabled={saving || conflict} className="px-4 py-2 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700 disabled:opacity-50">
          {saving ? 'Saving…' : 'Save'}
        </button>
        <button type="button" onClick={onCancel} className="px-4 py-2 bg-gray-100 text-sm rounded dark:bg-gray-800">Cancel</button>
      </div>
    </form>
  )
}
