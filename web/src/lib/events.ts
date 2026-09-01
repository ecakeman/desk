import type {
  DeskEvent,
  JsonObject,
  JsonValue,
  PendingApproval,
  TimelineEntry,
} from '@/lib/types'

export function asObject(value: JsonValue | undefined): JsonObject {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value
    : {}
}

export function stringField(
  object: JsonObject,
  ...keys: string[]
): string | undefined {
  for (const key of keys) {
    const value = object[key]
    if (typeof value === 'string') return value
  }
}

export function eventText(event: DeskEvent): string {
  if (typeof event.payload === 'string') return event.payload
  const payload = asObject(event.payload)
  return stringField(payload, 'text', 'delta', 'content', 'message') ?? ''
}

function deltaGroup(event: DeskEvent): string {
  const payload = asObject(event.payload)
  return (
    stringField(payload, 'message_id', 'id', 'stream_id') ??
    `${event.run_id ?? ''}:message`
  )
}

export function mergeDeltaEvents(events: DeskEvent[]): TimelineEntry[] {
  const sorted = [...events].sort((a, b) => a.seq - b.seq)
  const result: TimelineEntry[] = []

  for (const event of sorted) {
    const previous = result.at(-1)
    const canMerge =
      event.type === 'message.delta' &&
      previous?.type === 'message.delta' &&
      previous.endSeq + 1 === event.seq &&
      deltaGroup(previous.events[0]) === deltaGroup(event)

    if (canMerge && previous) {
      previous.endSeq = event.seq
      previous.events.push(event)
      previous.merged = true
      previous.payload = {
        text: previous.events.map(eventText).join(''),
        chunks: previous.events.length,
      }
      previous.key = `${previous.seq}-${previous.endSeq}`
      continue
    }

    result.push({
      key: String(event.seq),
      seq: event.seq,
      endSeq: event.seq,
      type: event.type,
      payload: event.payload,
      events: [event],
      merged: false,
    })
  }

  return result
}

export function eventKey(event: DeskEvent): string {
  return `${event.run_id ?? ''}:${event.seq}`
}

export function upsertEvents(
  current: DeskEvent[],
  incoming: DeskEvent | DeskEvent[],
): DeskEvent[] {
  const items = [
    ...current,
    ...(Array.isArray(incoming) ? incoming : [incoming]),
  ]
  const latest = new Map<string, DeskEvent>()
  const runOrder: string[] = []
  for (const event of items) {
    const runID = event.run_id ?? ''
    if (!runOrder.includes(runID)) runOrder.push(runID)
    latest.set(eventKey(event), event)
  }
  const grouped = new Map<string, DeskEvent[]>()
  for (const event of latest.values()) {
    const runID = event.run_id ?? ''
    const group = grouped.get(runID) ?? []
    group.push(event)
    grouped.set(runID, group)
  }
  return runOrder.flatMap((runID) =>
    (grouped.get(runID) ?? []).sort((a, b) => a.seq - b.seq),
  )
}

export function filterTimeline(
  entries: TimelineEntry[],
  type: string,
  search: string,
): TimelineEntry[] {
  const query = search.trim().toLocaleLowerCase()
  return entries.filter((entry) => {
    if (type !== 'all' && entry.type !== type) return false
    if (!query) return true
    return `${entry.type} ${JSON.stringify(entry.payload)}`
      .toLocaleLowerCase()
      .includes(query)
  })
}

function toolIdentity(event: DeskEvent): string | undefined {
  const payload = asObject(event.payload)
  return stringField(payload, 'id', 'tool_call_id')
}

export function findPendingApproval(
  events: DeskEvent[],
  status?: string,
): PendingApproval | undefined {
  if (!status || !['waiting', 'waiting_approval'].includes(status)) {
    return undefined
  }

  const closedIDs = new Set<string>()
  for (const event of events) {
    if (
      ['tool.started', 'tool.denied', 'tool.completed', 'tool.failed'].includes(
        event.type,
      )
    ) {
      const id = toolIdentity(event)
      if (id) closedIDs.add(id)
    }
  }

  const requested = [...events]
    .reverse()
    .find(
      (event) =>
        event.type === 'tool.requested' &&
        !closedIDs.has(toolIdentity(event) ?? ''),
    )
  if (!requested) return undefined

  const payload = asObject(requested.payload)
  const argsValue = payload.args
  const args = asObject(argsValue)
  return {
    seq: requested.seq,
    tool: stringField(payload, 'name', 'tool') ?? 'unknown tool',
    path: stringField(args, 'path', 'file'),
    content: stringField(args, 'content', 'text'),
    args,
  }
}

export function latestRuntimeMeta(events: DeskEvent[]): {
  model?: string
  phase?: string
} {
  let model: string | undefined
  let phase: string | undefined
  for (const event of events) {
    const payload = asObject(event.payload)
    model = stringField(payload, 'model') ?? model
    phase = stringField(payload, 'phase') ?? phase
  }
  return { model, phase }
}
