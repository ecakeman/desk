import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ApprovalCard } from '@/features/workspace/approval-card'
import { asObject, mergeDeltaEvents, stringField } from '@/lib/events'
import { formatTime, isBusy, shortID, statusVariant } from '@/lib/format'
import type {
  DeskEvent,
  JsonObject,
  JsonValue,
  PendingApproval,
  Run,
} from '@/lib/types'
import { CircleStop, PanelRightClose } from 'lucide-react'

export type InspectorTab = 'event' | 'stm' | 'task' | 'memory' | 'compact' | 'skill'
export type ProvenanceRef = { runID: string; seq: number; label: string }

function eventJSON(event: DeskEvent): JsonObject {
  return {
    run_id: event.run_id ?? '',
    seq: event.seq,
    type: event.type,
    payload: event.payload,
  }
}

export function inspectorData(
  tab: Exclude<InspectorTab, 'event'>,
  events: DeskEvent[],
  stm: JsonValue | undefined,
): JsonValue {
  if (tab === 'stm') return stm ?? null
  if (tab === 'task') {
    const tasks = new Map<string, JsonObject>()
    for (const event of events.filter((item) => item.type === 'task.updated')) {
      const payload = asObject(event.payload)
      const id = stringField(payload, 'id') ?? `seq-${event.seq}`
      tasks.set(id, { seq: event.seq, ...payload })
    }
    return [...tasks.values()]
  }
  const prefix: Record<Exclude<InspectorTab, 'event' | 'stm' | 'task'>, string[]> = {
    memory: ['memory.'],
    compact: ['episode.compacted'],
    skill: ['skill.'],
  }
  return events
    .filter((event) => prefix[tab].some((value) => event.type.startsWith(value)))
    .map(eventJSON)
}

export function provenanceRefs(tab: InspectorTab, events: DeskEvent[]): ProvenanceRef[] {
  if (!['memory', 'compact', 'skill'].includes(tab)) return []
  const refs: ProvenanceRef[] = []
  for (const event of events) {
    const payload = asObject(event.payload)
    if (tab === 'memory' && event.type === 'memory.retrieved') {
      const hits = Array.isArray(payload.hits) ? payload.hits : []
      for (const value of hits) {
        const hit = asObject(value)
        const runID = stringField(hit, 'run_id')
        const seq = hit.seq
        if (runID && typeof seq === 'number') {
          refs.push({
            runID,
            seq,
            label: `${stringField(hit, 'kind') ?? 'event'} · ${shortID(runID)} #${seq}`,
          })
        }
      }
    }
    if (
      (tab === 'compact' && event.type === 'episode.compacted') ||
      (tab === 'skill' && event.type.startsWith('skill.'))
    ) {
      const basedOn = Array.isArray(payload.based_on) ? payload.based_on : []
      for (const seq of basedOn) {
        if (typeof seq === 'number' && event.run_id) {
          refs.push({
            runID: event.run_id,
            seq,
            label: `based_on · ${shortID(event.run_id)} #${seq}`,
          })
        }
      }
    }
  }
  return [...new Map(refs.map((ref) => [`${ref.runID}:${ref.seq}`, ref])).values()]
}

function JsonView({ value, label }: { value: JsonValue | undefined; label: string }) {
  return (
    <pre
      aria-label={label}
      className="max-h-80 overflow-auto rounded-md border bg-muted p-2 font-mono text-[11px] text-foreground whitespace-pre-wrap"
    >
      {value === undefined ? '—' : JSON.stringify(value, null, 2)}
    </pre>
  )
}

