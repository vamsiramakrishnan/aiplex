import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { YourAccessPoints } from '../components/YourAccess'
import ConnectWizard from '../components/ConnectWizard'
import * as overview from '../api/overview'
import * as accessPoints from '../api/accessPoints'

vi.mock('../api/overview', async (original) => ({
  ...(await original<typeof import('../api/overview')>()),
  getConnect: vi.fn(),
  listClientProfiles: vi.fn(),
}))
vi.mock('../api/accessPoints', async (original) => ({ ...(await original<typeof import('../api/accessPoints')>()), listAccessPointViews: vi.fn() }))

const connect: overview.ConnectInfo = {
  access_point_id: 'local', client: 'generic',
  base_urls: { openai: 'http://127.0.0.1:8080/ap/local/v1' }, models: [{ name: 'mock-gpt', protocols: ['openai-chat-completions'] }],
  auth: { scheme: 'bearer', header: 'Authorization: Bearer $AIPLEX_TOKEN', token_env: 'AIPLEX_TOKEN', caller_kinds: ['user'], steps: ['Send it on every request'] },
  snippets: [{ title: 'curl: OpenAI chat completions', language: 'bash', content: 'curl -sS http://127.0.0.1:8080/ap/local/v1/chat/completions' }], notes: [],
}

const wrap = (ui: React.ReactElement) => render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter>{ui}</MemoryRouter></QueryClientProvider>)

beforeEach(() => {
  vi.mocked(overview.getConnect).mockReset().mockResolvedValue(connect)
  vi.mocked(overview.listClientProfiles).mockReset().mockResolvedValue([
    { id: 'claude-code', name: 'Claude Code', surface: 'cli', modes: ['gateway'], protocols: ['anthropic-messages', 'claude-apps'] },
    { id: 'claude-desktop', name: 'Claude Desktop', surface: 'desktop', modes: ['claude-apps-gateway'], protocols: ['claude-apps'] },
  ])
  vi.mocked(accessPoints.listAccessPointViews).mockReset().mockResolvedValue([{ id: 'local', display_name: 'Local development', endpoints: [{ kind: 'models', url: 'http://127.0.0.1:8080/ap/local/v1/models' }] }] as never)
})

describe('Home for a caller without a role', () => {
  it('opens the real Connect wizard, which asks the server for the connect response', async () => {
    wrap(<YourAccessPoints />)
    fireEvent.click(await screen.findByRole('button', { name: 'Connect a client to local' }))
    const dialog = await screen.findByRole('dialog', { name: 'Connect a client to local' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Show connection details' }))
    await waitFor(() => expect(overview.getConnect).toHaveBeenCalledWith('local', 'generic'))
    expect(await within(dialog).findByText('http://127.0.0.1:8080/ap/local/v1')).toBeInTheDocument()
  })
})

describe('connect wizard profile list', () => {
  it('does not offer a profile that only speaks the Claude apps gateway protocol', async () => {
    wrap(<ConnectWizard accessPointId="local" onClose={() => {}} />)
    const select = await screen.findByLabelText('Client')
    await waitFor(() => expect(within(select).getAllByRole('option').map(o => o.textContent)).toEqual([
      'Generic (any OpenAI, Anthropic, Google or MCP client)', 'Claude Code (anthropic-messages, claude-apps)',
    ]))
  })
})
