import { describe, expect, it } from 'vitest'

import { reconnectDelay } from '@/hooks/use-run-stream'
import { normalizeCollection } from '@/lib/api'

describe('normalizeCollection', () => {
  it('accepts bare arrays and common API envelopes', () => {
    expect(normalizeCollection<number>([1, 2], ['runs'])).toEqual([1, 2])
    expect(normalizeCollection<number>({ runs: [3] }, ['runs'])).toEqual([3])
    expect(normalizeCollection<number>({ items: [4] }, ['runs'])).toEqual([4])
  })

  it('returns an empty array for an unknown response shape', () => {
    expect(normalizeCollection({ ok: true }, ['sessions'])).toEqual([])
  })
})

describe('reconnectDelay', () => {
  it('backs off exponentially and caps retries', () => {
    expect(reconnectDelay(1)).toBe(750)
    expect(reconnectDelay(4)).toBe(6_000)
    expect(reconnectDelay(20)).toBe(12_000)
  })
})
