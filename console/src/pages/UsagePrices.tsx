import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import * as api from '../api/usage'
import { ErrorPanel } from '../components/UsageError'
import { useAccess } from '../hooks/useAccess'

const field = 'w-full rounded border border-gray-300 bg-transparent px-3 py-2 text-sm dark:border-gray-700'
const button = 'rounded bg-gray-900 px-4 py-2 text-sm text-white disabled:opacity-40 dark:bg-gray-200 dark:text-gray-900'
const muted = 'text-xs text-gray-500 dark:text-gray-400'

export interface PriceDraft {
  id: string
  model_id: string
  provider: string
  currency: string
  scale: string
  meters: Record<api.PriceMeter, { numerator: string; denominator: string }>
  effective_from: string
  effective_until: string
  /** Selectors: '' matches any exchange. */
  batch: '' | 'true' | 'false'
  image_size: string
  image_quality: string
}

const blankMeters = () => Object.fromEntries(api.ALL_PRICE_METERS.map((m) => [m, { numerator: '', denominator: '' }])) as PriceDraft['meters']
export const blankDraft = (): PriceDraft => ({ id: '', model_id: '', provider: '', currency: 'USD', scale: '6', meters: blankMeters(), effective_from: '', effective_until: '', batch: '', image_size: '', image_quality: '' })

export function draftFromPrice(price: api.ModelPrice): PriceDraft {
  const meters = blankMeters()
  for (const name of api.ALL_PRICE_METERS) {
    const rate = price.meters[name]
    if (rate) meters[name] = { numerator: String(rate.numerator), denominator: String(rate.denominator) }
  }
  return { id: price.id, model_id: price.model_id, provider: price.provider ?? '', currency: price.currency, scale: String(price.scale), meters, effective_from: price.effective_from ?? '', effective_until: price.effective_until ?? '', batch: price.batch === undefined ? '' : price.batch ? 'true' : 'false', image_size: price.image_size ?? '', image_quality: price.image_quality ?? '' }
}

const safeInt = (text: string, min: number) => /^\d+$/.test(text) && Number.isSafeInteger(Number(text)) && Number(text) >= min

/** Validates a draft with the backend's rules; returns the spec or every problem found. */
export function priceFromDraft(draft: PriceDraft): { price?: api.ModelPrice; errors: string[] } {
  const errors: string[] = []
  const id = draft.id.trim()
  if (!id || id.length > 512) errors.push('Price ID is required (up to 512 characters).')
  if (!draft.model_id.trim()) errors.push('Model ID is required.')
  if (!/^[A-Z]{3}$/.test(draft.currency)) errors.push('Currency must be three uppercase letters, e.g. USD.')
  if (!/^\d+$/.test(draft.scale) || Number(draft.scale) > 18) errors.push('Scale must be a whole number from 0 to 18.')
  const meters: api.ModelPrice['meters'] = {}
  for (const name of api.ALL_PRICE_METERS) {
    const { numerator, denominator } = draft.meters[name]
    if (!numerator.trim() && !denominator.trim()) continue
    if (!safeInt(numerator.trim(), 0) || !safeInt(denominator.trim(), 1)) errors.push(`${name}: numerator must be a whole number ≥ 0 and denominator a whole number ≥ 1.`)
    else meters[name] = { numerator: Number(numerator), denominator: Number(denominator) }
  }
  if (Object.keys(meters).length === 0 && !errors.some((e) => e.includes(': numerator'))) errors.push('Price at least one meter.')
  const from = draft.effective_from.trim(), until = draft.effective_until.trim()
  for (const [label, value] of [['Effective from', from], ['Effective until', until]] as const) if (value && Number.isNaN(Date.parse(value))) errors.push(`${label} must be an RFC 3339 timestamp, e.g. 2026-11-01T00:00:00Z.`)
  if (from && until && Date.parse(until) <= Date.parse(from)) errors.push('Effective until must be after effective from.')
  if (errors.length) return { errors }
  return { errors, price: { id, model_id: draft.model_id.trim(), ...(draft.provider.trim() ? { provider: draft.provider.trim() } : {}), currency: draft.currency, scale: Number(draft.scale), meters, ...(from ? { effective_from: from } : {}), ...(until ? { effective_until: until } : {}), ...(draft.batch ? { batch: draft.batch === 'true' } : {}), ...(draft.image_size.trim() ? { image_size: draft.image_size.trim() } : {}), ...(draft.image_quality.trim() ? { image_quality: draft.image_quality.trim() } : {}) } }
}

