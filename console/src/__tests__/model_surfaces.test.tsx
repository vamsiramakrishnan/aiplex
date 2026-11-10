import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ModelSurfaceEditor from '../components/ModelSurfaceEditor'
import ModelSurfacesPanel from '../components/ModelSurfacesPanel'
import SurfaceCapacityPanel from '../components/SurfaceCapacityPanel'
import OpenBatches from '../components/OpenBatches'
import * as api from '../api/modelSurfaces'

const op = (id: string, method: string, path: string, transport = 'http', action = 'invoke'): api.ModelOperation => ({ id, method, path, transport, action, source_url: 'https://docs.example/' + id })
const descriptors: Record<string, api.ProviderDescriptor> = {
  openai: {
    id: 'openai', protocols: ['openai-chat', 'openai-responses', 'openai-realtime', 'openai-live'], credential_schemes: ['bearer'], default_stream_usage: 'require',
    operations: [op('openai.v1.chat-completions', 'POST', '/v1/chat/completions', 'sse'), op('openai.v1.realtime', 'GET', '/v1/realtime', 'websocket'), op('openai.v1.live', 'GET', '/v1/live/sessions', 'websocket')],
  },
  anthropic: { id: 'anthropic', protocols: ['anthropic-messages'], credential_schemes: ['api-key'], api_key_header: 'x-api-key', operations: [op('anthropic.messages', 'POST', '/v1/messages', 'sse'), op('anthropic.messages-count-tokens', 'POST', '/v1/messages/count_tokens')] },
  bedrock: { id: 'bedrock', protocols: ['bedrock-converse', 'bedrock-invoke'], credential_schemes: ['aws-sigv4', 'bearer'], operations: [op('bedrock.converse', 'POST', '/model/{model}/converse'), op('bedrock.converse-stream', 'POST', '/model/{model}/converse-stream', 'aws-eventstream')] },
  'azure-openai': { id: 'azure-openai', protocols: ['openai-chat', 'openai-responses'], credential_schemes: ['api-key', 'entra'], api_key_header: 'api-key', default_stream_usage: 'require', operations: [op('azure.v1.chat-completions', 'POST', '/openai/v1/chat/completions', 'sse'), op('azure.deployments.chat-completions', 'POST', '/openai/deployments/{model}/chat/completions', 'sse')] },
  'openai-compatible': { id: 'openai-compatible', protocols: ['openai-chat'], credential_schemes: ['bearer', 'none'], default_stream_usage: 'off', operations: [op('compat.chat-completions', 'POST', '{base}/chat/completions', 'sse')] },
  'gemini-developer': { id: 'gemini-developer', protocols: ['google-genai'], credential_schemes: ['api-key'], api_key_header: 'x-goog-api-key', operations: [op('gemini.models.generateContent', 'POST', '/v1beta/models/{model}:generateContent')] },
}

type Reply = { status?: number; body?: unknown }
let routes: Record<string, (url: URL, init?: RequestInit) => Reply>
let calls: { url: URL; init?: RequestInit }[]

beforeEach(() => {
  calls = []
  routes = {
    'GET /api/v1/llm/operations': (url) => ({ body: descriptors[url.searchParams.get('provider') ?? ''] ?? {} }),
  }
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://localhost')
    calls.push({ url, init })
    const handler = routes[`${init?.method ?? 'GET'} ${url.pathname}`]
    if (!handler) return new Response(JSON.stringify({ message: 'no route' }), { status: 500 })
    const { status = 200, body } = handler(url, init)
    return new Response(status === 204 ? null : JSON.stringify(body ?? {}), { status })
  }))
})
afterEach(() => vi.unstubAllGlobals())

function show(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}
const puts = () => calls.filter((c) => c.init?.method === 'PUT')
const lastPut = () => JSON.parse(String(puts().at(-1)!.init!.body)) as api.SurfaceSpec

async function chooseProvider(provider: string) {
  fireEvent.change(await screen.findByLabelText('Provider'), { target: { value: provider } })
  await screen.findByRole('group', { name: /Operations/ }).catch(() => undefined)
  await waitFor(() => expect(screen.getByLabelText('Protocol')).toBeInTheDocument())
}

