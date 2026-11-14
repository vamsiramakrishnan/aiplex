import { useMutation } from '@tanstack/react-query'
import { ToolListFailure, errorWithCode, errorStatus, listConnectionTools } from '../api/toolConnections'
import { UNKNOWN } from '../api/usage'

const btn = 'px-3 py-1.5 text-sm rounded border border-gray-300 dark:border-gray-700 hover:bg-gray-100 dark:hover:bg-gray-800 disabled:opacity-50'

function explain(e: unknown): string {
  if (e instanceof ToolListFailure) return `The tool server refused tools/list through the gateway: ${errorWithCode(e)}`
  if (errorStatus(e) === 502) return `tools/list through the gateway failed: ${errorWithCode(e)}. The gateway could not get a valid answer from the server at the connection's URL.`
  return `tools/list through the gateway failed: ${errorWithCode(e)}`
}

/**
 * Lists the tools callers can see: a real tools/list through the gateway, with
 * the connection's credential and tools policy applied (what
 * `aiplex mcp connections test` does). A failure is what a client would hit.
 * The parent keys this panel by connection id, so nothing carries over.
 */
export default function ToolConnectionTools({ id, onClose }: { id: string; onClose: () => void }) {
  const list = useMutation({ mutationFn: () => listConnectionTools(id) })
  const tools = list.data?.tools
  return (
    <section aria-label={`Tools of ${id}`} className="bg-white rounded-lg shadow p-4 mt-4 dark:bg-gray-900">
      <h3 className="font-semibold mb-1">Tools through the gateway: {id}</h3>
      <p className="text-xs text-gray-600 dark:text-gray-400 mb-3">
        Sends a real tools/list to the server with the connection&rsquo;s credential and tools policy applied, and shows the tools callers can see. It does not call any tool.
      </p>
      <div className="flex gap-2 mb-3">
        <button type="button" className={btn} onClick={() => list.mutate()} disabled={list.isPending}>{list.isPending ? 'Listing…' : list.data ? 'List again' : 'List tools'}</button>
        <button type="button" className={btn} onClick={onClose}>Close</button>
      </div>
      {list.isError && <p role="alert" className="text-status-denied-fg text-sm whitespace-pre-wrap">{explain(list.error)}</p>}
      {tools && tools.length === 0 && <p role="status" className="text-sm">The server listed no tools. The connection&rsquo;s tools policy may be hiding them, or the server may have none.</p>}
      {tools && tools.length > 0 && (
        <table className="w-full text-sm">
          <caption className="sr-only">Tools callers can see on {id}</caption>
          <thead><tr><th scope="col" className="text-left py-2 pr-3">Tool</th><th scope="col" className="text-left pr-3">Description</th><th scope="col" className="text-left">Read-only</th></tr></thead>
          <tbody>
            {[...tools].sort((a, b) => a.name.localeCompare(b.name)).map((t) => (
              <tr key={t.name} className="border-t align-top">
                <td className="py-2 pr-3 font-mono text-xs">{t.name}</td>
                <td className="pr-3">{t.description || t.title || 'not reported'}</td>
                <td>{typeof t.annotations?.readOnlyHint === 'boolean' ? String(t.annotations.readOnlyHint) : UNKNOWN}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {list.data?.truncated && <p role="status" className="text-sm mt-2">The server has more pages than the Console reads. This list is not complete.</p>}
    </section>
  )
}
