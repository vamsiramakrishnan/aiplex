import CopyButton from './CopyButton'
import type { IdentityStep } from '../api/agentIdentity'

/** A rendered setup step's code block with a copy button. */
export function StepCode({ step }: { step: IdentityStep }) {
  if (!step.content) return null
  return (
    <div className="relative mt-1">
      <pre className="bg-gray-900 text-gray-100 text-xs rounded p-2 pr-16 overflow-x-auto" data-language={step.language}><code>{step.content}</code></pre>
      <div className="absolute top-1 right-1"><CopyButton text={step.content} label={`code for ${step.title}`} className="text-xs bg-gray-700 text-white rounded px-2 py-0.5" /></div>
    </div>
  )
}

/** Ordered setup steps (title, description, optional copyable code). */
export function StepList({ steps, numbered }: { steps: IdentityStep[]; numbered?: boolean }) {
  return (
    <ol className={`space-y-3 ${numbered ? 'list-decimal ml-5' : 'list-none'}`}>
      {steps.map((s, i) => (
        <li key={`${i}-${s.title}`}>
          <div className="text-sm font-medium">{s.title}</div>
          {s.description && <p className="text-xs text-gray-600 dark:text-gray-400">{s.description}</p>}
          <StepCode step={s} />
        </li>
      ))}
    </ol>
  )
}
