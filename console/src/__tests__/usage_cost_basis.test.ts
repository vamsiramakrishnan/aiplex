import { describe, expect, it } from 'vitest'
import * as api from '../api/usage'

describe('cost bases', () => {
  it('formats reported and backfilled estimated costs with their amount, and unknown as unknown', () => {
    expect(api.formatCost({ basis: 'reported', currency: 'USD', amount: '12346', scale: 6 })).toBe('USD 0.012346')
    expect(api.formatCost({ basis: 'estimated', currency: 'USD', amount: '10', scale: 6, exact: '37/4', price_id: 'p', backfilled_at: '2026-11-10T00:00:00Z' })).toBe('USD 0.000010')
    expect(api.formatCost({ basis: 'unknown' })).toBe('unknown')
  })
})
