import { asObject, eventText, mergeDeltaEvents, stringField } from '@/lib/events'
import type { DeskEvent } from '@/lib/types'

export type ChatBubble = {
  key: string
  role: 'user' | 'assistant'
  text: string
  runID?: string
  seq?: number
  model?: string
  phase?: string
  streaming?: boolean
}

export function sessionTitle(events: DeskEvent[], fallback: string): string {
  const user = events.find((event) => event.type === 'message.user')
  const text = user ? eventText(user).trim() : ''
  return text || fallback
}

export function transcriptFromEvents(events: DeskEvent[]): ChatBubble[] {
  const runOrder: string[] = []
  const grouped = new Map<string, DeskEvent[]>()
  for (const event of events) {
    const runID = event.run_id ?? ''
    if (!grouped.has(runID)) {
      runOrder.push(runID)
      grouped.set(runID, [])
    }
    grouped.get(runID)!.push(event)
  }

  const bubbles: ChatBubble[] = []
  for (const runID of runOrder) {
    const runEvents = (grouped.get(runID) ?? []).sort((a, b) => a.seq - b.seq)
    const user = runEvents.find((event) => event.type === 'message.user')
    if (user) {
      bubbles.push({
        key: `${runID}:user:${user.seq}`,
        role: 'user',
        text: eventText(user),
        runID,
        seq: user.seq,
      })
    }

    const completed = [...runEvents]
      .reverse()
      .find((event) => event.type === 'message.completed')
    const deltas = mergeDeltaEvents(
      runEvents.filter((event) => event.type === 'message.delta'),
    )
    const deltaText = deltas
      .map((entry) => eventText(entry.events.at(-1) ?? entry.events[0]))
      .join('')
    const mergedDelta = deltas
      .map((entry) => {
        const payload = asObject(entry.payload)
        return typeof payload.text === 'string' ? payload.text : eventText(entry.events[0])
      })
      .join('')
    const assistantText = completed
      ? eventText(completed)
      : mergedDelta || deltaText
    if (!assistantText && !completed) continue

    const source = completed ?? deltas.at(-1)?.events.at(-1)
    const payload = asObject(source?.payload)
    bubbles.push({
      key: `${runID}:assistant`,
      role: 'assistant',
      text: assistantText,
      runID,
      seq: source?.seq,
      model: stringField(payload, 'model'),
      phase: stringField(payload, 'phase'),
      streaming: !completed && assistantText.length > 0,
    })
  }
  return bubbles
}
