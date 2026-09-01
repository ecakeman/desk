import { describe, expect, it } from 'vitest'

import { transcriptFromEvents } from '@/lib/transcript'
import type { DeskEvent } from '@/lib/types'

const event = (
  run_id: string,
  seq: number,
  type: string,
  payload: DeskEvent['payload'],
): DeskEvent => ({ run_id, seq, type, payload })

describe('transcriptFromEvents', () => {
  it('projects user and completed assistant turns across runs', () => {
    const bubbles = transcriptFromEvents([
      event('r1', 2, 'message.user', { text: 'hello' }),
      event('r1', 8, 'message.completed', { text: 'hi', model: 'pro', phase: 'plan' }),
      event('r2', 2, 'message.user', { text: 'again' }),
      event('r2', 3, 'message.delta', { text: 'work' }),
      event('r2', 4, 'message.delta', { text: 'ing' }),
    ])

    expect(bubbles.map((bubble) => [bubble.role, bubble.text, bubble.streaming])).toEqual([
      ['user', 'hello', undefined],
      ['assistant', 'hi', false],
      ['user', 'again', undefined],
      ['assistant', 'working', true],
    ])
    expect(bubbles[1]).toMatchObject({ model: 'pro', phase: 'plan', runID: 'r1' })
  })
})
