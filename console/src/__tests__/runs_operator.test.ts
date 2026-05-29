import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

const mockFetch = vi.fn();
const originalFetch = global.fetch;

describe('Runs operator API client (PR 11)', () => {
  beforeEach(() => {
    global.fetch = mockFetch;
    mockFetch.mockReset();
    localStorage.clear();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.resetModules();
  });

  it('runRedrive POSTs with an Idempotency-Key header', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ accepted: true }) });
    const { runRedrive } = await import('../api/client');
    await runRedrive('run-1');

    const [, init] = mockFetch.mock.calls[0];
    expect(init.method).toBe('POST');
    const hdrs = init.headers as Record<string, string>;
    expect(hdrs['Idempotency-Key']).toMatch(/^run-1\|redrive\|\d+$/);
  });

  it('runCancel sends reason in body', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({}) });
    const { runCancel } = await import('../api/client');
    await runCancel('run-2', 'end of demo');

    const [, init] = mockFetch.mock.calls[0];
    const body = JSON.parse(init.body as string);
    expect(body).toEqual({ reason: 'end of demo' });
  });

  it('runSignal sends gate_name and resolution_json', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({}) });
    const { runSignal } = await import('../api/client');
    await runSignal('run-3', 'approval', '{"ok":true}');

    const [url, init] = mockFetch.mock.calls[0];
    expect(url).toContain('/runs/run-3/signal');
    const body = JSON.parse(init.body as string);
    expect(body).toEqual({ gate_name: 'approval', resolution_json: '{"ok":true}' });
  });

  it('listOperatorAudit GETs /operator-audit', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ audit: [] }) });
    const { listOperatorAudit } = await import('../api/client');
    await listOperatorAudit('run-4');
    expect(mockFetch.mock.calls[0][0]).toContain('/runs/run-4/operator-audit');
  });

  it('getRunsHealth GETs /runs/_health', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        last_ingest_at: '2026-01-01T00:00:00Z',
        has_runs: false,
        tape_instances_count: 0,
        now: '2026-01-01T00:00:00Z',
      }),
    });
    const { getRunsHealth } = await import('../api/client');
    const h = await getRunsHealth();
    expect(h.tape_instances_count).toBe(0);
    expect(mockFetch.mock.calls[0][0]).toContain('/runs/_health');
  });

  it('runReconcile and runCompensate target their dedicated subroutes', async () => {
    mockFetch
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({}) })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({}) });
    const c = await import('../api/client');
    await c.runReconcile('r-x');
    await c.runCompensate('r-y');
    expect(mockFetch.mock.calls[0][0]).toContain('/runs/r-x/reconcile');
    expect(mockFetch.mock.calls[1][0]).toContain('/runs/r-y/compensate');
  });
});