describe('specFromDraft', () => {
  const fill = (provider: api.ProviderId, over: Partial<api.SurfaceDraft>): api.SurfaceDraft => ({ ...api.blankDraft(provider), id: 's', protocol: descriptors[provider].protocols[0], origin: 'https://x.example', models: 'm1\nm2', secretRef: 'key-ref', ...over })

  it('builds an Anthropic surface with an api-key header reference and catalog operations, not values', () => {
    const { spec, errors } = api.specFromDraft(fill('anthropic', { operationIds: ['anthropic.messages'], anthropic_betas: 'beta-1' }), descriptors.anthropic)
    expect(errors).toEqual([])
    expect(spec!.auth).toEqual({ type: 'api_key', in: 'header', name: 'x-api-key', secret_ref: 'key-ref' })
    expect(spec!.identity_ref).toBeUndefined()
    expect(spec!.surface.operations).toEqual([descriptors.anthropic.operations[0]])
    expect(spec!.surface.provider).toEqual({ id: 'anthropic', anthropic_version: '2023-06-01', anthropic_betas: ['beta-1'] })
    expect(spec!.surface.models).toEqual(['m1', 'm2'])
    expect(spec!.capacity_policy).toBeUndefined()
    expect(spec!.surface.websocket).toBeUndefined()
  })

  it('uses an identity reference alone for Bedrock and requires the region', () => {
    const base = fill('bedrock', { operationIds: ['bedrock.converse'], authMode: 'identity', identityRef: 'aws-default', secretRef: '' })
    expect(api.specFromDraft(base, descriptors.bedrock).errors.join()).toMatch(/signing region/)
    const { spec } = api.specFromDraft({ ...base, region: 'us-east-1' }, descriptors.bedrock)
    expect(spec!.identity_ref).toBe('aws-default')
    expect(spec!.auth).toBeUndefined()
    expect(spec!.surface.provider).toEqual({ id: 'bedrock', region: 'us-east-1' })
  })

  it('pins the Azure API version only for deployment-scoped operations', () => {
    const d = fill('azure-openai', { operationIds: ['azure.v1.chat-completions'], api_version: '2024-10-21' })
    expect(api.specFromDraft(d, descriptors['azure-openai']).spec!.surface.provider).toEqual({ id: 'azure-openai' })
    const dep = { ...d, operationIds: ['azure.deployments.chat-completions'], api_version: '' }
    expect(api.specFromDraft(dep, descriptors['azure-openai']).errors.join()).toMatch(/API version/)
    expect(api.specFromDraft({ ...dep, api_version: '2024-10-21' }, descriptors['azure-openai']).spec!.surface.provider).toEqual({ id: 'azure-openai', api_version: '2024-10-21' })
  })

  it('keeps fields the form does not model, drops the credential mode that was switched away from, and removes a disabled capacity policy', () => {
    const saved: api.SurfaceSpec = {
      surface: { id: 's', protocol: 'openai-chat', origin: 'https://api.openai.com', models: ['gpt'], operations: [descriptors.openai.operations[0]], provider: { id: 'openai', future_provider_field: { a: 1 } }, future_surface_field: true },
      identity_ref: 'bearer-env', capacity_policy: { cooldown_enabled: true, future_capacity_field: 'x' }, future_spec_field: 'kept',
    }
    const draft = { ...api.draftFromSpec(saved), authMode: 'secret' as const, secretRef: 'openai-key', capacityEnabled: false }
    const { spec } = api.specFromDraft(draft, descriptors.openai, saved)
    expect(spec).toMatchObject({ future_spec_field: 'kept', surface: { future_surface_field: true, provider: { id: 'openai', future_provider_field: { a: 1 } } }, auth: { type: 'bearer', secret_ref: 'openai-key' } })
    expect(spec!.identity_ref).toBeUndefined()
    expect(spec!.capacity_policy).toBeUndefined()
    // Turning capacity on keeps its unknown fields.
    const on = api.specFromDraft({ ...draft, capacityEnabled: true, cooldownEnabled: true }, descriptors.openai, saved).spec!
    expect(on.capacity_policy).toEqual({ cooldown_enabled: true, future_capacity_field: 'x' })
  })

  it('validates the WebSocket block: token metering is refused for Live voice, an allowance needs token metering', () => {
    const d = fill('openai', { operationIds: ['openai.v1.realtime'], wsMetering: 'token', wsMaxSessionSeconds: '600', wsTokenAllowance: '2048' })
    expect(api.specFromDraft(d, descriptors.openai).spec!.surface.websocket).toEqual({ metering: 'token', max_session_seconds: 600, token_allowance: 2048 })
    expect(api.specFromDraft({ ...d, operationIds: ['openai.v1.realtime', 'openai.v1.live'] }, descriptors.openai).errors.join()).toMatch(/Live voice/)
    expect(api.specFromDraft({ ...d, wsMetering: 'session' }, descriptors.openai).errors.join()).toMatch(/allowance requires token/)
    expect(api.specFromDraft({ ...d, wsMaxSessionSeconds: '30' }, descriptors.openai).errors.join()).toMatch(/60 to 86400/)
    // No WebSocket operation selected: the block is not written even if fields linger.
    expect(api.specFromDraft({ ...d, operationIds: ['openai.v1.chat-completions'] }, descriptors.openai).spec!.surface.websocket).toBeUndefined()
  })

  it('validates the capacity policy like the server and omits unset members', () => {
    const base = fill('openai', { operationIds: ['openai.v1.chat-completions'], capacityEnabled: true })
    expect(api.specFromDraft(base, descriptors.openai).errors.join()).toMatch(/cooldown or set a headroom/)
    expect(api.specFromDraft({ ...base, cooldownEnabled: true }, descriptors.openai).spec!.capacity_policy).toEqual({ cooldown_enabled: true })
    expect(api.specFromDraft({ ...base, headroomFraction: '0.2' }, descriptors.openai).errors.join()).toMatch(/minimum weight of at least 1/)
    expect(api.specFromDraft({ ...base, headroomFraction: '0.2', minWeight: '2', defaultWeight: '1', signalMaxAgeSeconds: '30' }, descriptors.openai).spec!.capacity_policy).toEqual({ headroom_fraction: 0.2, min_weight: 2, default_weight: 1, signal_max_age_seconds: 30 })
    expect(api.specFromDraft({ ...base, cooldownEnabled: true, headroomFraction: '1.5' }, descriptors.openai).errors.join()).toMatch(/between 0 and 1/)
    expect(api.specFromDraft({ ...base, maxCooldownSeconds: '30', headroomFraction: '0.5', minWeight: '1' }, descriptors.openai).errors.join()).toMatch(/requires cooldown/)
  })
})

