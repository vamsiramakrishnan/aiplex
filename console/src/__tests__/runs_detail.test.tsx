import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import Runs, { detailRefetchInterval } from '../pages/Runs'
import {
  listOperatorAudit,
  listRunBudgets,
  listRunEffects,
  listRunObligations,
  listRuns,
  runCancel,
  type ExecutionEvent,
  type ExecutionRun,
} from '../api/client'

vi.mock('../hooks/useRunTimelineSSE', () => ({ useRunTimelineSSE: () => ({ events: [], live: false }) }))
vi.mock('../api/client', () => ({
  listRuns: vi.fn(),
  listOperatorAudit: vi.fn(),
  listRunEffects: vi.fn(),
  listRunObligations: vi.fn(),
  listRunBudgets: vi.fn(),
  getRunsHealth: vi.fn(),
  runRedrive: vi.fn(),
  runReconcile: vi.fn(),
  runSignal: vi.fn(),
  runCancel: vi.fn(),
  runCompensate: vi.fn(),
}))

const run: ExecutionRun = {
  run_id: 'run-1', tenant_id: 'acme', agent_id: 'treasury', plane: 'a2aplex', actor: 'spiffe://x', subject: 'u@example.com',
  status: 'terminal', started_at: '2026-05-01T10:00:00Z', decisions_count: 1, effects_count: 3, unknown_effects: 2,
  obligations: 0, policy_violations: 0, budget_usd_charged: 0, compacted: false,
}
const ev = (kind: string, seq: number, extra: Partial<ExecutionEvent> = {}): ExecutionEvent => ({
  run_id: 'run-1', seq, tenant_id: 'acme', agent_id: 'treasury', plane: 'a2aplex', actor: 'a', subject: 's',
  kind, timestamp: '2026-05-01T10:00:00Z', ...extra,
} as ExecutionEvent)

function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(<QueryClientProvider client={client}><Runs /></QueryClientProvider>)
}
async function openRun() { fireEvent.click(await screen.findByText('run-1')) }
const section = (title: string) => screen.getByRole('heading', { name: title }).parentElement as HTMLElement

beforeEach(() => {
  vi.clearAllMocks()
  vi.restoreAllMocks()
  vi.mocked(listRuns).mockResolvedValue({ runs: [run] })
  vi.mocked(listOperatorAudit).mockResolvedValue({ audit: [] })
  vi.mocked(listRunEffects).mockResolvedValue({ effects: [] })
  vi.mocked(listRunObligations).mockResolvedValue({ obligations: [] })
  vi.mocked(listRunBudgets).mockResolvedValue({ budgets: [] })
  vi.mocked(runCancel).mockResolvedValue(undefined as never)
})

describe('Run detail: effects', () => {
  it('labels an effect whose outcome is unknown as unknown, and counts it', async () => {
    vi.mocked(listRunEffects).mockResolvedValue({ effects: [
      ev('effect.confirmed', 1, { tool: 'wire', scope: 'payments:write' }),
      ev('effect.unknown', 2, { tool: 'pay', scope: 'payments:write' }),
      ev('', 3, { tool: 'refund' }),
    ] })
    show(); await openRun()
    const effects = section('Effects')
    expect(await within(effects).findByText('confirmed')).toBeInTheDocument()
    expect(within(within(effects).getByText('pay').closest('li') as HTMLElement).getByText('unknown')).toBeInTheDocument()
    expect(within(within(effects).getByText('refund').closest('li') as HTMLElement).getByText('unknown')).toBeInTheDocument()
    expect(within(effects).getByText(/3 effect\(s\), 2 with unknown outcome/)).toBeInTheDocument()
  })

  it('shows loading and empty states for its own section', async () => {
    vi.mocked(listRunEffects).mockReturnValue(new Promise(() => {}))
    show(); await openRun()
    expect(await screen.findByText('Loading effects…')).toBeInTheDocument()
  })
})

describe('Run detail: sections load and fail independently', () => {
  it('shows an error in the failed section while the other sections still show their data', async () => {
    vi.mocked(listRunObligations).mockRejectedValue(new Error('boom'))
    vi.mocked(listRunEffects).mockResolvedValue({ effects: [ev('effect.confirmed', 1, { tool: 'wire' })] })
    show(); await openRun()
    expect(await within(section('Obligations')).findByText('Could not load obligations: boom')).toBeInTheDocument()
    expect(within(section('Obligations')).queryByText(/No obligations recorded/)).not.toBeInTheDocument()
    expect(await within(section('Effects')).findByText('wire')).toBeInTheDocument()
    expect(within(section('Budget charges')).getByText('No budget charges recorded on this run; no total.')).toBeInTheDocument()
  })

  it('shows empty states for effects and obligations', async () => {
    show(); await openRun()
    expect(await within(section('Effects')).findByText('No effects recorded for this run.')).toBeInTheDocument()
    expect(await within(section('Obligations')).findByText('No obligations recorded for this run.')).toBeInTheDocument()
  })

  it('shows an error for budgets without hiding the effects', async () => {
    vi.mocked(listRunBudgets).mockRejectedValue(new Error('upstream down'))
    show(); await openRun()
    expect(await within(section('Budget charges')).findByText('Could not load budget charges: upstream down')).toBeInTheDocument()
    expect(within(section('Budget charges')).queryByText(/Total charged/)).not.toBeInTheDocument()
  })
})