const describeMeters = (p: api.ModelPrice) => api.ALL_PRICE_METERS.filter((m) => p.meters[m]).map((m) => `${m} ${p.meters[m]!.numerator}/${p.meters[m]!.denominator}`).join(', ')

const describeSelectors = (p: api.ModelPrice) => [p.batch === undefined ? '' : `batch ${p.batch ? 'only' : 'excluded'}`, p.image_size ? `size ${p.image_size}` : '', p.image_quality ? `quality ${p.image_quality}` : ''].filter(Boolean).join(', ') || 'any'

type Editing = { mode: 'create' } | { mode: 'edit'; revision: number }

export default function UsagePrices() {
  const access = useAccess()
  const formRef = useRef<HTMLFormElement>(null)
  const client = useQueryClient()
  const query = useQuery({ queryKey: ['usage-prices'], queryFn: api.listPrices })
  const [editing, setEditing] = useState<Editing | null>(null)
  const [draft, setDraft] = useState<PriceDraft>(blankDraft)
  const [problems, setProblems] = useState<string[]>([])
  const [message, setMessage] = useState('')
  const [conflict, setConflict] = useState(false)
  const [busy, setBusy] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState<api.PriceResource | null>(null)

  // The form sits below the table; bring it into view and put the cursor in its first field.
  const editKey = editing ? `${editing.mode}:${editing.mode === 'edit' ? editing.revision : ''}` : ''
  useEffect(() => {
    if (!editKey) return
    formRef.current?.scrollIntoView?.({ block: 'center' })
    formRef.current?.querySelector<HTMLInputElement>('input:not([readonly])')?.focus()
  }, [editKey])

  const open = (next: Editing, value: PriceDraft) => { setEditing(next); setDraft(value); setProblems([]); setMessage(''); setConflict(false); setConfirmDelete(null) }
  const close = () => { setEditing(null); setProblems([]); setMessage(''); setConflict(false) }

  // A failed write never changes local state: the server decides whether the revision was current.
  function failure(e: unknown, verb: string) {
    const status = api.errorStatus(e)
    if (status === 409) { setConflict(true); setMessage('') }
    else setMessage(status === 403 ? `Only administrators can ${verb} prices.` : e instanceof Error ? e.message : String(e))
  }

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!editing || busy) return
    const { price, errors } = priceFromDraft(draft)
    setProblems(errors)
    if (!price) return
    setBusy(true); setMessage(''); setConflict(false)
    try {
      await api.putPrice(price, editing.mode === 'edit' ? editing.revision : 0)
      await client.invalidateQueries({ queryKey: ['usage-prices'] })
      close()
    } catch (err) { failure(err, 'change') } finally { setBusy(false) }
  }

  async function loadLatest() {
    if (!editing) return
    setBusy(true)
    try {
      const latest = await api.getPrice(draft.id.trim())
      await client.invalidateQueries({ queryKey: ['usage-prices'] })
      open({ mode: 'edit', revision: latest.revision }, draftFromPrice(latest.spec))
      setMessage(`Loaded revision ${latest.revision}. Re-apply your changes and save.`)
    } catch (err) {
      if (api.errorStatus(err) === 404) { await client.invalidateQueries({ queryKey: ['usage-prices'] }); open({ mode: 'create' }, draft); setMessage('This price was deleted by someone else. Saving will create it again.') }
      else failure(err, 'change')
    } finally { setBusy(false) }
  }

  async function remove(price: api.PriceResource) {
    if (busy) return
    setBusy(true); setMessage(''); setConflict(false)
    try {
      await api.deletePrice(price.id, price.revision)
      setConfirmDelete(null)
      await client.invalidateQueries({ queryKey: ['usage-prices'] })
    } catch (err) {
      failure(err, 'delete')
      if (api.errorStatus(err) === 409) { setConfirmDelete(null); await client.invalidateQueries({ queryKey: ['usage-prices'] }) }
    } finally { setBusy(false) }
  }

  const setMeter = (name: api.PriceMeter, part: 'numerator' | 'denominator', value: string) => setDraft((d) => ({ ...d, meters: { ...d.meters, [name]: { ...d.meters[name], [part]: value } } }))
  const text = (name: 'id' | 'model_id' | 'provider' | 'currency' | 'scale' | 'effective_from' | 'effective_until' | 'image_size' | 'image_quality') => ({ value: draft[name], onChange: (e: { target: { value: string } }) => setDraft((d) => ({ ...d, [name]: e.target.value })) })

  const meterInputs = (m: api.PriceMeter) => <div key={m} className="grid grid-cols-[1fr_1fr] gap-2"><label className="text-sm">{m} numerator<input className={field} inputMode="numeric" value={draft.meters[m].numerator} onChange={(e) => setMeter(m, 'numerator', e.target.value)} /></label><label className="text-sm">{m} denominator<input className={field} inputMode="numeric" value={draft.meters[m].denominator} onChange={(e) => setMeter(m, 'denominator', e.target.value)} /></label></div>

  return <section className="space-y-3" aria-label="Model prices">
    <div className="flex items-center justify-between gap-3"><h2 className="text-lg font-medium">Model prices</h2><button type="button" className={button} disabled={!access.isAdmin} title={access.reason('admin')} onClick={() => open({ mode: 'create' }, blankDraft())}>Add price</button></div>
    <p className="text-sm text-gray-500">Prices turn reported token usage into an estimated cost. Without a matching price, a request’s cost stays unknown unless QuotaPlex settles it. Only administrators can change prices.</p>
    {message && <p role="alert" className="rounded border border-red-300 p-3 text-sm text-red-700 dark:border-red-800 dark:text-red-400">{message}</p>}
    {conflict && <div role="alert" className="rounded border border-amber-400 p-3 text-sm text-amber-800 dark:text-amber-300"><p>This price was changed by someone else (or already exists), so nothing was saved.</p>{editing ? <button type="button" className="mt-2 underline" disabled={busy} onClick={() => void loadLatest()}>Load latest revision</button> : <p className="mt-1">The list below has been refreshed.</p>}</div>}

    {query.isPending ? <p role="status">Loading prices…</p>
      : query.isError ? <ErrorPanel error={query.error} what="model prices" onRetry={() => void query.refetch()} />
      : query.data.length === 0 ? <p role="status">No prices configured. Cost is unknown until a price is configured or quota settles it.</p>
      : <div className="overflow-x-auto"><table className="w-full text-left text-sm">
        <caption className="sr-only">Configured model prices</caption>
        <thead><tr className="border-b border-gray-200 dark:border-gray-800">{['ID', 'Model', 'Provider', 'Currency / scale', 'Meters (minor units per quantity)', 'Selectors', 'Effective', 'Revision', 'Actions'].map((h) => <th key={h} scope="col" className="py-2 pr-4">{h}</th>)}</tr></thead>
        <tbody>{query.data.map((p) => <tr key={p.id} className="border-t border-gray-200 align-top dark:border-gray-800">
          <th scope="row" className="py-2 pr-4 font-normal">{p.id}</th><td className="pr-4">{p.spec.model_id}</td><td className="pr-4">{p.spec.provider || '—'}</td>
          <td className="pr-4">{p.spec.currency} / {p.spec.scale}</td><td className="pr-4 font-mono text-xs">{describeMeters(p.spec)}</td>
          <td className="pr-4 text-xs">{describeSelectors(p.spec)}</td>
          <td className="pr-4 text-xs">{p.spec.effective_from || p.spec.effective_until ? `${p.spec.effective_from ?? '…'} → ${p.spec.effective_until ?? '…'}` : 'always'}</td>
          <td className="pr-4">{p.revision}</td>
          <td className="whitespace-nowrap">{confirmDelete?.id === p.id
            ? <span>Delete {p.id}? <button type="button" className="underline" disabled={busy} onClick={() => void remove(p)}>Confirm delete</button> <button type="button" className="underline" onClick={() => setConfirmDelete(null)}>Cancel</button></span>
            : <><button type="button" className="mr-3 underline disabled:opacity-50" disabled={!access.isAdmin} title={access.reason('admin')} aria-label={`Edit price ${p.id}`} onClick={() => open({ mode: 'edit', revision: p.revision }, draftFromPrice(p.spec))}>Edit</button><button type="button" className="underline disabled:opacity-50" disabled={!access.isAdmin} title={access.reason('admin')} aria-label={`Delete price ${p.id}`} onClick={() => { setConfirmDelete(p); setMessage(''); setConflict(false) }}>Delete</button></>}</td>
        </tr>)}</tbody>
      </table></div>}

    {editing && <form ref={formRef} aria-label={editing.mode === 'create' ? 'Add price' : `Edit price ${draft.id}`} onSubmit={(e) => void save(e)} className="space-y-4 rounded border border-gray-200 p-4 dark:border-gray-800">
      <h3 className="font-medium">{editing.mode === 'create' ? 'Add price' : `Edit price (revision ${editing.revision})`}</h3>
      {problems.length > 0 && <ul role="alert" className="list-disc pl-5 text-sm text-red-700 dark:text-red-400">{problems.map((p) => <li key={p}>{p}</li>)}</ul>}
      <div className="grid gap-3 sm:grid-cols-3">
        <label className="text-sm">Price ID<input className={field} {...text('id')} readOnly={editing.mode === 'edit'} /></label>
        <label className="text-sm">Model ID<input className={field} {...text('model_id')} /></label>
        <label className="text-sm">Provider (optional)<input className={field} {...text('provider')} /></label>
        <label className="text-sm">Currency<input className={field} {...text('currency')} maxLength={3} /></label>
        <label className="text-sm">Scale (decimal places of the minor unit)<input className={field} inputMode="numeric" {...text('scale')} /></label>
      </div>
      <fieldset><legend className="text-sm font-medium">Token and request meters</legend><p className={muted}>Rate = numerator minor units (at the scale above) per denominator raw units. A numerator of 0 declares a meter free; leave both blank if it is not priced.</p>
        <div className="mt-2 grid gap-2 sm:grid-cols-2">{api.PRICE_METERS.map(meterInputs)}</div>
      </fieldset>
      <fieldset><legend className="text-sm font-medium">Modality token meters</legend><p className={muted}>Audio, image, video and text splits are part of the input or output tokens. A split you price here is charged at its own rate and taken out of the parent total, so it is not counted twice. If a modality is priced and cache reads or writes are priced too, a record whose cache counters are unknown cannot be priced.</p>
        <div className="mt-2 grid gap-2 sm:grid-cols-2">{api.MODALITY_METERS.map(meterInputs)}</div>
      </fieldset>
      <fieldset><legend className="text-sm font-medium">Unit meters</legend><p className={muted}>Milliseconds of audio, voice session or video, and the count of generated images. These are separate quantities, never tokens: a rate of 1 per 1000 on a milliseconds meter means 1 minor unit per second.</p>
        <div className="mt-2 grid gap-2 sm:grid-cols-2">{api.UNIT_METERS.map(meterInputs)}</div>
      </fieldset>
      <fieldset><legend className="text-sm font-medium">Selectors (optional)</legend><p className={muted}>Narrow the price to some exchanges. A price without a selector matches any value; when several prices match, the one with the most selectors wins, and an exact tie prices nothing.</p>
        <div className="mt-2 grid gap-3 sm:grid-cols-3">
          <label className="text-sm">Batch<select className={field} value={draft.batch} onChange={(e) => setDraft((d) => ({ ...d, batch: e.target.value as PriceDraft['batch'] }))}><option value="">Any exchange</option><option value="true">Batch exchanges only</option><option value="false">Non-batch exchanges only</option></select></label>
          <label className="text-sm">Image size<input className={field} placeholder="1024x1024" {...text('image_size')} /></label>
          <label className="text-sm">Image quality<input className={field} placeholder="high" {...text('image_quality')} /></label>
        </div>
      </fieldset>
      <div className="grid gap-3 sm:grid-cols-2"><label className="text-sm">Effective from (RFC 3339, optional)<input className={field} placeholder="2026-11-01T00:00:00Z" {...text('effective_from')} /></label><label className="text-sm">Effective until (RFC 3339, optional)<input className={field} placeholder="2027-01-01T00:00:00Z" {...text('effective_until')} /></label></div>
      <div className="flex gap-3"><button type="submit" className={button} disabled={busy}>Save price</button><button type="button" className="underline" onClick={close}>Cancel</button></div>
    </form>}
  </section>
}
