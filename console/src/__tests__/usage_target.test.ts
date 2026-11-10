import { describe, expect, it } from 'vitest'
import { keyText, recordTarget } from '../pages/Usage'

describe('usage record target and group labels (same rules as the CLI)', () => {
  it('names the tool called, or the connection and what was done for discovery', () => {
    expect(recordTarget({ model_id: 'mock-gpt', resource_id: 'local-models' })).toBe('mock-gpt')
    expect(recordTarget({ tool_name: 'add', resource_id: 'local-tools', operation: 'tools/call' })).toBe('add')
    expect(recordTarget({ resource_id: 'local-tools', operation: 'tools/list' })).toBe('local-tools (list)')
    expect(recordTarget({ resource_id: 'local-tools', operation: 'initialize' })).toBe('local-tools (initialize)')
    expect(recordTarget({ resource_id: 'local-tools', operation: 'tools/call' })).toBe('local-tools')
    expect(recordTarget({})).toBe('—')
  })

  it('labels the group of requests that name no model or no tool plainly', () => {
    expect(keyText('model_id', '')).toBe('(no model)')
    expect(keyText('model_id', undefined)).toBe('(no model)')
    expect(keyText('tool_name', '')).toBe('(no tool)')
    expect(keyText('model_id', 'mock-gpt')).toBe('mock-gpt')
    expect(keyText('access_point_id', '')).toBe('(none)')
  })
})
