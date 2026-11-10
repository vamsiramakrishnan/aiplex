import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { errorStatus } from '../api/accessPoints'
import {
  deleteUserProvider, listUserProviderPresets, listUserProviders, planUserProvider,
  type UserPlan, type UserProvider,
} from '../api/userIdentity'
import { StepList } from '../components/SetupSteps'
import UserProviderForm, { describeProviderError } from '../components/UserProviderForm'
import RefreshGroups from '../components/RefreshGroups'
import UserTokenTest from '../components/UserTokenTest'

function PlanView({ plan, onClose }: { plan: UserPlan; onClose: () => void }) {
  return (
    <section aria-label={`Setup plan for ${plan.provider.id}`} className="rounded border border-gray-200 dark:border-gray-800 p-3 space-y-3">
      <div className="flex justify-between">
        <h3 className="text-sm font-semibold">Setup plan: {plan.provider.id} ({plan.vendor})</h3>
        <button type="button" className="text-xs text-gray-500" onClick={onClose}>Close plan</button>
      </div>
      <dl className="text-xs grid grid-cols-[auto,1fr] gap-x-3 gap-y-1">
        <dt className="text-gray-500">Issuer</dt><dd className="font-mono break-all">{plan.issuer}</dd>
        <dt className="text-gray-500">Audience</dt><dd className="font-mono break-all">{plan.audience}</dd>
        <dt className="text-gray-500">Subject claim</dt><dd className="font-mono">{plan.subject_claim}</dd>
        <dt className="text-gray-500">Groups from</dt><dd>{plan.groups_from}{plan.group_claim ? <> (<code>{plan.group_claim}</code>)</> : null}</dd>
        <dt className="text-gray-500">Principal pool</dt><dd className="font-mono break-all">{plan.principal_pool}</dd>
      </dl>
      <div><h4 className="text-xs font-semibold uppercase text-gray-500 mb-1">In your identity provider</h4><StepList steps={plan.steps} numbered /></div>
      {plan.token_snippet?.title && <div><h4 className="text-xs font-semibold uppercase text-gray-500 mb-1">Getting a token</h4><StepList steps={[plan.token_snippet]} /></div>}
      {!!plan.aiplex_steps?.length && <div><h4 className="text-xs font-semibold uppercase text-gray-500 mb-1">In AIPlex</h4><StepList steps={plan.aiplex_steps} numbered /></div>}
      {!!plan.notes?.length && <ul className="list-disc ml-5 text-xs text-gray-600 dark:text-gray-400">{plan.notes.map(n => <li key={n}>{n}</li>)}</ul>}
    </section>
  )
}

