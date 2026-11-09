import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { quotaOperationKey, quotaPolicy, safeQuotaJSON } from '../api/quota'
beforeEach(() => { sessionStorage.clear(); vi.stubGlobal('crypto', webcrypto) })
afterEach(() => vi.unstubAllGlobals())
it('rejects lossy quota numeric lexemes before parsing input and response bodies', async () => {
  for (const n of ['1e-400', '9007199254740990.5', '9007199254740993', '-9007199254740993', '1e3']) expect(() => safeQuotaJSON(`{"usage":{"requests":${n}}}`)).toThrow(/exact/)
  expect(safeQuotaJSON('{"requests":42,"note":"1e-400"}')).toEqual({ requests: 42, note: '1e-400' })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{"limit":9007199254740990.5}', { status: 200 })))
  await expect(quotaPolicy()).rejects.toThrow(/exact/)
})
it('retains operation IDs after uncertainty and separates owner and changed payload', async () => {
  const id = await quotaOperationKey('tenant/principal/actor-a', 'loan', { amount: 7 })
  expect(await quotaOperationKey('tenant/principal/actor-a', 'loan', { amount: 7 })).toBe(id)
  expect(await quotaOperationKey('tenant/principal/actor-b', 'loan', { amount: 7 })).not.toBe(id)
  expect(await quotaOperationKey('tenant/principal/actor-a', 'loan', { amount: 8 })).not.toBe(id)
  expect(Object.keys(sessionStorage).join('')).not.toContain('principal')
})
