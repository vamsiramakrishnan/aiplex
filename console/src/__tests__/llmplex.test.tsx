import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LLMPlex from '../pages/LLMPlex'
import { authenticatedRequest, getCatalog, listInstances, putLLMRoute } from '../api/client'
import { PROVIDER_IDS, PROVIDER_LABELS } from '../api/modelSurfaces'

vi.mock('../api/client', () => ({ authenticatedRequest: vi.fn(), getCatalog: vi.fn(), listInstances: vi.fn(), putLLMRoute: vi.fn(), deleteLLMRoute: vi.fn() }))
vi.mock('../components/ModelSurfacesPanel', () => ({ default: () => null }))
vi.mock('../components/OpenBatches', () => ({ default: () => null }))
vi.mock('../components/LedgerCosts', () => ({ default: () => null }))

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<MemoryRouter><QueryClientProvider client={client}><LLMPlex /></QueryClientProvider></MemoryRouter>)
}

async function openNewRoute() {
  fireEvent.click(screen.getByRole('button', { name: 'routes' }))
  fireEvent.click(await screen.findByRole('button', { name: '+ New Route' }))
  fireEvent.change(screen.getByLabelText('Model ID', { selector: 'input[placeholder^="e.g."]' }), { target: { value: 'my-model' } })
  fireEvent.click(screen.getByRole('button', { name: '+ Add Backend' }))
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getCatalog).mockResolvedValue({ templates: [], total: 0, page: 0, page_size: 20 } as never)
  vi.mocked(listInstances).mockResolvedValue([{ id: 'llm-1', display_name: 'Chat model', template_id: 'tpl', status: 'running' }] as never)
  vi.mocked(authenticatedRequest).mockResolvedValue([])
  vi.mocked(putLLMRoute).mockResolvedValue({} as never)
})

