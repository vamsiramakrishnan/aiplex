import { useId, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { connectErrorMessage, getConnect, listClientProfiles } from '../api/overview'
import CopyButton from './CopyButton'
import { StepCode } from './SetupSteps'
import { useFocusTrap } from '../hooks/useFocusTrap'

/** Profiles that speak only the Claude apps gateway protocol sign in through a gateway server, not an access point. */
const connectable = (p: { protocols: string[] }) => p.protocols.some(x => x !== 'claude-apps')

const input = 'border rounded px-2 py-1 text-sm bg-transparent border-gray-300 dark:border-gray-700 max-w-full'

function UrlRow({ label, url }: { label: string; url: string }) {
  return (
    <li className="flex flex-wrap items-center gap-2 text-sm">
      <span className="text-xs text-gray-600 dark:text-gray-400 min-w-40">{label}</span>
      <code className="font-mono text-xs break-all">{url}</code>
      <CopyButton text={url} label={`${label} URL`} />
    </li>
  )
}

/** Step 2: server-built connection details for one client profile. */
export function ConnectDetails({ accessPointId, client }: { accessPointId: string; client: string }) {
  const q = useQuery({ queryKey: ['ap-connect', accessPointId, client], queryFn: () => getConnect(accessPointId, client), retry: false })
  if (q.isLoading) return <p role="status" className="text-sm text-gray-600 dark:text-gray-400">Building connection details…</p>
  if (q.isError) return <p role="alert" className="text-sm text-status-denied-fg">{connectErrorMessage(q.error)}</p>
  const c = q.data!
  const urls = Object.entries(c.base_urls ?? {})
  const endpoints = c.endpoints ?? []
  return (
    <div aria-label="Connection details" className="space-y-4">
      <section aria-label="URLs" className="space-y-1">
        <h4 className="text-sm font-semibold">URLs</h4>
        <ul className="space-y-1">
          {urls.map(([family, url]) => <UrlRow key={family} label={`${family} base URL`} url={url} />)}
          {endpoints.map(e => <UrlRow key={`${e.protocol}-${e.transport}-${e.url}`} label={`${e.label ?? e.protocol} (${e.transport})${e.query?.length ? `, requires ?${e.query.join('&')}=` : ''}`} url={e.url} />)}
          {c.mcp_url && <UrlRow label="MCP URL" url={c.mcp_url} />}
          {c.mcp_catalog_url && <UrlRow label="MCP catalog URL" url={c.mcp_catalog_url} />}
          {c.skills_url && <UrlRow label="Skills URL" url={c.skills_url} />}
        </ul>
        {urls.length === 0 && endpoints.length === 0 && !c.mcp_url && !c.skills_url && <p className="text-sm text-gray-600 dark:text-gray-400">This access point has nothing a client can connect to yet.</p>}
      </section>
      {!!c.models?.length && (
        <section aria-label="Models" className="text-sm">
          <h4 className="text-sm font-semibold">Models</h4>
          <ul className="list-disc ml-5 text-xs">{c.models.map(m => <li key={m.name}><code>{m.name}</code>: {(m.protocols ?? []).join(', ') || 'no client protocol reaches it'}</li>)}</ul>
        </section>
      )}
      <section aria-label="Authentication" className="space-y-1 text-sm">
        <h4 className="text-sm font-semibold">Authentication</h4>
        <p role="note" aria-label="API key headers" className="text-xs text-gray-600 dark:text-gray-400">
          SDKs may pass the AIPlex token as their normal API key: on Anthropic paths in <code>x-api-key</code>, on Google paths in <code>x-goog-api-key</code>, and on Azure OpenAI model-gateway paths in <code>api-key</code>. OpenAI SDKs already send it as a bearer token. A token in a URL query parameter (such as <code>?key=</code>) is rejected.
        </p>
        <p className="text-xs text-gray-600 dark:text-gray-400">
          {c.auth.scheme} in the <code>{c.auth.header}</code> header, read from <code>${c.auth.token_env}</code>. For caller kinds: {(c.auth.caller_kinds ?? []).join(', ') || 'any'}.
        </p>
        {c.auth.authorization_servers?.length ? <p className="text-xs">Authorization servers: {c.auth.authorization_servers.join(', ')}</p> : null}
        {c.auth.resource_metadata_url && <UrlRow label="Resource metadata" url={c.auth.resource_metadata_url} />}
        {!!c.auth.steps?.length && <ol className="list-decimal ml-5 text-xs space-y-1">{c.auth.steps.map(s => <li key={s}>{s}</li>)}</ol>}
      </section>
      {!!c.snippets?.length && (
        <section aria-label="Snippets" className="space-y-2">
          <h4 className="text-sm font-semibold">Configuration</h4>
          {c.snippets.map((s, i) => (
            <div key={`${i}-${s.title}`}><div className="text-sm font-medium">{s.title}</div><StepCode step={{ title: s.title, language: s.language, content: s.content }} /></div>
          ))}
        </section>
      )}
      {!!c.notes?.length && (
        <section aria-label="Notes"><h4 className="text-sm font-semibold">Notes</h4><ul className="list-disc ml-5 text-xs text-gray-600 dark:text-gray-400">{c.notes.map(n => <li key={n}>{n}</li>)}</ul></section>
      )}
    </div>
  )
}

/** Connect wizard: pick a client profile (or generic), then show how to connect it. */
export default function ConnectWizard({ accessPointId, onClose }: { accessPointId: string; onClose: () => void }) {
  const uid = useId()
  const profiles = useQuery({ queryKey: ['client-profiles'], queryFn: listClientProfiles })
  const [choice, setChoice] = useState('generic')
  const [shown, setShown] = useState<string | null>(null)
  const ref = useFocusTrap<HTMLElement>(true, onClose)
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center overflow-y-auto bg-black/50 p-4" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
    <section ref={ref} role="dialog" aria-modal="true" aria-label={`Connect a client to ${accessPointId}`} tabIndex={-1} className="w-full max-w-3xl rounded-lg border border-gray-300 dark:border-gray-700 p-4 space-y-3 bg-white dark:bg-gray-900 shadow-xl">
      <div className="flex justify-between gap-3">
        <h3 className="text-lg font-medium">Connect a client to {accessPointId}</h3>
        <button type="button" className="text-sm underline" onClick={onClose}>Close</button>
      </div>
      <div className="flex flex-wrap items-end gap-2">
        <div>
          <label htmlFor={`${uid}-client`} className="block text-xs mb-1">Client</label>
          <select id={`${uid}-client`} value={choice} onChange={e => { setChoice(e.target.value); setShown(null) }} className={input}>
            <option value="generic">Generic (any OpenAI, Anthropic, Google or MCP client)</option>
            {(profiles.data ?? []).filter(connectable).map(p => <option key={p.id} value={p.id}>{p.name} ({p.protocols.join(', ')})</option>)}
          </select>
        </div>
        <button type="button" onClick={() => setShown(choice)} className="px-3 py-1.5 text-sm bg-brand-600 text-brand-contrast rounded">Show connection details</button>
      </div>
      {profiles.isError && <p role="alert" className="text-xs text-status-warning-fg">Could not load client profiles ({(profiles.error as Error).message}); only the generic option is available.</p>}
      {shown && <ConnectDetails key={shown} accessPointId={accessPointId} client={shown} />}
    </section>
    </div>
  )
}