describe('Model surface editor', () => {
  it('shows the fields of the chosen provider, driven by its catalog', async () => {
    show(<ModelSurfaceEditor onClose={() => {}} />)
    expect(await screen.findByLabelText('Organization (optional)')).toBeInTheDocument()
    expect(screen.queryByLabelText('Signing region')).not.toBeInTheDocument()
    expect(screen.getByLabelText(/openai\.v1\.chat-completions/)).toBeInTheDocument()
    expect(screen.getByLabelText('Protocol')).toHaveValue('openai-chat')

    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'bedrock' } })
    expect(await screen.findByLabelText('Signing region')).toBeInTheDocument()
    expect(screen.queryByLabelText('Organization (optional)')).not.toBeInTheDocument()
    expect(await screen.findByLabelText(/bedrock\.converse-stream/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/openai\.v1\.chat-completions/)).not.toBeInTheDocument()
    expect(calls.map((c) => c.url.search)).toEqual(['?provider=openai', '?provider=bedrock'])

    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'anthropic' } })
    expect(await screen.findByLabelText('Anthropic version')).toHaveValue('2023-06-01')
    expect(screen.getByText('Sent upstream in the x-api-key header.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Streaming usage (stream_usage)')).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'openai-compatible' } })
    expect(await screen.findByLabelText('Base path')).toBeInTheDocument()
    expect(screen.getByLabelText('Usage semantics')).toBeInTheDocument()
    expect(within(screen.getByLabelText('Streaming usage (stream_usage)')).getByRole('option', { name: /Provider default \(off\)/ })).toBeInTheDocument()
    expect(screen.getByLabelText('No credential')).toBeInTheDocument()
  })

  it('has no field that accepts a secret value: credentials are references', async () => {
    const { container } = show(<ModelSurfaceEditor onClose={() => {}} />)
    await screen.findByLabelText('Protocol')
    expect(container.querySelector('input[type="password"]')).toBeNull()
    expect(screen.getByText(/Credential \(a reference, never a value\)/)).toBeInTheDocument()
    expect(screen.getByLabelText('Secret reference name')).toBeInTheDocument()
    expect(screen.getByText(/this form has no field for a key or token/)).toBeInTheDocument()
    for (const provider of api.PROVIDER_IDS) {
      fireEvent.change(screen.getByLabelText('Provider'), { target: { value: provider } })
      await screen.findByLabelText('Protocol')
      await waitFor(() => expect(calls.at(-1)!.url.search).toBe(`?provider=${provider}`))
      expect(screen.queryByLabelText(/secret value|api key|key value|token value|password|bearer token/i)).not.toBeInTheDocument()
      expect(container.querySelector('input[type="password"]')).toBeNull()
    }
    // Anthropic and Gemini Developer take a secret reference only; others also take an identity reference.
    expect(screen.queryByLabelText('Identity reference')).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Provider'), { target: { value: 'bedrock' } })
    fireEvent.click(await screen.findByLabelText('Identity reference'))
    expect(screen.getByLabelText('Identity reference name')).toBeInTheDocument()
    expect(screen.queryByLabelText('Secret reference name')).not.toBeInTheDocument()
  })

  it('explains session and token metering and disables token metering for Live voice', async () => {
    show(<ModelSurfaceEditor onClose={() => {}} />)
    await screen.findByLabelText('Protocol')
    expect(screen.queryByRole('group', { name: 'WebSocket sessions' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByLabelText(/openai\.v1\.realtime/))
    const ws = screen.getByRole('group', { name: 'WebSocket sessions' })
    expect(ws).toHaveTextContent('Session metering admits the whole WebSocket session once, as a single request')
    expect(ws).toHaveTextContent('Token metering admits each response (or each renewable token allowance window) by the tokens it uses')
    const metering = within(ws).getByLabelText('Metering')
    expect(within(ws).getByRole('option', { name: 'token' })).toBeEnabled()
    fireEvent.change(metering, { target: { value: 'token' } })
    expect(within(ws).getByLabelText('Token allowance (optional)')).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText(/openai\.v1\.live/))
    expect(within(ws).getByRole('option', { name: 'token' })).toBeDisabled()
    expect(metering).toHaveValue('session')
    expect(ws).toHaveTextContent('unavailable for Live voice')
    expect(within(ws).queryByLabelText('Token allowance (optional)')).not.toBeInTheDocument()
  })

  it('keeps the capacity policy off by default and says it is per surface and per replica', async () => {
    show(<ModelSurfaceEditor onClose={() => {}} />)
    await screen.findByLabelText('Protocol')
    const toggle = screen.getByLabelText(/Enforce capacity signals from this provider/)
    expect(toggle).not.toBeChecked()
    expect(screen.queryByLabelText('Headroom fraction (optional)')).not.toBeInTheDocument()
    expect(screen.getByText(/applies per surface and per server replica/)).toBeInTheDocument()
    fireEvent.click(toggle)
    expect(screen.getByLabelText('Headroom fraction (optional)')).toBeInTheDocument()
  })

  it('saves a new surface as revision 0 with the catalog operations and a secret reference', async () => {
    routes['PUT /api/v1/llm/surfaces/oa'] = () => ({ body: { id: 'oa', revision: 1, spec: {} } })
    routes['GET /api/v1/llm/surfaces/'] = () => ({ body: [] })
    const onClose = vi.fn()
    show(<ModelSurfaceEditor onClose={onClose} />)
    await screen.findByLabelText('Protocol')
    fireEvent.change(screen.getByLabelText('Surface ID'), { target: { value: 'oa' } })
    fireEvent.change(screen.getByLabelText('Origin'), { target: { value: 'https://api.openai.com' } })
    fireEvent.change(screen.getByLabelText('Allowed models (one per line)'), { target: { value: 'gpt-x' } })
    fireEvent.change(screen.getByLabelText('Secret reference name'), { target: { value: 'openai-key' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save surface' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Select at least one operation')
    expect(puts()).toHaveLength(0)
    fireEvent.click(screen.getByLabelText(/openai\.v1\.chat-completions/))
    fireEvent.click(screen.getByRole('button', { name: 'Save surface' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(puts()[0].init!.headers).toMatchObject({ 'If-Match': '"0"' })
    expect(lastPut()).toEqual({
      surface: { id: 'oa', protocol: 'openai-chat', origin: 'https://api.openai.com', models: ['gpt-x'], operations: [descriptors.openai.operations[0]], provider: { id: 'openai' } },
      auth: { type: 'bearer', secret_ref: 'openai-key' },
    })
  })

  it('handles a revision conflict by offering the latest revision and re-saving at it', async () => {
    const saved: api.SurfaceRecord = { id: 'oa', revision: 3, spec: { surface: { id: 'oa', protocol: 'openai-chat', origin: 'https://api.openai.com', models: ['gpt-x'], operations: [descriptors.openai.operations[0]], provider: { id: 'openai' }, future_field: 1 }, auth: { type: 'bearer', secret_ref: 'k' } } }
    let revision = 3
    routes['PUT /api/v1/llm/surfaces/oa'] = (_url, init) => ((init!.headers as Record<string, string>)['If-Match'] === `"${revision}"` ? { body: { ...saved, revision: revision + 1 } } : { status: 409, body: { code: 'REVISION_CONFLICT', message: 'resource revision changed' } })
    routes['GET /api/v1/llm/surfaces/oa'] = () => ({ body: { ...saved, revision: 5, spec: { ...saved.spec, surface: { ...saved.spec.surface, models: ['gpt-x', 'gpt-y'] } } } })
    routes['GET /api/v1/llm/surfaces/'] = () => ({ body: [] })
    const onClose = vi.fn()
    show(<ModelSurfaceEditor record={saved} onClose={onClose} />)
    await screen.findByLabelText('Protocol')
    revision = 5 // someone else saved in the meantime
    fireEvent.click(screen.getByRole('button', { name: 'Save surface' }))
    const alert = await screen.findByText(/changed by someone else/)
    expect(alert).toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Load latest revision' }))
    expect(await screen.findByText(/Loaded revision 5/)).toBeInTheDocument()
    expect(screen.getByLabelText('Allowed models (one per line)')).toHaveValue('gpt-x\ngpt-y')
    fireEvent.click(screen.getByRole('button', { name: 'Save surface' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(puts().map((c) => (c.init!.headers as Record<string, string>)['If-Match'])).toEqual(['"3"', '"5"'])
    expect(lastPut().surface.future_field).toBe(1)
  })

  it('surfaces the server validation error verbatim', async () => {
    routes['PUT /api/v1/llm/surfaces/oa'] = () => ({ status: 400, body: { code: 'INVALID_SPEC', message: 'provider surfaces require exactly one of auth or identity_ref' } })
    const saved: api.SurfaceRecord = { id: 'oa', revision: 1, spec: { surface: { id: 'oa', protocol: 'openai-chat', origin: 'https://api.openai.com', models: ['m'], operations: [descriptors.openai.operations[0]], provider: { id: 'openai' } }, auth: { type: 'bearer', secret_ref: 'k' } } }
    show(<ModelSurfaceEditor record={saved} onClose={() => {}} />)
    await screen.findByLabelText('Protocol')
    fireEvent.click(screen.getByRole('button', { name: 'Save surface' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('provider surfaces require exactly one of auth or identity_ref')
  })
})

describe('Model surfaces panel', () => {
  it('lists surfaces with credential references and opens the capacity panel', async () => {
    routes['GET /api/v1/llm/surfaces/'] = () => ({ body: [
      { id: 'oa', revision: 2, spec: { surface: { id: 'oa', protocol: 'openai-chat', origin: 'https://api.openai.com', models: ['gpt-x'], operations: [descriptors.openai.operations[0]], provider: { id: 'openai' } }, auth: { type: 'bearer', secret_ref: 'openai-key' }, capacity_policy: { cooldown_enabled: true } } },
      { id: 'vtx', revision: 1, spec: { surface: { id: 'vtx', protocol: 'google-genai', origin: 'https://us-central1-aiplatform.googleapis.com', models: [] }, identity_ref: 'gcp-default' } },
    ] })
    routes['GET /api/v1/llm/surfaces/oa/capacity'] = () => ({ body: { surface_id: 'oa', observed: false, scope: 'replica-local last observation; advisory', capacity_policy: { enabled: true, policy: { cooldown_enabled: true, max_cooldown_seconds: 60, default_weight: 1, signal_max_age_seconds: 60 }, cooldown: { phase: 'none' } } } })
    show(<ModelSurfacesPanel />)
    const row = (await screen.findByRole('rowheader', { name: 'oa' })).closest('tr')!
    expect(row).toHaveTextContent('secret reference: openai-key')
    expect(row).toHaveTextContent('on')
    const google = screen.getByRole('rowheader', { name: 'vtx' }).closest('tr')!
    expect(google).toHaveTextContent('identity reference: gcp-default')
    expect(within(google).queryByRole('button', { name: /Edit surface/ })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Capacity of surface oa' }))
    expect(await screen.findByRole('region', { name: 'Capacity of oa' })).toHaveTextContent('No provider limit has been observed on this replica yet')
  })

  it('deletes at the listed revision and refreshes on a conflict', async () => {
    let listed = 2
    routes['GET /api/v1/llm/surfaces/'] = () => ({ body: [{ id: 'oa', revision: listed, spec: { surface: { id: 'oa', protocol: 'openai-chat', origin: 'https://x', provider: { id: 'openai' } }, auth: { type: 'bearer', secret_ref: 'k' } } }] })
    routes['DELETE /api/v1/llm/surfaces/oa'] = () => ({ status: 409, body: { code: 'REVISION_CONFLICT', message: 'changed' } })
    show(<ModelSurfacesPanel />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete surface oa' }))
    listed = 3
    fireEvent.click(screen.getByRole('button', { name: 'Confirm delete' }))
    expect(await screen.findByText(/oa was changed by someone else, so it was not deleted/)).toBeInTheDocument()
    expect(calls.find((c) => c.init?.method === 'DELETE')!.init!.headers).toMatchObject({ 'If-Match': '"2"' })
    await waitFor(() => expect(screen.getByRole('rowheader', { name: 'oa' }).closest('tr')).toHaveTextContent('3'))
  })
})

describe('Surface capacity panel', () => {
  it('shows last observed limits with unknown members as unknown, never zero, and the cooldown phase', async () => {
    routes['GET /api/v1/llm/surfaces/oa/capacity'] = () => ({ body: {
      surface_id: 'oa', observed: true, scope: 'replica-local last observation; advisory',
      limits: { provider: 'openai', observed_at: '2026-11-09T10:00:00Z', classification: 'rate_limited', retry_after_ms: 2000, requests: { limit: 100, remaining: 0 }, tokens: { limit: 1000 } },
      capacity_policy: { enabled: true, policy: { cooldown_enabled: true, max_cooldown_seconds: 60, headroom_fraction: 0.2, min_weight: 2, default_weight: 1, signal_max_age_seconds: 60 }, cooldown: { phase: 'cooling', until: '2026-11-09T10:00:02Z', retry_after_ms: 2000 } },
    } })
    show(<SurfaceCapacityPanel surfaceId="oa" />)
    const limits = await screen.findByRole('group', { name: 'Last observed limits' })
    expect(limits).toHaveTextContent('rate_limited')
    expect(limits).toHaveTextContent('0 remaining of 100') // a reported zero
    expect(limits).toHaveTextContent('unknown remaining of 1,000') // an unreported member
    expect(limits).toHaveTextContent(/Input tokens\s*not reported/)
    expect(limits).toHaveTextContent('2,000 ms')
    const policy = screen.getByRole('group', { name: 'Capacity policy state' })
    expect(policy).toHaveTextContent('Cooldown phase: cooling until 2026-11-09T10:00:02Z')
    expect(policy).toHaveTextContent('Cooling down: new requests are rejected')
    expect(policy).toHaveTextContent('headroom 0.2 (minimum weight 2, default weight 1)')
  })

  it('says the policy is off, and explains a 403', async () => {
    routes['GET /api/v1/llm/surfaces/idle/capacity'] = () => ({ body: { surface_id: 'idle', observed: false, scope: 's', capacity_policy: { enabled: false } } })
    routes['GET /api/v1/llm/surfaces/locked/capacity'] = () => ({ status: 403, body: { message: 'ADMIN_REQUIRED' } })
    const first = show(<SurfaceCapacityPanel surfaceId="idle" />)
    expect(await screen.findByText('Off. Limits are observed but nothing is enforced.')).toBeInTheDocument()
    first.unmount()
    show(<SurfaceCapacityPanel surfaceId="locked" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Capacity details need an administrator.')
  })
})

describe('Open batches', () => {
  const batch = (over: Partial<api.ModelBatch>): api.ModelBatch => ({ id: 'i1', surface_id: 'oa', provider: 'openai', batch_id: 'batch_1', create_operation: 'op', state: 'open', ended: false, expected: null, ...over })

  it('lists open batches with the exposure statement, null items as unknown and zero as 0', async () => {
    routes['GET /api/v1/llm/batches'] = (url) => (url.searchParams.get('cursor') === 'i2'
      ? { body: { batches: [batch({ id: 'i3', batch_id: 'batch_3', expected: 9 })] } }
      : { body: { batches: [batch({ attempt_id: 'att-1' }), batch({ id: 'i2', batch_id: 'batch_2', expected: 0, ended: true, provider_status: 'completed' })], next_cursor: 'i2' } })
    show(<OpenBatches />)
    expect(await screen.findByRole('note')).toHaveTextContent('An open batch holds quota exposure and a concurrency slot until its results are fetched through AIPlex')
    expect(calls[0].url.searchParams.get('state')).toBe('open')
    const rows = within(await screen.findByRole('table')).getAllByRole('row')
    const one = rows.find((r) => r.textContent?.includes('batch_1'))!
    const two = rows.find((r) => r.textContent?.includes('batch_2'))!
    expect(within(one).getAllByRole('cell').at(-1)).toHaveTextContent('unknown')
    expect(within(two).getAllByRole('cell').at(-1)).toHaveTextContent(/^0$/)
    expect(one).toHaveTextContent('att-1')
    fireEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByText('batch_3')).toBeInTheDocument()
    expect(calls.at(-1)!.url.searchParams.get('cursor')).toBe('i2')
  })

  it('asks for newest first, shows created and settled times, and highlights open batches older than a day', async () => {
    const old = new Date(Date.now() - 3 * 86_400_000).toISOString()
    const fresh = new Date(Date.now() - 3_600_000).toISOString()
    routes['GET /api/v1/llm/batches'] = () => ({ body: { batches: [
      batch({ id: 'i1', batch_id: 'batch_old', created_at: old }),
      batch({ id: 'i2', batch_id: 'batch_new', created_at: fresh }),
      batch({ id: 'i3', batch_id: 'batch_done', state: 'settled', created_at: old, settled_at: fresh }),
    ] } })
    show(<OpenBatches />)
    const rows = within(await screen.findByRole('table')).getAllByRole('row')
    expect(calls[0].url.searchParams.get('order')).toBe('newest')
    const row = (id: string) => rows.find((r) => r.textContent?.includes(id))!
    expect(row('batch_old')).toHaveAttribute('data-stale', 'true')
    expect(row('batch_old')).toHaveTextContent('open for 3 days')
    expect(row('batch_new')).not.toHaveAttribute('data-stale')
    expect(row('batch_done')).not.toHaveAttribute('data-stale')
    expect(row('batch_done')).toHaveTextContent(/settled/)
    fireEvent.change(screen.getByLabelText('Created'), { target: { value: '1d' } })
    await waitFor(() => expect(calls.at(-1)!.url.searchParams.get('older_than')).toMatch(/^\d{4}-\d\d-\d\dT/))
    expect(Date.now() - Date.parse(calls.at(-1)!.url.searchParams.get('older_than')!)).toBeGreaterThanOrEqual(86_000_000)
    fireEvent.change(screen.getByLabelText('Order'), { target: { value: 'id' } })
    await waitFor(() => expect(calls.at(-1)!.url.searchParams.get('order')).toBe('id'))
  })

  it('says plainly when nothing is open, and explains a 403', async () => {
    routes['GET /api/v1/llm/batches'] = () => ({ body: { batches: [] } })
    const first = show(<OpenBatches />)
    expect(await screen.findByText('No open batches. Nothing is holding quota for a batch.')).toBeInTheDocument()
    first.unmount()
    routes['GET /api/v1/llm/batches'] = () => ({ status: 403, body: { message: 'ADMIN_REQUIRED' } })
    show(<OpenBatches />)
    expect(await screen.findByRole('alert')).toHaveTextContent('You need the admin role for this.')
  })
})