describe('LLMPlex providers tab', () => {
  it('labels the instance list as instances and links each to its detail page', async () => {
    show()
    expect(await screen.findByRole('heading', { name: 'Deployed LLM instances' })).toBeInTheDocument()
    expect(screen.queryByText('Active Model Routes')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Chat model' })).toHaveAttribute('href', '/instances/llm-1')
  })
})

describe('LLMPlex route form', () => {
  it('offers exactly the shared provider list', async () => {
    show(); await openNewRoute()
    const options = Array.from((screen.getByLabelText('Provider') as HTMLSelectElement).options).map(o => o.value)
    expect(options).toEqual([...PROVIDER_IDS])
    expect(options).toEqual(expect.arrayContaining(['azure-openai', 'openai-compatible', 'gemini-developer']))
    expect(screen.getByRole('option', { name: PROVIDER_LABELS['azure-openai'] })).toBeInTheDocument()
  })

  it('collects fallbacks in an inline input and never calls window.prompt', async () => {
    const prompt = vi.spyOn(window, 'prompt').mockReturnValue('from-prompt')
    show(); await openNewRoute()
    fireEvent.change(screen.getByLabelText('Fallback model ID'), { target: { value: 'backup-model' } })
    fireEvent.click(screen.getByRole('button', { name: '+ Add Fallback' }))
    expect(screen.getByText('backup-model')).toBeInTheDocument()
    expect(screen.getByLabelText('Fallback model ID')).toHaveValue('')
    fireEvent.change(screen.getByPlaceholderText('Model ID'), { target: { value: 'upstream' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Route' }))
    await waitFor(() => expect(putLLMRoute).toHaveBeenCalled())
    expect(vi.mocked(putLLMRoute).mock.calls[0][1]).toMatchObject({ fallbacks: ['backup-model'] })
    expect(prompt).not.toHaveBeenCalled()
  })

  it('ignores an empty fallback', async () => {
    show(); await openNewRoute()
    fireEvent.click(screen.getByRole('button', { name: '+ Add Fallback' }))
    fireEvent.change(screen.getByPlaceholderText('Model ID'), { target: { value: 'upstream' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Route' }))
    await waitFor(() => expect(putLLMRoute).toHaveBeenCalled())
    expect(vi.mocked(putLLMRoute).mock.calls[0][1].fallbacks).toBeUndefined()
  })

  it('refuses a budget of 0 with a message instead of silently dropping it', async () => {
    show(); await openNewRoute()
    fireEvent.change(screen.getByPlaceholderText('Model ID'), { target: { value: 'upstream' } })
    fireEvent.change(screen.getByLabelText('Max Daily Cost (USD)'), { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Route' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Max daily cost must be greater than 0')
    expect(putLLMRoute).not.toHaveBeenCalled()
  })

  it('sends no budget when the cost is left empty', async () => {
    show(); await openNewRoute()
    fireEvent.change(screen.getByPlaceholderText('Model ID'), { target: { value: 'upstream' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Route' }))
    await waitFor(() => expect(putLLMRoute).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putLLMRoute).mock.calls[0][1].budget).toBeUndefined()
  })

  it('sends a positive budget', async () => {
    show(); await openNewRoute()
    fireEvent.change(screen.getByPlaceholderText('Model ID'), { target: { value: 'upstream' } })
    fireEvent.change(screen.getByLabelText('Max Daily Cost (USD)'), { target: { value: '12.5' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save Route' }))
    await waitFor(() => expect(putLLMRoute).toHaveBeenCalledTimes(1))
    expect(vi.mocked(putLLMRoute).mock.calls[0][1].budget).toEqual({ max_daily_cost_usd: 12.5, alert_threshold_pct: 80 })
  })
})

describe('LLMPlex routing rules and prices: unknown is never zero', () => {
  it('says "no daily cap" for a budget without a daily cost, and shows the caps it does carry', async () => {
    vi.mocked(authenticatedRequest).mockResolvedValue([
      { model_id: 'no-budget', backends: [] },
      { model_id: 'monthly-only', backends: [], budget: { max_monthly_cost_usd: 300 } },
      { model_id: 'daily', backends: [], budget: { max_daily_cost_usd: 5, alert_threshold_pct: 80 } },
    ])
    show()
    fireEvent.click(screen.getByRole('button', { name: 'routes' }))
    await screen.findByText('no-budget')
    expect(screen.getAllByText(/Budget:/)).toHaveLength(2)
    expect(screen.getByText(/Budget: no daily cap, \$300\/month/)).toBeInTheDocument()
    expect(screen.getByText(/Budget: \$5\/day \(alert at 80%\)/)).toBeInTheDocument()
    expect(screen.queryByText(/\$\/day/)).not.toBeInTheDocument()
  })

  it('shows unknown for a missing backend weight and 0% for a real zero', async () => {
    vi.mocked(authenticatedRequest).mockResolvedValue([
      { model_id: 'm', backends: [{ provider: 'openai', model_id: 'a', enabled: true }, { provider: 'anthropic', model_id: 'b', weight: 0, enabled: true }, { provider: 'bedrock', model_id: 'c', weight: 40, enabled: true }] },
    ])
    show()
    fireEvent.click(screen.getByRole('button', { name: 'routes' }))
    const rowOf = async (model: string) => (await screen.findByText(model)).closest('div.flex') as HTMLElement
    expect((await rowOf('a')).textContent).toMatch(/unknown$/)
    expect((await rowOf('b')).textContent).toMatch(/0%$/)
    expect((await rowOf('c')).textContent).toMatch(/40%$/)
  })

  it('shows unknown for a missing model price and $0 for a real zero', async () => {
    vi.mocked(getCatalog).mockResolvedValue({ templates: [
      { id: 'x', name: 'Missing price', provider: 'p', description: 'd', pricing: { output: 3 } },
      { id: 'y', name: 'Free', provider: 'p', description: 'd', pricing: { input: 0, output: 0 } },
    ], total: 2, page: 0, page_size: 20 } as never)
    show()
    expect((await screen.findByText('Missing price')).closest('div.bg-white')!.textContent).toContain('unknown/M input · $3/M output')
    expect((await screen.findByText('Free')).closest('div.bg-white')!.textContent).toContain('$0/M input · $0/M output')
  })
})
