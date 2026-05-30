import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Reuse the existing pattern from client.test.ts — mock global fetch
// and import the client lazily so the per-test mock takes effect.
const mockFetch = vi.fn();
const originalFetch = global.fetch;

describe('Runs API client (AIPlex integration PR 8)', () => {
  beforeEach(() => {
    global.fetch = mockFetch;
    mockFetch.mockReset();
    localStorage.clear();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.resetModules();
  });

  it('listRuns hits /api/v1/runs and decodes the response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        runs: [{
          run_id: 'r1', tenant_id: 'acme', agent_id: 'treasury',
          plane: 'a2aplex', actor: 'spiffe://test', subject: 'u@e.com',
          status: 'terminal', started_at: '2026-01-01T00:00:00Z',
          decisions_count: 1, effects_count: 2, unknown_effects: 0,
          obligations: 0, policy_violations: 0, budget_usd_charged: 0,
        }],
      }),
    });

    const { listRuns } = await import('../api/client');
    const resp = await listRuns();

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/runs'),
      expect.any(Object),
    );
    expect(resp.runs).toHaveLength(1);
    expect(resp.runs[0].run_id).toBe('r1');
  });

  it('listRuns serialises filter params into the query string', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ runs: [] }) });

    const { listRuns } = await import('../api/client');
    await listRuns({
      tenant_id: 'acme',
      agent_id: 'treasury',
      has_unknown_effects: true,
      has_obligations: true,
      limit: 50,
    });

    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('tenant_id=acme');
    expect(url).toContain('agent_id=treasury');
    expect(url).toContain('has_unknown_effects=true');
    expect(url).toContain('has_obligations=true');
    expect(url).toContain('limit=50');
  });

  it('listRunEvents threads from_seq + limit', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ events: [] }) });

    const { listRunEvents } = await import('../api/client');
    await listRunEvents('run-x', 5, 100);

    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain('/api/v1/runs/run-x/events');
    expect(url).toContain('from_seq=5');
    expect(url).toContain('limit=100');
  });

  it('getRun hits /api/v1/runs/{id} and bubbles HTTP errors', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      statusText: 'Not Found',
      json: () => Promise.resolve({ message: 'not found' }),
    });

    const { getRun } = await import('../api/client');
    await expect(getRun('missing')).rejects.toThrow('not found');
    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/runs/missing'),
      expect.any(Object),
    );
  });

  it('listRunEffects/Obligations/Budgets each call their dedicated subroute', async () => {
    // Three back-to-back calls, each with its own response.
    const mk = (key: string) => ({
      ok: true,
      json: () => Promise.resolve({ [key]: [] }),
    });
    mockFetch
      .mockResolvedValueOnce(mk('effects'))
      .mockResolvedValueOnce(mk('obligations'))
      .mockResolvedValueOnce(mk('budgets'));

    const c = await import('../api/client');
    await c.listRunEffects('r-1');
    await c.listRunObligations('r-1');
    await c.listRunBudgets('r-1');

    const urls = mockFetch.mock.calls.map(c => c[0]);
    expect(urls[0]).toContain('/api/v1/runs/r-1/effects');
    expect(urls[1]).toContain('/api/v1/runs/r-1/obligations');
    expect(urls[2]).toContain('/api/v1/runs/r-1/budgets');
  });

  it('URL-encodes special characters in run_id', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({}) });

    const { getRun } = await import('../api/client');
    await getRun('runs/with/slashes');

    const url = mockFetch.mock.calls[0][0] as string;
    // encodeURIComponent turns / into %2F so the path doesn't fan out.
    expect(url).toContain('runs%2Fwith%2Fslashes');
  });
});
