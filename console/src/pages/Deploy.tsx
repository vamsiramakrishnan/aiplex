import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { deployInstance, getCatalog, type Template, type Manifest, applyManifest } from '../api/client'
import { useModules } from '../hooks/useModules'

type Step = 'plane' | 'template' | 'config' | 'review'

export default function Deploy() {
  const [mode, setMode] = useState<'wizard' | 'yaml'>('wizard')

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Deploy</h1>
        <div className="flex gap-1 bg-gray-100 rounded p-1 dark:bg-gray-800">
          <button
            onClick={() => setMode('wizard')}
            className={`px-3 py-1 text-sm rounded ${mode === 'wizard' ? 'bg-white shadow dark:bg-gray-900' : ''}`}
          >
            Wizard
          </button>
          <button
            onClick={() => setMode('yaml')}
            className={`px-3 py-1 text-sm rounded ${mode === 'yaml' ? 'bg-white shadow dark:bg-gray-900' : ''}`}
          >
            JSON manifest
          </button>
        </div>
      </div>

      {mode === 'wizard' ? <DeployWizard /> : <ManifestApply />}
    </div>
  )
}

function DeployWizard() {
  const queryClient = useQueryClient()
  const { enabled, loading: modulesLoading, error: modulesError } = useModules()
  const deployable = enabled.filter((module) => ['mcplex', 'a2aplex', 'llmplex', 'skillsplex'].includes(module.name))
  const availablePlanes = deployable.map((module) => module.name)
  const availableKey = availablePlanes.join(',')
  const [searchParams] = useSearchParams()
  const [step, setStep] = useState<Step>('plane')
  const [plane, setPlane] = useState('')
  const [selectedTemplate, setSelectedTemplate] = useState<Template | null>(null)
  const [displayName, setDisplayName] = useState('')
  const [config, setConfig] = useState<Record<string, string>>({})

  const { data: catalog, isLoading } = useQuery({
    queryKey: ['catalog', plane],
    queryFn: () => getCatalog(plane),
    enabled: availablePlanes.includes(plane),
  })

  // Pre-populate from URL params
  useEffect(() => {
    const urlPlane = searchParams.get('plane')
    const urlTemplate = searchParams.get('template')

    const available = availableKey.split(',').filter(Boolean)
    if (urlPlane && available.includes(urlPlane)) {
      setPlane(urlPlane)
      if (urlTemplate) {
        // Move to template step and wait for catalog to load
        setStep('template')
      }
    } else {
      setPlane((current) => available.includes(current) ? current : available[0] ?? '')
      setStep('plane')
      setSelectedTemplate(null)
    }
  }, [searchParams, availableKey])

  // Auto-select template when catalog loads if template param is present
  useEffect(() => {
    const urlTemplate = searchParams.get('template')
    if (urlTemplate && catalog?.templates && !selectedTemplate) {
      const template = catalog.templates.find(t => t.id === urlTemplate)
      if (template) {
        setSelectedTemplate(template)
        setStep('config')
      }
    }
  }, [catalog, searchParams, selectedTemplate])

  const deploy = useMutation({
    mutationFn: (request: Parameters<typeof deployInstance>[0]) => {
      if (!availablePlanes.includes(request.plane)) throw new Error('Selected module is not enabled')
      return deployInstance(request)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['instances'] })
      setStep('plane')
      setSelectedTemplate(null)
      setDisplayName('')
      setConfig({})
    },
  })

  const steps: { key: Step; label: string }[] = [
    { key: 'plane', label: 'Choose Plane' },
    { key: 'template', label: 'Pick Template' },
    { key: 'config', label: 'Configure' },
    { key: 'review', label: 'Review & Deploy' },
  ]

  const planeDescriptions: Record<string, string> = {
    mcplex: 'Deploy MCP servers that expose tools to AI agents (search, database, APIs)',
    a2aplex: 'Deploy A2A agents that other agents can delegate tasks to',
    llmplex: 'Configure LLM model routing, failover, and cost budgets',
    skillsplex: 'Deploy skill servers that host invokable skill bundles for agents',
  }

  if (modulesLoading) return <p role="status">Loading modules…</p>
  if (modulesError) return <p role="alert">Module discovery unavailable: {modulesError}</p>
  if (deployable.length === 0) return <p role="status">No deployable modules are enabled.</p>

  return (
    <div>
      {deploy.isSuccess && deploy.data && <p role="status" className="mb-4 text-sm">Deployment request completed for <a className="underline" href={`/instances/${encodeURIComponent(deploy.data.id)}`}>{deploy.data.id}</a>. Reported status: {deploy.data.status}.</p>}
      {/* Step indicator */}
      <div className="flex gap-2 mb-6">
        {steps.map((s, i) => (
          <div key={s.key} className="flex items-center gap-2">
            <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-medium
              ${step === s.key ? 'bg-brand-600 text-brand-contrast' :
                steps.findIndex(x => x.key === step) > i ? 'bg-status-ok-bg text-status-ok-fg' :
                'bg-gray-100 text-gray-600 dark:text-gray-400 dark:bg-gray-800'}`}>
              {steps.findIndex(x => x.key === step) > i ? '\u2713' : i + 1}
            </div>
            <span className={`text-sm ${step === s.key ? 'font-medium' : 'text-gray-600 dark:text-gray-400'}`}>
              {s.label}
            </span>
            {i < steps.length - 1 && <div className="w-8 h-px bg-gray-200 dark:bg-gray-700" />}
          </div>
        ))}
      </div>

      {/* Step 1: Plane */}
      {step === 'plane' && (
        <div className="space-y-4">
          <p className="text-sm text-gray-600 dark:text-gray-400">What do you want to deploy?</p>
          <div className="grid gap-3">
            {deployable.map(({ name: p, label }) => (
              <button
                key={p}
                onClick={() => { setPlane(p); setStep('template') }}
                className={`text-left p-4 border-2 rounded-lg transition-colors
                  ${plane === p ? 'border-brand-500 bg-brand-50' : 'border-gray-200 hover:border-gray-300 dark:border-gray-800'}`}
              >
                <div className="font-medium">
                  {label}
                </div>
                <div className="text-sm text-gray-600 dark:text-gray-400 mt-1">{planeDescriptions[p]}</div>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Step 2: Template */}
      {step === 'template' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-gray-600 dark:text-gray-400">
              Choose a template from the {plane} catalog
            </p>
            <button onClick={() => setStep('plane')} className="text-sm text-brand-600 hover:underline">
              Back
            </button>
          </div>

          {isLoading ? (
            <div className="text-sm text-gray-600 dark:text-gray-400">Loading catalog...</div>
          ) : (
            <div className="grid gap-2">
              {(catalog?.templates ?? []).map(t => (
                <button
                  key={t.id}
                  onClick={() => { setSelectedTemplate(t); setStep('config') }}
                  className={`text-left p-3 border rounded-lg hover:border-brand-300 transition-colors
                    ${selectedTemplate?.id === t.id ? 'border-brand-500 bg-brand-50' : 'border-gray-200 dark:border-gray-800'}`}
                >
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-sm">{t.name}</span>
                    {t.verified && (
                      <span className="text-xs bg-status-ok-bg text-status-ok-fg px-1.5 py-0.5 rounded">verified</span>
                    )}
                  </div>
                  <div className="text-xs text-gray-600 dark:text-gray-400 mt-0.5">{t.description}</div>
                  {t.provider && (
                    <div className="text-xs text-gray-600 dark:text-gray-400 mt-1">Provider: {t.provider}</div>
                  )}
                </button>
              ))}
              {(catalog?.templates ?? []).length === 0 && (
                <p className="text-sm text-gray-600 dark:text-gray-400">No templates found for {plane}.</p>
              )}
            </div>
          )}
        </div>
      )}

      {/* Step 3: Configure */}
      {step === 'config' && selectedTemplate && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-gray-600 dark:text-gray-400">Configure {selectedTemplate.name}</p>
            <button onClick={() => setStep('template')} className="text-sm text-brand-600 hover:underline">
              Back
            </button>
          </div>

          <div>
            <label className="block text-sm font-medium mb-1">Display Name</label>
            <input
              value={displayName}
              onChange={e => setDisplayName(e.target.value)}
              className="w-full border rounded px-3 py-2 text-sm"
              placeholder={selectedTemplate.name}
            />
          </div>

          {selectedTemplate.pricing && (
            <div className="text-sm bg-brand-50 border border-brand-200 rounded p-3">
              <div className="font-medium text-brand-800">Pricing</div>
              <div className="text-brand-600">
                ${selectedTemplate.pricing.input}/M input tokens,
                ${selectedTemplate.pricing.output}/M output tokens
              </div>
            </div>
          )}

          <button
            onClick={() => setStep('review')}
            className="px-4 py-2 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700"
          >
            Review
          </button>
        </div>
      )}

      {/* Step 4: Review & Deploy */}
      {step === 'review' && selectedTemplate && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-gray-600 dark:text-gray-400">Review your deployment</p>
            <button onClick={() => setStep('config')} className="text-sm text-brand-600 hover:underline">
              Back
            </button>
          </div>

          <div className="bg-gray-50 rounded-lg p-4 text-sm space-y-2 dark:bg-gray-900">
            <div className="flex justify-between">
              <span className="text-gray-600 dark:text-gray-400">Plane</span>
              <span className="font-medium">{plane}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-600 dark:text-gray-400">Template</span>
              <span className="font-medium">{selectedTemplate.name}</span>
            </div>
            {displayName && (
              <div className="flex justify-between">
                <span className="text-gray-600 dark:text-gray-400">Name</span>
                <span className="font-medium">{displayName}</span>
              </div>
            )}
            {selectedTemplate.provider && (
              <div className="flex justify-between">
                <span className="text-gray-600 dark:text-gray-400">Provider</span>
                <span className="font-medium">{selectedTemplate.provider}</span>
              </div>
            )}
          </div>

          {/* Show equivalent YAML */}
          <details className="text-sm">
            <summary className="cursor-pointer text-gray-600 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200">
              View as JSON
            </summary>
            <pre className="mt-2 bg-gray-900 text-gray-100 p-3 rounded text-xs overflow-x-auto">
{JSON.stringify({ version: 'v1', instances: [{ name: displayName || selectedTemplate.name, plane, template: selectedTemplate.id, config: Object.keys(config).length > 0 ? config : undefined }] }, null, 2)}
            </pre>
          </details>

          {deploy.error && (
            <p className="text-sm text-status-denied-fg">{deploy.error.message}</p>
          )}

          <button
            onClick={() => deploy.mutate({
              plane,
              template_id: selectedTemplate.id,
              display_name: displayName || undefined,
              config: Object.keys(config).length > 0 ? config : undefined,
            })}
            disabled={deploy.isPending}
            className="w-full px-4 py-2 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700 disabled:opacity-50"
          >
            {deploy.isPending ? 'Deploying...' : 'Deploy'}
          </button>

        </div>
      )}
    </div>
  )
}

function ManifestApply() {
  const queryClient = useQueryClient()
  const { enabled } = useModules()
  const [yamlInput, setYamlInput] = useState('')
  const [result, setResult] = useState<{ applied: number; failed: string[] } | null>(null)

  const apply = useMutation({
    mutationFn: async () => {
      let manifest: Manifest
      try {
        manifest = JSON.parse(yamlInput) as Manifest
      } catch {
        throw new Error('Invalid JSON. Paste a valid JSON manifest.')
      }
      if (!manifest || typeof manifest !== 'object') throw new Error('A JSON manifest object is required.')
      const planes = enabled.map((module) => module.name)
      if (Array.isArray(manifest.instances) && manifest.instances.some((instance) => !planes.includes(instance.plane))) throw new Error('The manifest selects a deployment module that is not enabled.')
      if (Array.isArray(manifest.routes) && manifest.routes.length > 0 && !planes.includes('llmplex')) throw new Error('LLMPlex is not enabled for route configuration.')
      if (Array.isArray(manifest.agents) && manifest.agents.some((agent) => agent.auth_method === 'client_credentials')) throw new Error('Register client-credentials agents on the Agents page so you can securely receive their one-time issued secret.')
      return applyManifest(manifest)
    },
    onSuccess: (data) => {
      setResult(data)
      queryClient.invalidateQueries()
    },
  })

  return (
    <div className="space-y-4">
      <p className="text-sm text-gray-600 dark:text-gray-400">
        Paste a JSON manifest to request deployments, register public OAuth clients, and configure routes. Resources are applied sequentially; each failure is reported.
      </p>

      <div className="flex gap-2 flex-wrap">
        <button className="px-3 py-1 text-xs border rounded" onClick={() => { setYamlInput(JSON.stringify({ version: 'v1', instances: [], agents: [], routes: [] }, null, 2)); setResult(null) }}>Insert empty schema</button>
      </div>

      <textarea
        value={yamlInput}
        onChange={e => setYamlInput(e.target.value)}
        rows={16}
        className="w-full font-mono text-xs border rounded p-3 bg-gray-50 dark:bg-gray-900"
        aria-label="Deployment manifest" placeholder="Paste a JSON manifest using template IDs and modules available on this server."
      />

      {apply.error && (
        <p className="text-sm text-status-denied-fg">{(apply.error as Error).message}</p>
      )}

      {result && (
        <div className={`text-sm rounded p-3 ${result.failed.length > 0
          ? 'bg-status-warning-bg border border-status-warning-fg'
          : 'bg-status-ok-bg border border-status-ok-fg'}`}>
          <div className={result.failed.length > 0 ? 'text-status-warning-fg' : 'text-status-ok-fg'}>
            Applied {result.applied} resource(s)
            {result.failed.length > 0 && `, ${result.failed.length} failed`}
          </div>
          {result.failed.map((f, i) => (
            <div key={i} className="text-xs text-status-denied-fg mt-1">{f}</div>
          ))}
        </div>
      )}

      <button
        onClick={() => apply.mutate()}
        disabled={!yamlInput.trim() || apply.isPending}
        className="px-4 py-2 bg-brand-600 text-brand-contrast text-sm rounded hover:bg-brand-700 disabled:opacity-50"
      >
        {apply.isPending ? 'Applying...' : 'Apply Manifest'}
      </button>
    </div>
  )
}
