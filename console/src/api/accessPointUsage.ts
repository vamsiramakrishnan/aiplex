import { usageSummary, type GroupField, type SummaryGroup, type UsageFilter, type UsageSummary } from './usage'

// Usage of one access point: the ledger summary filtered by access_point_id.
// usage.ts owns the exact decoding and the access_point_id / caller_kind keys.

export type APGroupField = Extract<GroupField, 'caller_kind' | 'model_id' | 'tool_name'>
export type APSummaryGroup = SummaryGroup
export type APUsageSummary = UsageSummary

export const accessPointUsageSummary = (accessPointId: string, groupBy: readonly APGroupField[], filter: UsageFilter = {}) =>
  usageSummary({ ...filter, access_point_id: accessPointId }, groupBy)
