import { describe, expect, it } from 'vitest'
import { firstTabWithRows } from '../api/overview'

describe('Overview default tab', () => {
  it('picks Users, then Agents, then Clients, whichever has rows first', () => {
    expect(firstTabWithRows({ user: 2, agent: 5, client: 1 })).toBe('user')
    expect(firstTabWithRows({ user: 0, agent: 5, client: 1 })).toBe('agent')
    expect(firstTabWithRows({ user: 0, agent: 0, client: 1 })).toBe('client')
  })

  it('waits while a higher-priority kind is still loading, so the page never flashes the wrong tab', () => {
    expect(firstTabWithRows({})).toBeNull()
    expect(firstTabWithRows({ agent: 5, client: 1 })).toBeNull()
    expect(firstTabWithRows({ user: 0, client: 3 })).toBeNull()
    // A kind that already has rows decides without waiting for the later ones.
    expect(firstTabWithRows({ user: 3 })).toBe('user')
    expect(firstTabWithRows({ user: 0, agent: 2 })).toBe('agent')
  })

  it('falls back to Users when nothing has rows', () => {
    expect(firstTabWithRows({ user: 0, agent: 0, client: 0 })).toBe('user')
  })
})
