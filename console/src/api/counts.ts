import { UNKNOWN } from './usage'

/** A tab label with its count; the count is omitted until the data has loaded, never shown as 0. */
export const tabLabel = (label: string, count: number | null | undefined): string =>
  count === null || count === undefined ? label : `${label} (${count})`

/** "3 tools"; a count the server did not send is unknown, a real 0 stays "0 tools". */
export const countOf = (items: readonly unknown[] | null | undefined, noun: string, plural = `${noun}s`): string =>
  items === null || items === undefined ? `${UNKNOWN} ${plural}` : `${items.length} ${items.length === 1 ? noun : plural}`
