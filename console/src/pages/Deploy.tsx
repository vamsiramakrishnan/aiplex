import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { deployInstance, getCatalog, type Template, type Manifest, applyManifest } from '../api/client'
import PlaneSelector from '../components/PlaneSelector'

type Step = 'plane' | 'template' | 'config' | 'review'

export default function Deploy() {
  const queryClient = useQueryClient()
  const [mode, setMode] = useState<'wizard' | 'yaml'>('wizard')

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-2xl font-bold">Deploy</h2>
        <div className="flex gap-1 bg-gray-100 rounded p-1">
          <button
            onClick={() => setMode('wizard')}
            className={`px-3 py-1 text-sm rounded ${mode === 'wizard' ? 'bg-white shadow' : ''}`}
          >
            Wizard
          </button>
          <button
            onClick={() => setMode('yaml')}
            className={`px-3 py-1 text-sm rounded ${mode === 'yaml' ? 'bg-white shadow' : ''}`}
          >
            YAML / JSON
          </button>
        </div>
      </div>

      {mode === 'wizard' ? <DeployWizard /> : <ManifestApply />}
    </div>
  )
}

function DeployWizard() {
  const queryClient = useQueryClient()
  const [step, setStep] = useState<Step>('plane')
  const [plane, setPlane] = useState('mcplex')
  const [selectedTemplate, setSelectedTemplate] = useState<Template | null>(null)
  const [displayName, setDisplayName] = useState('')
  const [config, setConfig] = useState<Record<string, string>>({})

  const { data: catalog, isLoading } = useQuery({
    queryKey: ['catalog', plane],
    queryFn: () => getCatalog(plane),
  })

  const deploy = useMutation({
    mutationFn: deployInstance,
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
  }

  return (
    <div>
      {/* Step indicator */}
      <div className="flex gap-2 mb-6">
        {steps.map((s, i) => (
          <div key={s.key} className="flex items-center gap-2">
            <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-medium
              ${step === s.key ? 'bg-brand-600 text-white' :
                steps.findIndex(x => x.key === step) > i ? 'bg-green-100 text-green-700' :
                'bg-gray-100 text-gray-400'}`}>
              {steps.findIndex(x => x.key === step) > i ? '\u2713' : i + 1}
            </div>
            <span className={`text-sm ${step === s.key ? 'font-medium' : 'text-gray-400'}`}>
              {s.label}
            </span>
            {i < steps.length - 1 && <div className="w-8 h-px bg-gray-200" />}
          </div>
        ))}
      </div>

      {/* Step 1: Plane */}
      {step === 'plane' && (
        <div className="space-y-4">
          <p className="text-sm text-gray-600">What do you want to deploy?</p>
          <div className="grid gap-3">
            {['mcplex', 'a2aplex', 'llmplex'].map(p => (
              <button
                key={p}
                onClick={() => { setPlane(p); setStep('template') }}
                className={`text-left p-4 border-2 rounded-lg transition-colors
                  ${plane === p ? 'border-brand-500 bg-brand-50' : 'border-gray-200 hover:border-gray-300'}`}
              >
                <div className="font-medium">
                  {p === 'mcplex' ? 'MCPlex \u2014 Tools' :
                   p === 'a2aplex' ? 'A2APlex \u2014 Agents' :
                   'LLMPlex \u2014 Models'}
                </div>
                <div className="text-sm text-gray-500 mt-1">{planeDescriptions[p]}</div>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Step 2: Template */}
      {step === 'template' && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-gray-600">
              Choose a template from the {plane} catalog
            </p>
            <button onClick={() => setStep('plane')} className="text-sm text-brand-600 hover:underline">
              Back
            </button>
          </div>

          {isLoading ? (
            <div className="text-sm text-gray-400">Loading catalog...</div>
          ) : (
            <div className="grid gap-2">
              {(catalog?.templates ?? []).map(t => (
                <button
                  key={t.id}
                  onClick={() => { setSelectedTemplate(t); setStep('config') }}
                  className={`text-left p-3 border rounded-lg hover:border-brand-300 transition-colors
                    ${selectedTemplate?.id === t.id ? 'border-brand-500 bg-brand-50' : 'border-gray-200'}`}
                >
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-sm">{t.name}</span>
                    {t.verified && (
                      <span className="text-xs bg-green-100 text-green-700 px-1.5 py-0.5 rounded">verified</span>
                    )}
                  </div>
                  <div className="text-xs text-gray-500 mt-0.5">{t.description}</div>
                  {t.provider && (
                    <div className="text-xs text-gray-400 mt-1">Provider: {t.provider}</div>
                  )}
                </button>
              ))}
              {(catalog?.templates ?? []).length === 0 && (
                <p className="text-sm text-gray-400">No templates found for {plane}.</p>
              )}
            </div>
          )}
        </div>
      )}

      {/* Step 3: Configure */}
      {step === 'config' && selectedTemplate && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-gray-600">Configure {selectedTemplate.name}</p>
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

          {selectedTemplate.config_schema && (
            <div className="text-xs text-gray-500">
              Additional configuration available for this template.
            </div>
          )}

          {selectedTemplate.pricing && (
            <div className="text-sm bg-blue-50 border border-blue-200 rounded p-3">
              <div className="font-medium text-blue-800">Pricing</div>
              <div className="text-blue-600">
                ${selectedTemplate.pricing.input}/M input tokens,
                ${selectedTemplate.pricing.output}/M output tokens
              </div>
            </div>
          )}

          <button
            onClick={() => setStep('review')}
            className="px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700"
          >
            Review
          </button>
        </div>
      )}

      {/* Step 4: Review & Deploy */}
      {step === 'review' && selectedTemplate && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-gray-600">Review your deployment</p>
            <button onClick={() => setStep('config')} className="text-sm text-brand-600 hover:underline">
              Back
            </button>
          </div>

          <div className="bg-gray-50 rounded-lg p-4 text-sm space-y-2">
            <div className="flex justify-between">
              <span className="text-gray-500">Plane</span>
              <span className="font-medium">{plane}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500">Template</span>
              <span className="font-medium">{selectedTemplate.name}</span>
            </div>
            {displayName && (
              <div className="flex justify-between">
                <span className="text-gray-500">Name</span>
                <span className="font-medium">{displayName}</span>
              </div>
            )}
            {selectedTemplate.provider && (
              <div className="flex justify-between">
                <span className="text-gray-500">Provider</span>
                <span className="font-medium">{selectedTemplate.provider}</span>
              </div>
            )}
          </div>

          {/* Show equivalent YAML */}
          <details className="text-sm">
            <summary className="cursor-pointer text-gray-500 hover:text-gray-700">
              View as YAML
            </summary>
            <pre className="mt-2 bg-gray-900 text-gray-100 p-3 rounded text-xs overflow-x-auto">
{`version: v1
instances:
  - name: ${displayName || selectedTemplate.name}
    plane: ${plane}
    template: ${selectedTemplate.id}`}
            </pre>
          </details>

          {deploy.error && (
            <p className="text-sm text-red-600">{deploy.error.message}</p>
          )}

          <button
            onClick={() => deploy.mutate({
              plane,
              template_id: selectedTemplate.id,
              display_name: displayName || undefined,
              config: Object.keys(config).length > 0 ? config : undefined,
            })}
            disabled={deploy.isPending}
            className="w-full px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700 disabled:opacity-50"
          >
            {deploy.isPending ? 'Deploying...' : 'Deploy'}
          </button>

          {deploy.isSuccess && (
            <div className="bg-green-50 border border-green-200 rounded p-3 text-sm text-green-700">
              Deployed successfully! View it in the instances list.
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function ManifestApply() {
  const queryClient = useQueryClient()
  const [yamlInput, setYamlInput] = useState('')
  const [result, setResult] = useState<{ applied: number; failed: string[] } | null>(null)

  const apply = useMutation({
    mutationFn: async () => {
      let manifest: Manifest
      try {
        manifest = JSON.parse(yamlInput) as Manifest
      } catch {
        throw new Error('Invalid JSON. Paste a valid manifest (use the examples below as a starting point).')
      }
      return applyManifest(manifest)
    },
    onSuccess: (data) => {
      setResult(data)
      queryClient.invalidateQueries()
    },
  })

  const examples: { name: string; description: string; file: string }[] = [
    { name: 'Quickstart', description: 'Tools + agents + models', file: 'quickstart' },
    { name: 'MCPlex Only', description: 'Just MCP servers', file: 'mcplex-only' },
    { name: 'LLM Routing', description: 'Model failover + budgets', file: 'llm-routing' },
    { name: 'Multi-Agent', description: 'Full agent ecosystem', file: 'multi-agent' },
  ]

  return (
    <div className="space-y-4">
      <p className="text-sm text-gray-600">
        Paste a manifest to deploy instances, register agents, and configure routes in one step.
      </p>

      <div className="flex gap-2 flex-wrap">
        {examples.map(ex => (
          <button
            key={ex.file}
            className="px-3 py-1 text-xs border rounded hover:bg-gray-50"
            onClick={() => {
              // In production, fetch from /examples/${ex.file}.json
              // For now, show a helpful message
              setYamlInput(`{\n  "version": "v1",\n  "instances": [],\n  "agents": [],\n  "routes": []\n}`)
            }}
          >
            {ex.name}
            <span className="text-gray-400 ml-1">{ex.description}</span>
          </button>
        ))}
      </div>

      <textarea
        value={yamlInput}
        onChange={e => setYamlInput(e.target.value)}
        rows={16}
        className="w-full font-mono text-xs border rounded p-3 bg-gray-50"
        placeholder={`Paste JSON manifest here. Example:
{
  "version": "v1",
  "instances": [
    { "name": "Knowledge Base", "plane": "mcplex", "template": "kb-search-server" }
  ],
  "agents": [
    {
      "client_id": "tutor-agent",
      "display_name": "Tutor Agent",
      "auth_method": "client_credentials",
      "grant_types": ["client_credentials"],
      "allowed_scopes": ["mcp:tools:search_curriculum", "llm:model:gemini-2.5-flash"]
    }
  ]
}`}
      />

      {apply.error && (
        <p className="text-sm text-red-600">{(apply.error as Error).message}</p>
      )}

      {result && (
        <div className={`text-sm rounded p-3 ${result.failed.length > 0
          ? 'bg-yellow-50 border border-yellow-200'
          : 'bg-green-50 border border-green-200'}`}>
          <div className={result.failed.length > 0 ? 'text-yellow-800' : 'text-green-700'}>
            Applied {result.applied} resource(s)
            {result.failed.length > 0 && `, ${result.failed.length} failed`}
          </div>
          {result.failed.map((f, i) => (
            <div key={i} className="text-xs text-red-600 mt-1">{f}</div>
          ))}
        </div>
      )}

      <button
        onClick={() => apply.mutate()}
        disabled={!yamlInput.trim() || apply.isPending}
        className="px-4 py-2 bg-brand-600 text-white text-sm rounded hover:bg-brand-700 disabled:opacity-50"
      >
        {apply.isPending ? 'Applying...' : 'Apply Manifest'}
      </button>
    </div>
  )
}
