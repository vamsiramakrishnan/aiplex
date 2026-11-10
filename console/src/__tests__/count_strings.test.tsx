import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { countNoun, countVerb } from '../api/usage'
import { UsageHeadline } from '../components/UsageSummaryTable'

describe('count strings', () => {
  it('pluralises one helper for every count', () => {
    expect(countNoun(0, 'record')).toBe('0 records')
    expect(countNoun(1, 'record')).toBe('1 record')
    expect(countNoun('1', 'record')).toBe('1 record')
    expect(countNoun('1234', 'record')).toBe('1,234 records')
    expect(countNoun(2, 'batch record')).toBe('2 batch records')
    expect(countNoun(1, 'child', 'children')).toBe('1 child')
    expect(countNoun(3, 'child', 'children')).toBe('3 children')
    // Unknown is never a number.
    expect(countNoun(undefined, 'record')).toBe('unknown records')
    expect(countVerb(1)).toBe('has')
    expect(countVerb('1')).toBe('has')
    expect(countVerb(2)).toBe('have')
  })

  it('reads "1 record has unknown cost" in the usage headline', () => {
    const group = { key: {}, requests: '3', errors: '0', denied: '0', unknown_usage_records: '0', unknown_cost_records: '1', cost: [], tokens: {}, tokens_unknown: {} } as never
    render(<UsageHeadline groups={[group]} label="Totals" />)
    expect(screen.getByText(/1 record has unknown cost/)).toBeInTheDocument()
  })
})