export function InspectorPane({
  run,
  runID,
  model,
  phase,
  streamState,
  pending,
  tab,
  selectedEvent,
  stm,
  events,
  error,
  onTab,
  onCancel,
  onDecide,
  onOpenRef,
  onSelectEvent,
  onCollapse,
}: {
  run?: Run
  runID?: string
  model?: string
  phase?: string
  streamState?: string
  pending?: PendingApproval
  tab: InspectorTab
  selectedEvent?: DeskEvent
  stm?: JsonValue
  events: DeskEvent[]
  error?: string
  onTab: (tab: InspectorTab) => void
  onCancel: () => void
  onDecide: (allow: boolean) => void
  onOpenRef: (ref: ProvenanceRef) => void
  onSelectEvent: (event: DeskEvent) => void
  onCollapse?: () => void
}) {
  const provenance = provenanceRefs(tab, events)
  const eventEntries = mergeDeltaEvents(events)
  const value = tab === 'event' ? selectedEvent?.payload : inspectorData(tab, events, stm)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b px-4 py-3">
        <p className="text-[10px] font-semibold tracking-[0.14em] text-muted-foreground">
          RUN STATE
        </p>
        <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Inspector</h2>
          {onCollapse && (
            <Button
              variant="ghost"
              size="icon"
              aria-label="收起 Inspector"
              onClick={onCollapse}
            >
              <PanelRightClose />
            </Button>
          )}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {error && <p className="mb-2 text-sm text-destructive">{error}</p>}
        <div className="mb-3 rounded-lg border bg-card p-3">
          <div className="mb-2 flex items-center justify-between gap-2">
            <span className="text-xs font-semibold">Run</span>
            <Badge variant={statusVariant(run?.status)}>{run?.status ?? 'unknown'}</Badge>
          </div>
          <dl className="grid grid-cols-2 gap-2 text-xs">
            <div>
              <dt className="text-muted-foreground">ID</dt>
              <dd className="font-mono">{runID ? shortID(runID) : '—'}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Model</dt>
              <dd>{model ?? '—'}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Phase</dt>
              <dd>{phase ?? '—'}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">Updated</dt>
              <dd>{formatTime(run?.updated_at)}</dd>
            </div>
            <div>
              <dt className="text-muted-foreground">SSE</dt>
              <dd>{streamState ?? 'idle'}</dd>
            </div>
          </dl>
          {isBusy(run?.status) && (
          <Button
            variant="destructive"
            className="mt-3 w-full"
            disabled={!runID}
            onClick={onCancel}
          >
            <CircleStop />
            Cancel run
          </Button>
          )}
        </div>

        {pending && <ApprovalCard pending={pending} onDecide={onDecide} />}

        <Tabs
          value={tab}
          onValueChange={(value) => onTab(value as InspectorTab)}
          className="mt-3"
        >
          <TabsList
            aria-label="Inspector 数据"
            className="flex h-auto min-h-9 w-full flex-wrap items-center justify-start gap-0.5"
          >
            {(['event', 'stm', 'task', 'memory', 'compact', 'skill'] as const).map((item) => (
              <TabsTrigger
                key={item}
                value={item}
                className="h-7 flex-none capitalize px-2 text-xs"
              >
                {item === 'stm' ? 'STM' : item[0].toUpperCase() + item.slice(1)}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent value={tab} className="mt-3">
            {tab === 'event' && (
              <div className="mb-2 max-h-56 overflow-auto rounded-md border">
                {eventEntries.length === 0 ? (
                  <p className="p-2 text-xs text-muted-foreground">本 Run 还没有事件</p>
                ) : (
                  eventEntries.map((entry) => {
                    const item = entry.events.at(-1)
                    if (!item) return null
                    const active = selectedEvent?.seq === entry.endSeq
                    const label =
                      entry.merged && entry.endSeq !== entry.seq
                        ? `${entry.type} #${entry.seq}–${entry.endSeq}`
                        : `${entry.type} #${entry.seq}`
                    return (
                      <button
                        type="button"
                        key={entry.key}
                        className={`flex w-full items-center justify-between border-b bg-card px-2 py-1 text-left font-mono text-[11px] text-card-foreground last:border-b-0 hover:bg-muted ${
                          active ? 'bg-muted' : ''
                        }`}
                        onClick={() => onSelectEvent(item)}
                      >
                        <span>{label}</span>
                      </button>
                    )
                  })
                )}
              </div>
            )}
            {tab === 'event' && selectedEvent && (
              <div className="mb-2 flex items-center justify-between text-xs font-semibold">
                <span>{selectedEvent.type}</span>
                <Badge variant="secondary">#{selectedEvent.seq}</Badge>
              </div>
            )}
            {tab !== 'event' &&
              tab !== 'stm' &&
              Array.isArray(value) &&
              value.length === 0 && (
                <p className="mb-2 text-xs text-muted-foreground">
                  本 Run 没有这类事件。Task 来自 `task.update`，Memory 来自 `memory.search`，Compact 来自超预算压缩，Skill 来自 review 里写 `memory/skills/*.md`。
                </p>
              )}
            <JsonView value={value} label={tab} />
            {provenance.length > 0 && (
              <div className="mt-3 grid gap-1">
                <span className="text-[10px] font-semibold tracking-wide text-muted-foreground">
                  PROVENANCE
                </span>
                {provenance.map((reference) => (
                  <button
                    type="button"
                    key={`${reference.runID}:${reference.seq}`}
                    className="flex items-center justify-between rounded-md border bg-card px-2 py-1 font-mono text-[11px] text-card-foreground hover:border-primary"
                    onClick={() => onOpenRef(reference)}
                  >
                    {reference.label}
                  </button>
                ))}
              </div>
            )}
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
