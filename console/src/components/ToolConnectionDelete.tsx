import { useState } from 'react'
import { deleteToolConnection, errorWithCode, errorStatus, knownRevision } from '../api/toolConnections'
import { isRevisionConflict, type ToolConnectionRecord } from '../api/toolAuth'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50'

/** Confirmation before deleting a connection, with the revision it will be checked against. A conflict names what to do; nothing is retried. */
export default function ToolConnectionDelete({ record, onDeleted, onReload, onCancel }: {
  record: ToolConnectionRecord
  onDeleted: () => void
  onReload: () => Promise<void>
  onCancel: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [conflict, setConflict] = useState<'changed' | 'gone' | null>(null)
  const known = knownRevision(record.revision)

  const confirm = async () => {
    if (busy || !known) return
    setBusy(true)
    setFailure(null)
    try {
      await deleteToolConnection(record.id, record.revision)
      onDeleted()
    } catch (e) {
      if (errorStatus(e) === 404) setConflict('gone')
      else if (isRevisionConflict(e) || errorStatus(e) === 412) setConflict('changed')
      else setFailure(errorWithCode(e))
    } finally { setBusy(false) }
  }
  const reload = async () => {
    setBusy(true)
    try { await onReload(); setConflict(null) } catch (e) { setFailure(errorWithCode(e)) } finally { setBusy(false) }
  }

  return (
    <section role="group" aria-label={`Delete ${record.id}`} className="bg-white rounded-lg shadow p-4 mt-4 dark:bg-gray-900">
      <h3 className="font-semibold mb-1">Delete tool connection {record.id}</h3>
      <p className="text-xs text-gray-600 dark:text-gray-400 mb-3">Revision {known ? record.revision : 'unknown'}</p>
      {!conflict && <p className="text-sm mb-3">Delete tool connection <strong>{record.id}</strong>? Access points that use it stop offering its tools. This cannot be undone.</p>}
      {!known && <p role="alert" className="text-status-denied-fg text-sm mb-3">The revision is unknown, so the delete cannot be checked against it. Reload first.</p>}
      {conflict && (
        <div role="alert" className="mb-3 border border-status-warning-fg bg-status-warning-bg text-status-warning-fg rounded p-3 text-sm">
          <p className="font-medium">{conflict === 'gone' ? 'This connection no longer exists. Nothing was deleted.' : 'This connection was changed by someone else. Nothing was deleted.'}</p>
          {conflict === 'changed' && <p className="mb-2">Reload the latest version, review it, then delete again if you still want to.</p>}
          <button type="button" className={btn} onClick={reload} disabled={busy}>Reload latest version</button>
        </div>
      )}
      {failure && <p role="alert" className="text-status-denied-fg text-sm mb-3 whitespace-pre-wrap">Could not delete: {failure}</p>}
      <div className="flex gap-2">
        {!conflict && <button type="button" className="px-3 py-1.5 text-sm bg-status-denied-fg text-status-denied-bg rounded disabled:opacity-50" disabled={busy || !known} onClick={confirm}>{busy ? 'Deleting…' : 'Confirm delete'}</button>}
        <button type="button" className={btn} onClick={onCancel} disabled={busy}>Cancel</button>
      </div>
    </section>
  )
}
