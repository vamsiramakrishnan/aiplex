import { fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EndpointList from '../components/EndpointList'
import { ConnectDetails } from '../components/ConnectWizard'
import { endpointGroups, endpointGroupsFromConnect, surfaceUnroutableReason, wiresForProtocol, WIRES, type AccessPointSpec } from '../api/accessPoints'
import * as overview from '../api/overview'

vi.mock('../api/overview', async (original) => ({ ...(await original<typeof import('../api/overview')>()), getConnect: vi.fn(), listClientProfiles: vi.fn() }))

// The server's wire table: accessPointWires (access_point_models.go),
// accessPointMediaWires (client_gateway_media.go) and the utility wires
// (client_gateway_utility.go). Keep this list in step with them.
const SERVER_WIRES: [id: string, surfaces: string[]][] = [
  ['openai-chat-completions', ['openai-chat']],
  ['openai-responses', ['openai-responses']],
  ['anthropic-messages', ['anthropic-messages']],
  ['google-genai', ['google-genai']],
  ['bedrock-converse', ['bedrock-converse']],
  ['bedrock-invoke', ['bedrock-invoke']],
  ['openai-realtime', ['openai-realtime']],
  ['google-live', ['google-genai']],
  ['openai-embeddings', ['openai-embeddings']],
  ['openai-moderations', ['openai-moderations']],
  ['openai-images', ['openai-chat', 'openai-responses', 'openai-images', 'openai-audio']],
  ['openai-audio', ['openai-chat', 'openai-responses', 'openai-images', 'openai-audio']],
]

const spec: AccessPointSpec = { id: 'ide', caller_kinds: ['user'], subjects: {}, models: [{ name: 'e', surface_ref: 'emb', native_model: 'text-embedding' }] }
const surface = (id: string, protocol: string) => ({ id, revision: 1, spec: { surface: { id, protocol } } })

describe('wire table', () => {
  it('lists every wire the server serves, with the surface protocols that reach each', () => {
    expect(WIRES.map((w) => [w.id, [...w.surfaceProtocols]])).toEqual(SERVER_WIRES)
  })

  it('treats surface protocols with a media, embeddings or moderations wire as routable', () => {
    for (const protocol of ['openai-embeddings', 'openai-moderations', 'openai-images', 'openai-audio', 'bedrock-converse', 'openai-realtime']) {
      expect(surfaceUnroutableReason({ id: 's', protocol })).toBeUndefined()
    }
    expect(surfaceUnroutableReason({ id: 's', protocol: 'openai-batches' })).toMatch(/no client protocol for openai-batches/)
    expect(wiresForProtocol('openai-chat').map((w) => w.id)).toEqual(['openai-chat-completions', 'openai-images', 'openai-audio'])
    expect(wiresForProtocol('openai-embeddings').map((w) => w.id)).toEqual(['openai-embeddings'])
  })

  it('derives endpoint groups from the surface protocols, including the new utility wires and their further paths', () => {
    const groups = endpointGroups(spec, 'https://aiplex.example', [surface('emb', 'openai-embeddings')])
    expect(groups.map((g) => g.id)).toEqual(['openai-embeddings', 'models'])
    expect(groups[0].urls).toEqual([{ label: 'Endpoint', url: 'https://aiplex.example/ap/ide/v1/embeddings' }])
    const media = endpointGroups({ ...spec, models: [{ name: 'i', surface_ref: 'img', native_model: 'gpt-image' }] }, 'https://aiplex.example', [surface('img', 'openai-images')])
    expect(media.find((g) => g.id === 'openai-images')!.urls.map((u) => u.url)).toEqual(['https://aiplex.example/ap/ide/v1/images/generations', 'https://aiplex.example/ap/ide/v1/images/edits'])
    expect(media.find((g) => g.id === 'openai-audio')!.urls.map((u) => u.url)).toEqual(['https://aiplex.example/ap/ide/v1/audio/transcriptions', 'https://aiplex.example/ap/ide/v1/audio/translations', 'https://aiplex.example/ap/ide/v1/audio/speech'])
    const anthropic = endpointGroups({ ...spec, models: [{ name: 'c', surface_ref: 'a', native_model: 'claude' }] }, 'https://aiplex.example', [surface('a', 'anthropic-messages')])
    expect(anthropic[0].urls.map((u) => u.url)).toEqual(['https://aiplex.example/ap/ide/v1/messages', 'https://aiplex.example/ap/ide/v1/messages/count_tokens'])
    // With unknown surfaces every wire is offered, flagged as undetermined.
    const unknown = endpointGroups(spec, 'https://aiplex.example', undefined)
    expect(unknown.filter((g) => g.note).map((g) => g.id)).toEqual(SERVER_WIRES.map(([id]) => id))
  })

  it('builds groups from a server connect response that lists the new endpoints', () => {
    const connect: overview.ConnectInfo = {
      access_point_id: 'ide', client: 'generic', base_urls: { openai: 'https://x/ap/ide/v1' },
      endpoints: [
        { protocol: 'openai-realtime', transport: 'websocket', url: 'wss://x/ap/ide/v1/realtime', label: 'OpenAI Realtime', query: ['model'], models: ['r'] },
        { protocol: 'bedrock-converse', transport: 'http-stream', url: 'https://x/ap/ide/model/{model}/converse-stream', label: 'ConverseStream', models: ['b'] },
      ],
      models: [
        { name: 'e', protocols: ['openai-embeddings', 'openai-moderations'] }, { name: 'i', protocols: ['openai-chat-completions', 'openai-images', 'openai-audio'] },
        { name: 'r', protocols: ['openai-realtime'] }, { name: 'b', protocols: ['bedrock-converse'] },
      ],
      auth: { scheme: 'bearer', header: 'Authorization', token_env: 'AIPLEX_TOKEN', caller_kinds: [], steps: [] }, snippets: [], notes: [],
    }
    const groups = endpointGroupsFromConnect(spec, connect)
    const urls = (id: string) => groups.find((g) => g.id === id)!.urls.map((u) => u.url)
    expect(urls('openai-embeddings')).toEqual(['https://x/ap/ide/v1/embeddings'])
    expect(urls('openai-moderations')).toEqual(['https://x/ap/ide/v1/moderations'])
    expect(urls('openai-images')).toEqual(['https://x/ap/ide/v1/images/generations', 'https://x/ap/ide/v1/images/edits'])
    expect(urls('openai-audio')).toEqual(['https://x/ap/ide/v1/audio/transcriptions', 'https://x/ap/ide/v1/audio/translations', 'https://x/ap/ide/v1/audio/speech'])
    expect(urls('openai-realtime')).toEqual(['wss://x/ap/ide/v1/realtime?model={model}'])
    expect(urls('bedrock-converse')).toEqual(['https://x/ap/ide/model/{model}/converse-stream'])
  })

  it('renders the new groups in the endpoint list', () => {
    const connect = { access_point_id: 'ide', client: 'generic', base_urls: { openai: 'https://x/ap/ide/v1' }, endpoints: [], models: [{ name: 'e', protocols: ['openai-embeddings'] }], auth: { scheme: 'bearer', header: 'Authorization', token_env: 'AIPLEX_TOKEN', caller_kinds: [], steps: [] }, snippets: [], notes: [] } as overview.ConnectInfo
    render(<EndpointList spec={spec} surfaces={undefined} connect={connect} />)
    expect(within(screen.getByRole('group', { name: 'OpenAI Embeddings' })).getByText('https://x/ap/ide/v1/embeddings')).toBeInTheDocument()
  })
})

describe('Connect details', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.mocked(overview.getConnect).mockResolvedValue({
      access_point_id: 'ide', client: 'generic', base_urls: { openai: 'https://x/ap/ide/v1' },
      endpoints: [{ protocol: 'openai-images', transport: 'http', url: 'https://x/ap/ide/v1/images/generations', label: 'Image generation', models: ['i'] }],
      models: [], auth: { scheme: 'bearer', header: 'Authorization', token_env: 'AIPLEX_TOKEN', caller_kinds: [], steps: [] }, snippets: [], notes: [],
    })
  })

  it('lists endpoints and says SDKs may send the token as their API key but never in the URL', async () => {
    render(<MemoryRouter><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><ConnectDetails accessPointId="ide" client="generic" /></QueryClientProvider></MemoryRouter>)
    const note = await screen.findByRole('note', { name: 'API key headers' })
    expect(note).toHaveTextContent('SDKs may pass the AIPlex token as their normal API key')
    for (const header of ['x-api-key', 'x-goog-api-key', 'api-key']) expect(within(note).getByText(header)).toBeInTheDocument()
    expect(note).toHaveTextContent('Anthropic')
    expect(note).toHaveTextContent('Google')
    expect(note).toHaveTextContent('Azure')
    expect(note).toHaveTextContent('A token in a URL query parameter (such as ?key=) is rejected.')
    expect(screen.getByText('https://x/ap/ide/v1/images/generations')).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: /Copy/ })[0])
  })
})