describe('Run detail: budget charges', () => {
  it('shows a charge with no amount as unknown, never $0.00, and says the total leaves it out', async () => {
    vi.mocked(listRunBudgets).mockResolvedValue({ budgets: [
      ev('budget.charged', 1, { payload_json: '{"usd_charged":1.25}' }),
      ev('budget.charged', 2),
    ] })
    show(); await openRun()
    const budgets = section('Budget charges')
    expect(await within(budgets).findByText('$1.25')).toBeInTheDocument()
    expect(within(budgets).getByText('unknown')).toBeInTheDocument()
    expect(within(budgets).queryByText('$0.00')).not.toBeInTheDocument()
    expect(within(budgets).getByText('Total charged: $1.25 (excludes 1 charge(s) with unknown amount)')).toBeInTheDocument()
  })

  it('shows the total as unknown when every charge has an unknown amount', async () => {
    vi.mocked(listRunBudgets).mockResolvedValue({ budgets: [
      ev('budget.charged', 1),
      ev('budget.charged', 2, { payload_json: 'not json' }),
    ] })
    show(); await openRun()
    const budgets = section('Budget charges')
    expect(await within(budgets).findByText('Total charged: unknown (2 charge(s) with unknown amount)')).toBeInTheDocument()
    expect(within(budgets).queryByText(/\$/)).not.toBeInTheDocument()
  })

  it('says a run with no charges has no total', async () => {
    show(); await openRun()
    const budgets = section('Budget charges')
    expect(await within(budgets).findByText('No budget charges recorded on this run; no total.')).toBeInTheDocument()
    expect(within(budgets).queryByText(/Total charged/)).not.toBeInTheDocument()
  })

  it('shows a real total of zero from known charges as $0.00', async () => {
    vi.mocked(listRunBudgets).mockResolvedValue({ budgets: [ev('budget.charged', 1, { payload_json: '{"usd_charged":0}' })] })
    show(); await openRun()
    const budgets = section('Budget charges')
    expect(await within(budgets).findByText('Total charged: $0.00')).toBeInTheDocument()
    expect(within(budgets).getByText('$0.00', { selector: 'span' })).toBeInTheDocument()
  })
})

describe('Run detail: compacted runs', () => {
  it('makes no request to the effect, obligation or budget routes and says the detail was archived', async () => {
    vi.mocked(listRuns).mockResolvedValue({ runs: [{ ...run, compacted: true, compacted_at: '2026-04-01T00:00:00Z', effects_count: 3, unknown_effects: 1, obligations: 2 }] })
    show(); await openRun()
    expect(await within(section('Effects')).findByText(/Effect detail was archived.*counts 3 effect\(s\), 1 with unknown outcome/)).toBeInTheDocument()
    expect(within(section('Obligations')).getByText(/Obligation detail was archived.*counts 2 obligation\(s\)/)).toBeInTheDocument()
    expect(within(section('Budget charges')).getByText(/Charge detail was archived/)).toBeInTheDocument()
    expect(screen.queryByText(/No (effects|obligations|budget charges) recorded/)).not.toBeInTheDocument()
    expect(listRunEffects).not.toHaveBeenCalled()
    expect(listRunObligations).not.toHaveBeenCalled()
    expect(listRunBudgets).not.toHaveBeenCalled()
  })
})

describe('Run detail: refetch', () => {
  it('polls only while the run can still change', () => {
    const base = { ...run }
    expect(detailRefetchInterval({ ...base, status: 'running' }, false)).toBe(5_000)
    expect(detailRefetchInterval({ ...base, status: 'failed' }, false)).toBe(5_000)
    expect(detailRefetchInterval(null, false)).toBe(5_000)
    expect(detailRefetchInterval({ ...base, status: 'terminal' }, false)).toBe(false)
    expect(detailRefetchInterval({ ...base, status: 'running', compacted: true }, true)).toBe(false)
  })
})

describe('Run detail: budget amount types', () => {
  it.each([
    { payload: '{"usd_charged":2}', shown: '$2.00' },
    { payload: '{"usd_charged":0}', shown: '$0.00' },
  ])('reads a JSON number amount $shown', async ({ payload, shown }) => {
    vi.mocked(listRunBudgets).mockResolvedValue({ budgets: [ev('budget.charged', 1, { payload_json: payload })] })
    show(); await openRun()
    expect(await within(section('Budget charges')).findByText(shown)).toBeInTheDocument()
  })

  it.each([
    { label: 'a numeric string', payload: '{"usd_charged":"1.25"}' },
    { label: 'null', payload: '{"usd_charged":null}' },
    { label: 'a boolean', payload: '{"usd_charged":true}' },
    { label: 'a missing key', payload: '{"other":1}' },
  ])('treats $label as unknown, never $0.00', async ({ payload }) => {
    vi.mocked(listRunBudgets).mockResolvedValue({ budgets: [ev('budget.charged', 1, { payload_json: payload })] })
    show(); await openRun()
    const budgets = section('Budget charges')
    expect(await within(budgets).findByText('Total charged: unknown (1 charge(s) with unknown amount)')).toBeInTheDocument()
    expect(within(budgets).queryByText(/\$/)).not.toBeInTheDocument()
  })
})

describe('Run detail: cancel reason', () => {
  it('collects the cancel reason in an inline input and never calls window.prompt', async () => {
    const prompt = vi.spyOn(window, 'prompt').mockReturnValue('from-prompt')
    show(); await openRun()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.change(screen.getByLabelText('Cancel reason'), { target: { value: 'user requested' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send cancel' }))
    await waitFor(() => expect(runCancel).toHaveBeenCalledWith('run-1', 'user requested'))
    expect(prompt).not.toHaveBeenCalled()
  })

  it('dismisses the cancel form without cancelling the run', async () => {
    const prompt = vi.spyOn(window, 'prompt').mockReturnValue(null)
    show(); await openRun()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getByRole('button', { name: 'Keep run' }))
    expect(screen.queryByLabelText('Cancel reason')).not.toBeInTheDocument()
    expect(runCancel).not.toHaveBeenCalled()
    expect(prompt).not.toHaveBeenCalled()
  })
})
