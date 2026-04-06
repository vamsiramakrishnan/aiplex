import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock fetch globally
const mockFetch = vi.fn();
const originalFetch = global.fetch;

describe('API Client', () => {
  beforeEach(() => {
    global.fetch = mockFetch;
    mockFetch.mockReset();
    localStorage.clear();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.resetModules();
  });

  it('adds authorization header when token exists', async () => {
    localStorage.setItem('aiplex_token', 'test-token-123');
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        total_instances: 5,
        instances_by_plane: { mcplex: 3, a2aplex: 2 },
        total_agents: 2,
        total_tool_calls: 100,
        total_a2a_delegations: 50,
        total_llm_requests: 200,
        policy_denials: 0,
        cost_usd: 12.5,
      }),
    });

    const { getDashboardStats } = await import('../api/client');
    await getDashboardStats();

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/dashboard/stats'),
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: 'Bearer test-token-123',
        }),
      })
    );
  });

  it('throws on non-2xx response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      json: () => Promise.resolve({ message: 'not found' }),
    });

    const { getDashboardStats } = await import('../api/client');
    await expect(getDashboardStats()).rejects.toThrow('not found');
  });

  it('throws with fallback message on json parse error', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: () => Promise.reject(new Error('Invalid JSON')),
    });

    const { getDashboardStats } = await import('../api/client');
    await expect(getDashboardStats()).rejects.toThrow('Internal Server Error');
  });

  it('works without auth token', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve([]),
    });

    const { listInstances } = await import('../api/client');
    await listInstances();

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/instances'),
      expect.objectContaining({
        headers: expect.not.objectContaining({
          Authorization: expect.anything(),
        }),
      })
    );
  });

  it('handles 204 No Content responses', async () => {
    localStorage.setItem('aiplex_token', 'test-token-123');
    mockFetch.mockResolvedValueOnce({
      ok: true,
      status: 204,
      json: () => Promise.reject(new Error('No content')),
    });

    const { undeployInstance } = await import('../api/client');
    const result = await undeployInstance('test-instance');

    expect(result).toBeUndefined();
  });

  it('sends POST request with correct body for deploy', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        id: 'test-instance',
        plane: 'mcplex',
        template_id: 'test-template',
        owner: 'user@example.com',
        namespace: 'mcplex',
        scopes: ['mcp:tools:test'],
        status: 'running',
        replicas: 1,
        deployed_at: '2026-04-06T10:00:00Z',
        updated_at: '2026-04-06T10:00:00Z',
        deployed_by: 'user@example.com',
      }),
    });

    const { deployInstance } = await import('../api/client');
    await deployInstance({
      plane: 'mcplex',
      template_id: 'test-template',
      display_name: 'Test Instance',
      config: { key: 'value' },
    });

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/instances'),
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          plane: 'mcplex',
          template_id: 'test-template',
          display_name: 'Test Instance',
          config: { key: 'value' },
        }),
      })
    );
  });

  it('fetches catalog with plane filter', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        templates: [],
        total: 0,
        page: 0,
        page_size: 20,
      }),
    });

    const { getCatalog } = await import('../api/client');
    await getCatalog('mcplex', 0);

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/catalog?plane=mcplex&page=0'),
      expect.anything()
    );
  });

  it('registers agent with correct payload', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        client_id: 'test-agent',
        display_name: 'Test Agent',
        auth_method: 'client_credentials',
        grant_types: ['client_credentials'],
        allowed_scopes: ['mcp:tools:test'],
        status: 'active',
        registered_at: '2026-04-06T10:00:00Z',
      }),
    });

    const { registerAgent } = await import('../api/client');
    await registerAgent({
      client_id: 'test-agent',
      display_name: 'Test Agent',
      description: 'A test agent',
      auth_method: 'client_credentials',
      grant_types: ['client_credentials'],
      allowed_scopes: ['mcp:tools:test'],
    });

    expect(mockFetch).toHaveBeenCalledWith(
      expect.stringContaining('/agents'),
      expect.objectContaining({
        method: 'POST',
        body: expect.stringContaining('test-agent'),
      })
    );
  });

  it('applies manifest sequentially', async () => {
    const deployInstanceMock = {
      ok: true,
      json: () => Promise.resolve({
        id: 'inst-1',
        plane: 'mcplex',
        template_id: 'tpl-1',
        owner: 'user@example.com',
        namespace: 'mcplex',
        scopes: [],
        status: 'running',
        replicas: 1,
        deployed_at: '2026-04-06T10:00:00Z',
        updated_at: '2026-04-06T10:00:00Z',
        deployed_by: 'user@example.com',
      }),
    };

    mockFetch
      .mockResolvedValueOnce(deployInstanceMock)
      .mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          client_id: 'agent-1',
          display_name: 'Agent 1',
          auth_method: 'client_credentials',
          grant_types: ['client_credentials'],
          allowed_scopes: [],
          status: 'active',
          registered_at: '2026-04-06T10:00:00Z',
        }),
      });

    const { applyManifest } = await import('../api/client');
    const result = await applyManifest({
      version: '1',
      instances: [{
        name: 'test-instance',
        plane: 'mcplex',
        template: 'test-template',
      }],
      agents: [{
        client_id: 'test-agent',
        display_name: 'Test Agent',
        auth_method: 'client_credentials',
        grant_types: ['client_credentials'],
        allowed_scopes: [],
      }],
    });

    expect(result.applied).toBe(2);
    expect(result.failed).toHaveLength(0);
    expect(mockFetch).toHaveBeenCalledTimes(2);
  });

  it('handles partial manifest failures', async () => {
    mockFetch
      .mockResolvedValueOnce({
        ok: false,
        status: 400,
        json: () => Promise.resolve({ message: 'Invalid template' }),
      })
      .mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          client_id: 'agent-1',
          display_name: 'Agent 1',
          auth_method: 'client_credentials',
          grant_types: ['client_credentials'],
          allowed_scopes: [],
          status: 'active',
          registered_at: '2026-04-06T10:00:00Z',
        }),
      });

    const { applyManifest } = await import('../api/client');
    const result = await applyManifest({
      version: '1',
      instances: [{
        name: 'bad-instance',
        plane: 'mcplex',
        template: 'bad-template',
      }],
      agents: [{
        client_id: 'test-agent',
        display_name: 'Test Agent',
        auth_method: 'client_credentials',
        grant_types: ['client_credentials'],
        allowed_scopes: [],
      }],
    });

    expect(result.applied).toBe(1);
    expect(result.failed).toHaveLength(1);
    expect(result.failed[0]).toContain('Invalid template');
  });
});
