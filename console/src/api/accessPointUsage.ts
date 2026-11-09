import { authenticatedRequest } from './client'
import { parseUsageJSON, filterParams, type Count, type CostTotal, type TokenCounter, type UsageFilter } from './usage'

// Usage for one access point. Same ledger and exactness rules as usage.ts;
// this module only adds the access_point_id filter and the caller_kind group
// key, which usage.ts does not model.

export type APGroupField = 'caller_kind' | 'model_id' | 'tool_name'
export interface APSummaryGroup {
  key: Partial<Record<APGroupField, string>>
  records: Count
  requests: Count
  errors: Count
  denied: Count
  unknown_status: Count
  tokens: Partial<Record<TokenCounter, Count>>
  tokens_unknown: Partial<Record<TokenCounter, Count>>
  unknown_usage_records: Count
  cost: CostTotal[]
  unknown_cost_records: Count
}
export interface APUsageSummary { group_by: APGroupField[]; groups: APSummaryGroup[]; records_scanned: Count; truncated: boolean }

export function accessPointUsageSummary(accessPointId: string, groupBy: readonly APGroupField[], filter: UsageFilter = {}) {
  const params = filterParams(filter)
  params.set('access_point_id', accessPointId)
  if (groupBy.length) params.set('group_by', groupBy.join(','))
  return authenticatedRequest<APUsageSummary>(`/api/v1/usage/summary?${params}`, undefined, async (r) => parseUsageJSON(await r.text()) as APUsageSummary)
}
