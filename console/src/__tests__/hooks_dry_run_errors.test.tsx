import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Hooks from '../pages/Hooks'
import { accessFrom } from '../hooks/useAccess'

// The real api/client runs here; only fetch is replaced.
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ user: null }), useOptionalAuth: () => null }))
vi.mock('../hooks/useAccess', async (orig) => ({ ...(await orig<typeof import('../hooks/useAccess')>()), useAccess: () => accessFrom(['deployer']) }))

const SAMPLE = 'SECRET-sample-7731'
const policies = JSON.stringify([{ id: 'p', revision: 1, spec: { id: 'p', scope: { kind: 'tenant' }, hooks: [] } }])
let testReply: () => Promise<Response>
let calls: { url: string; body: string }[]

beforeEach(() => {
  calls = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    if (url.endsWith('/test')) { calls.push({ url, body: String(init?.body) }); return testReply() }
    return new Response(policies, { status: 200 })
  }))
})
afterEach(() => { vi.unstubAllGlobals() })

async function runDry() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><Hooks /></QueryClientProvider>)
  await screen.findByLabelText('Edit p')
  fireEvent.change(screen.getByLabelText('Policy'), { target: { value: 'p' } })
  fireEvent.change(screen.getByLabelText('Sample text'), { target: { value: SAMPLE } })
  fireEvent.click(screen.getByText('Run test'))
  const alert = await screen.findByText(/Test failed/)
  await waitFor(() => expect(calls).toHaveLength(1))
  expect(calls[0]!.url).not.toContain('SECRET')
  expect(calls[0]!.body).toContain(SAMPLE)
  // The person's own text box still holds what they typed; nothing else on the page may.
  const rest = view.container.cloneNode(true) as HTMLElement
  rest.querySelectorAll('textarea').forEach((t) => t.remove())
  expect(rest.textContent).not.toContain('SECRET-sample')
  return alert
}

describe('a failed dry run renders no part of the sample', () => {
  it('500 with a JSON error body', async () => {
    testReply = async () => new Response(JSON.stringify({ code: 'TEST_FAILED', message: 'the transformed sample could not be read' }), { status: 500 })
    expect(await runDry()).toHaveTextContent('the transformed sample could not be read')
  })
  it('500 with a body that is not JSON', async () => {
    testReply = async () => new Response('<html>oops</html>', { status: 500, statusText: 'Internal Server Error' })
    expect(await runDry()).toHaveTextContent('Internal Server Error')
  })
  it('network failure', async () => {
    testReply = async () => { throw new TypeError('Failed to fetch') }
    expect(await runDry()).toHaveTextContent('Failed to fetch')
  })
})
