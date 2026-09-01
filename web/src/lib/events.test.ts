import { describe, expect, it } from 'vitest'

import {
  filterTimeline,
  findPendingApproval,
  mergeDeltaEvents,
  upsertEvents,
} from '@/lib/events'
import type { DeskEvent } from '@/lib/types'

const event = (
  seq: number,
  type: string,
  payload: DeskEvent['payload'],
): DeskEvent => ({ seq, type, payload })

describe('mergeDeltaEvents', () => {
  it('merges adjacent chunks and preserves their sequence range', () => {
    const entries = mergeDeltaEvents([
      event(1, 'message.delta', { text: 'Desk ' }),
      event(2, 'message.delta', { delta: 'ready' }),
      event(3, 'tool.requested', { name: 'fs.read' }),
    ])

    expect(entries).toHaveLength(2)
    expect(entries[0]).toMatchObject({
      seq: 1,
      endSeq: 2,
      merged: true,
      payload: { text: 'Desk ready', chunks: 2 },
    })
  })

  it('does not merge chunks from different message ids', () => {
    const entries = mergeDeltaEvents([
      event(1, 'message.delta', { id: 'a', text: 'one' }),
      event(2, 'message.delta', { id: 'b', text: 'two' }),
    ])

    expect(entries).toHaveLength(2)
  })
})

describe('event collection', () => {
  it('upserts duplicate SSE events in sequence order', () => {
    const events = upsertEvents(
      [event(2, 'old', {}), event(1, 'first', {})],
      [event(2, 'new', {})],
    )

    expect(events.map(({ seq, type }) => [seq, type])).toEqual([
      [1, 'first'],
      [2, 'new'],
    ])
  })

  it('filters merged entries by type and payload text', () => {
    const entries = mergeDeltaEvents([
      event(1, 'task.updated', { title: 'Index memory' }),
      event(2, 'tool.started', { name: 'fs.read' }),
    ])

    expect(filterTimeline(entries, 'task.updated', 'memory')).toHaveLength(1)
    expect(filterTimeline(entries, 'tool.started', 'memory')).toHaveLength(0)
  })
})

describe('findPendingApproval', () => {
  it('exposes path and content for the latest open tool request', () => {
    const pending = findPendingApproval(
      [
        event(4, 'tool.requested', {
          id: 'call-1',
          name: 'fs.write',
          args: { path: 'notes.md', content: 'hello' },
        }),
      ],
      'waiting',
    )

    expect(pending).toMatchObject({
      seq: 4,
      tool: 'fs.write',
      path: 'notes.md',
      content: 'hello',
    })
  })

  it('hides a request after it starts', () => {
    expect(
      findPendingApproval(
        [
          event(4, 'tool.requested', { id: 'call-1', args: {} }),
          event(5, 'tool.started', { id: 'call-1' }),
        ],
        'waiting',
      ),
    ).toBeUndefined()
  })
})