export default function UserIdentity() {
  const queryClient = useQueryClient()
  const presets = useQuery({ queryKey: ['user-provider-presets'], queryFn: listUserProviderPresets })
  const providers = useQuery({ queryKey: ['user-providers'], queryFn: listUserProviders })
  const [form, setForm] = useState<{ mode: 'add' } | { mode: 'edit'; provider: UserProvider } | null>(null)
  const [plan, setPlan] = useState<UserPlan | null>(null)
  const [testing, setTesting] = useState<string | null>(null)
  const [notice, setNotice] = useState('')
  const [warnings, setWarnings] = useState<string[]>([])
  const [refreshing, setRefreshing] = useState<UserProvider | null>(null)
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['user-providers'] })

  const planMutation = useMutation({ mutationFn: planUserProvider, onSuccess: setPlan })
  const remove = useMutation({
    mutationFn: (p: UserProvider) => deleteUserProvider(p.id, p.revision),
    onSuccess: (_d, p) => { setNotice(`Removed provider ${p.id}.`); if (plan?.provider.id === p.id) setPlan(null); void refresh() },
    onError: () => void refresh(),
  })
  const [confirming, setConfirming] = useState<string | null>(null)

  const catalog = presets.data
  const presetName = (id: string) => catalog?.presets.find(p => p.id === id)?.display_name ?? id
  const loadError = providers.error ?? presets.error
  const forbidden = errorStatus(loadError) === 403

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-2xl font-bold">User identity</h2>
          <p className="text-gray-500">Trust your organisation&apos;s identity provider so people sign in to AIPlex-governed models and tools with their own accounts.</p>
        </div>
        <button type="button" className="px-3 py-1.5 text-sm bg-brand-600 text-white rounded disabled:opacity-50" disabled={!catalog}
          onClick={() => setForm(form ? null : { mode: 'add' })}>{form ? 'Cancel' : 'Add provider'}</button>
      </header>

      {catalog && !catalog.live_authentication && (
        <p role="status" className="text-sm bg-yellow-50 border border-yellow-200 text-yellow-800 dark:bg-yellow-950 dark:border-yellow-800 dark:text-yellow-200 rounded p-2">
          Live authentication is not enabled on this server. You can configure providers and test tokens against them, but they do not yet authenticate real requests.
        </p>
      )}
      {(presets.isLoading || providers.isLoading) && <p role="status">Loading user identity providers…</p>}
      {loadError && (
        <p role="alert" className="text-sm text-red-600">
          {forbidden ? 'You do not have access to user identity providers (administrator or deployer role required).' : `Could not load user identity providers: ${(loadError as Error).message}`}
        </p>
      )}
      {notice && <p role="status" className="text-sm text-green-700">{notice}</p>}
      {warnings.length > 0 && <ul aria-label="Warnings" className="list-disc ml-5 text-sm text-amber-800 dark:text-amber-300">{warnings.map(w => <li key={w}>{w}</li>)}</ul>}

      {form && catalog && (
        <UserProviderForm
          key={form.mode === 'edit' ? `${form.provider.id}@${form.provider.revision}` : 'add'}
          presets={catalog.presets}
          existing={form.mode === 'edit' ? form.provider : undefined}
          onCancel={() => setForm(null)}
          onDone={(saved) => { setForm(null); setNotice(`Saved provider ${saved.id}.`); setWarnings(saved.warnings ?? []); void refresh() }}
        />
      )}

      {remove.error && (
        <p role="alert" className="text-sm text-red-600">{describeProviderError(remove.error, 'Could not remove provider')}</p>
      )}
      {planMutation.error && <p role="alert" className="text-sm text-red-600">{describeProviderError(planMutation.error, 'Could not build the setup plan')}</p>}

      {providers.data && providers.data.length === 0 && !providers.error && (
        <p className="text-gray-500">No user identity providers yet. Add one to let people from Google Workspace, Microsoft Entra ID, Okta or any OIDC issuer sign in.</p>
      )}
      {!!providers.data?.length && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <caption className="sr-only">User identity providers</caption>
            <thead>
              <tr className="text-xs text-gray-500 border-b border-gray-200 dark:border-gray-800">
                <th scope="col" className="py-1 pr-3">Provider</th><th scope="col" className="pr-3">Preset</th><th scope="col" className="pr-3">Issuer</th>
                <th scope="col" className="pr-3">Audience</th><th scope="col" className="pr-3">Subject claim</th><th scope="col" className="pr-3">State</th><th scope="col"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {providers.data.map(p => (
                <tr key={p.id} className="border-t border-gray-200 dark:border-gray-800 align-top">
                  <th scope="row" className="py-1 pr-3 font-normal font-mono text-xs">{p.display_name ? `${p.display_name} (${p.id})` : p.id}</th>
                  <td className="pr-3">{presetName(p.preset)}</td>
                  <td className="pr-3 font-mono text-xs break-all">{p.issuer}</td>
                  <td className="pr-3 font-mono text-xs break-all">{p.audience}</td>
                  <td className="pr-3 font-mono text-xs">{p.subject_claim}</td>
                  <td className="pr-3">{p.disabled ? <span className="text-gray-500">Disabled</span> : <span className="text-green-700">Enabled</span>}</td>
                  <td className="whitespace-nowrap space-x-2">
                    <button type="button" className="text-brand-600 hover:underline" aria-label={`Edit provider ${p.id}`} onClick={() => setForm({ mode: 'edit', provider: p })}>Edit</button>
                    <button type="button" className="text-brand-600 hover:underline" aria-label={`Show setup plan for ${p.id}`} onClick={() => planMutation.mutate(p.id)}>Plan</button>
                    {p.directory && <button type="button" className="text-brand-600 hover:underline" aria-label={`Refresh groups for a user of ${p.id}`} onClick={() => setRefreshing(p)}>Refresh groups</button>}
                    <button type="button" className="text-brand-600 hover:underline" aria-label={`Test a token against ${p.id}`} onClick={() => setTesting(p.id)}>Test</button>
                    <button type="button" className="text-red-600 hover:underline" aria-label={`Remove provider ${p.id}`} onClick={() => setConfirming(p.id)}>Remove</button>
                    {confirming === p.id && (
                      <span role="alertdialog" aria-label={`Confirm removal of ${p.id}`} className="ml-2 inline-flex items-center gap-2 text-xs">
                        Users from this provider can no longer sign in.
                        <button type="button" className="rounded bg-red-600 text-white px-2 py-0.5" onClick={() => { setConfirming(null); remove.mutate(p) }}>Confirm remove</button>
                        <button type="button" className="underline" onClick={() => setConfirming(null)}>Cancel</button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {refreshing && <RefreshGroups provider={refreshing} onClose={() => setRefreshing(null)} />}
      {plan && <PlanView plan={plan} onClose={() => setPlan(null)} />}
      {providers.data && !providers.error && (
        testing === null
          ? <button type="button" className="text-sm text-brand-600 hover:underline" onClick={() => setTesting('')}>Test a token</button>
          : <div className="space-y-2"><UserTokenTest providers={providers.data} initialProvider={testing} /><button type="button" className="text-xs text-gray-500" onClick={() => setTesting(null)}>Close token test</button></div>
      )}
    </div>
  )
}
