import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { asObject, eventText, stringField } from '@/lib/events'
import type { TimelineEntry } from '@/lib/types'
import { Activity, Bot, Database, FolderKanban, Sparkles, Wrench } from 'lucide-react'

function icon(type: string) {
  if (type.startsWith('tool.')) return <Wrench className="size-3.5" />
  if (type.startsWith('message.')) return <Bot className="size-3.5" />
  if (type.startsWith('task.')) return <FolderKanban className="size-3.5" />
  if (type.startsWith('memory.')) return <Database className="size-3.5" />
  if (type.startsWith('skill.')) return <Sparkles className="size-3.5" />
  return <Activity className="size-3.5" />
}

export function TimelinePane({
  entries,
  types,
  typeFilter,
  search,
  selectedSeq,
  onSearch,
  onFilter,
  onSelect,
}: {
  entries: TimelineEntry[]
  types: string[]
  typeFilter: string
  search: string
  selectedSeq?: number
  onSearch: (value: string) => void
  onFilter: (value: string) => void
  onSelect: (entry: TimelineEntry) => void
}) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-b px-4 py-3">
        <p className="text-[10px] font-semibold tracking-[0.14em] text-muted-foreground">
          LIVE RUN
        </p>
        <h2 className="text-sm font-semibold">Event Timeline</h2>
        <div className="mt-3 flex flex-wrap gap-2">
          <Input
            aria-label="搜索事件"
            placeholder="搜索 type / payload"
            value={search}
            onChange={(event) => onSearch(event.target.value)}
          />
          <select
            aria-label="按事件类型筛选"
            className="h-9 rounded-md border border-input bg-background px-2 text-sm text-foreground"
            value={typeFilter}
            onChange={(event) => onFilter(event.target.value)}
          >
            <option value="all">全部事件</option>
            {types.map((type) => (
              <option key={type} value={type}>
                {type}
              </option>
            ))}
          </select>
        </div>
      </div>
      <ol className="min-h-0 flex-1 overflow-y-auto p-3">
        {entries.map((entry) => {
          const selected =
            selectedSeq !== undefined &&
            selectedSeq >= entry.seq &&
            selectedSeq <= entry.endSeq
          const payload = asObject(entry.payload)
          const text =
            entry.type === 'message.delta'
              ? stringField(payload, 'text')
              : eventText(entry.events.at(-1) ?? entry.events[0])
          return (
            <li key={entry.key} className="mb-2">
              <button
                type="button"
                className={`flex w-full gap-2 rounded-lg border bg-card px-3 py-2 text-left text-sm text-card-foreground hover:bg-accent ${
                  selected ? 'border-primary bg-accent' : ''
                }`}
                onClick={() => onSelect(entry)}
              >
                <span className="mt-0.5 text-muted-foreground">{icon(entry.type)}</span>
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 font-mono text-xs">
                    {entry.type}
                    <span className="text-muted-foreground">
                      #{entry.seq}
                      {entry.endSeq !== entry.seq ? `–${entry.endSeq}` : ''}
                    </span>
                    {entry.merged && (
                      <Badge variant="secondary">{entry.events.length} deltas</Badge>
                    )}
                  </span>
                  {text ? (
                    <span className="mt-1 line-clamp-4 block whitespace-pre-wrap text-card-foreground/80">
                      {text}
                    </span>
                  ) : (
                    stringField(payload, 'name', 'tool') && (
                      <code className="mt-1 inline-block text-xs">
                        {stringField(payload, 'name', 'tool')}
                      </code>
                    )
                  )}
                </span>
              </button>
            </li>
          )
        })}
      </ol>
    </div>
  )
}
