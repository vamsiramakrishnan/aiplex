import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { errorStatus, getQuotaPlan, type AccessPointSpec, type QuotaPlan } from '../api/accessPoints'
import CopyButton from './CopyButton'

/** Renders the QuotaPlex fragment for an access point's QoS block. Read-only: applies nothing. */
export default function QuotaPlanPanel({ spec }: { spec: AccessPointSpec }) {
  const [plan, setPlan] = useState<QuotaPlan | null>(null)
  const render = useMutation({ mutationFn: () => getQuotaPlan(spec.id), onSuccess: setPlan })
  const forbidden = errorStatus(render.error) === 403

  return (
    <section aria-label="Quota plan" className="space-y-3">
      <h3 className="text-lg font-medium">Quota plan</h3>
      {!spec.qos ? (
        <p className="text-sm text-gray-500">This access point has no QoS block, so there is nothing to render. Requests are not subject to access-point limits.</p>
      ) : (
        <>
          <p role="note" className="text-sm rounded border border-amber-300 bg-amber-50 text-amber-900 dark:bg-amber-950 dark:text-amber-200 dark:border-amber-800 p-3">
            This access point has a QoS block, so requests through it are rejected until a matching quota binding is active.
            Rendering a plan does not create one.
          </p>
          <p className="text-sm text-gray-600 dark:text-gray-400">
            Render the QuotaPlex policy fragment for QoS class <strong>{spec.qos.class}</strong> to review it. <strong>Nothing is applied</strong> by this
            view: copy the fragment into the QuotaPlex policy flow (<Link className="underline" to="/quotaplex">QuotaPlex</Link>) to activate it.
          </p>
          <button type="button" onClick={() => render.mutate()} disabled={render.isPending}
            className="px-3 py-1.5 text-sm bg-brand-600 text-white rounded disabled:opacity-50">
            {render.isPending ? 'Rendering…' : plan ? 'Render again' : 'Render quota plan'}
          </button>
          {render.isError && (
            <p role="alert" className="text-sm text-red-600">
              {forbidden ? 'You do not have access to quota plans for this access point (administrator role required).' : `Could not render the quota plan: ${(render.error as Error).message}`}
            </p>
          )}
          {plan && <PlanView plan={plan} />}
        </>
      )}
    </section>
  )
}

function PlanView({ plan }: { plan: QuotaPlan }) {
  const fragment = JSON.stringify({ fragment: plan.fragment, ...(plan.scheduler ? { scheduler: plan.scheduler } : {}) }, null, 2)
  return (
    <div aria-label="Rendered quota plan" className="space-y-2">
      <p className="text-sm font-medium">Not applied. Read-only preview.</p>
      <p className={`text-sm ${plan.validation.valid ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`}>
        {plan.validation.valid ? `Valid (checked by ${plan.validation.validator}).` : `Not valid (${plan.validation.validator}): ${plan.validation.error ?? 'unknown reason'}`}
      </p>
      {plan.warnings.length > 0 && (
        <div>
          <h4 className="text-sm font-semibold">Warnings</h4>
          <ul className="list-disc ml-5 text-sm text-amber-800 dark:text-amber-300">{plan.warnings.map(w => <li key={w}>{w}</li>)}</ul>
        </div>
      )}
      <div className="relative">
        <pre tabIndex={0} aria-label="Quota policy fragment" className="bg-gray-900 text-gray-100 text-xs rounded p-3 pr-16 overflow-x-auto max-h-96">{fragment}</pre>
        <div className="absolute top-2 right-2"><CopyButton text={fragment} label="quota policy fragment" className="text-xs bg-gray-700 text-white rounded px-2 py-0.5" /></div>
      </div>
    </div>
  )
}
