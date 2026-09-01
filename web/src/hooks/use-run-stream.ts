import { useEffect, useState } from 'react'

import type { DeskEvent, StreamState } from '@/lib/types'

const BASE_DELAY = 750
const MAX_DELAY = 12_000

export function reconnectDelay(attempt: number): number {
  return Math.min(MAX_DELAY, BASE_DELAY * 2 ** Math.max(0, attempt - 1))
}

function isDeskEvent(value: unknown): value is DeskEvent {
  if (!value || typeof value !== 'object') return false
  const event = value as Partial<DeskEvent>
  return (
    typeof event.seq === 'number' &&
    typeof event.type === 'string' &&
    'payload' in event
  )
}

export function useRunStream(
  runID: string | undefined,
  onEvent: (event: DeskEvent) => void,
): StreamState {
  const [state, setState] = useState<StreamState>('idle')

  useEffect(() => {
    if (!runID) {
      setState('idle')
      return
    }

    let source: EventSource | undefined
    let timer: number | undefined
    let stopped = false
    let attempt = 0
    let lastSeq = 0

    const connect = () => {
      if (stopped) return
      setState(attempt ? 'retrying' : 'connecting')
      source = new EventSource(
        `/v1/runs/${encodeURIComponent(runID)}/events?after=${lastSeq}`,
      )
      source.onopen = () => {
        attempt = 0
        setState('live')
      }
      source.onmessage = (message) => {
        try {
          const event: unknown = JSON.parse(message.data)
          if (!isDeskEvent(event)) return
          lastSeq = Math.max(lastSeq, event.seq)
          onEvent(event)
        } catch {
          // Ignore malformed frames; the next valid event keeps the stream alive.
        }
      }
      source.onerror = () => {
        source?.close()
        if (stopped) return
        attempt += 1
        setState('retrying')
        timer = window.setTimeout(connect, reconnectDelay(attempt))
      }
    }

    connect()
    return () => {
      stopped = true
      source?.close()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [onEvent, runID])

  return state
}
