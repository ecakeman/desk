import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { clip, formatTime } from '@/lib/format'
import { eventText } from '@/lib/events'
import { sessionTitle } from '@/lib/transcript'
import type { DeskEvent, Run, Session } from '@/lib/types'
import { ChevronDown, ChevronRight, FolderOpen, PanelLeftClose, Plus } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent } from 'react'

const FOLD_KEY = 'desk-folded-sessions'
const SPLIT_KEY = 'desk-nav-split'

export function titlesBySession(
  sessions: Session[],
  runs: Run[],
  events: DeskEvent[],
): Record<string, string> {
  const runSession = new Map(runs.map((run) => [run.id, run.session_id]))
  const grouped = new Map<string, DeskEvent[]>()
  for (const event of events) {
    const sessionID = event.run_id ? runSession.get(event.run_id) : undefined
    if (!sessionID) continue
    const list = grouped.get(sessionID) ?? []
    list.push(event)
    grouped.set(sessionID, list)
  }
  const titles: Record<string, string> = {}
  for (const session of sessions) {
    const live = sessionTitle(grouped.get(session.id) ?? [], '')
    const stored = typeof session.title === 'string' ? session.title.trim() : ''
    titles[session.id] = clip(live || stored, 42) || '新对话'
  }
  return titles
}

export function titlesByRun(
  runs: Run[],
  events: DeskEvent[],
): Record<string, string> {
  const titles: Record<string, string> = {}
  for (const run of runs) {
    const user = events.find(
      (event) => event.run_id === run.id && event.type === 'message.user',
    )
    const text = user ? eventText(user).trim() : ''
    titles[run.id] = clip(text, 42) || '未命名回合'
  }
  return titles
}

function readFolds(): string[] {
  try {
    const raw = JSON.parse(localStorage.getItem(FOLD_KEY) || '[]') as unknown
    return Array.isArray(raw) ? raw.filter((id) => typeof id === 'string') : []
  } catch {
    return []
  }
}

function writeFolds(ids: string[]) {
  localStorage.setItem(FOLD_KEY, JSON.stringify(ids))
}

function readSplit() {
  try {
    const raw = JSON.parse(localStorage.getItem(SPLIT_KEY) || '') as {
      a?: number
      b?: number
      c?: number
    }
    if (
      typeof raw.a === 'number' &&
      typeof raw.b === 'number' &&
      typeof raw.c === 'number'
    ) {
      return { a: raw.a, b: raw.b, c: raw.c }
    }
  } catch {
    /* default */
  }
  return { a: 48, b: 20, c: 32 }
}

type Menu = {
  kind: 'session' | 'run'
  id: string
  x: number
  y: number
}

type PendingDelete =
  | { kind: 'session'; id: string; title: string }
  | { kind: 'run'; id: string; title: string }
  | { kind: 'batch'; ids: string[]; title: string }

export function SessionNav({
  sessions,
  runs,
  titles,
  runTitles,
  selectedSessionID,
  selectedRunID,
  onSelectSession,
  onSelectRun,
  onNewSession,
  onDeleteSession,
  onDeleteRun,
  onOpenWorkspace,
  onCollapse,
  creating,
}: {
  sessions: Session[]
  runs: Run[]
  titles: Record<string, string>
  runTitles: Record<string, string>
  selectedSessionID?: string
  selectedRunID?: string
  onSelectSession: (id: string) => void
  onSelectRun: (sessionID: string, runID: string) => void
  onNewSession: () => void
  onDeleteSession: (id: string) => void
  onDeleteRun: (id: string) => void
  onOpenWorkspace: () => void
  onCollapse?: () => void
  creating?: boolean
}) {
  const [menu, setMenu] = useState<Menu>()
  const [pending, setPending] = useState<PendingDelete>()
  const [managing, setManaging] = useState(false)
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [foldedIds, setFoldedIds] = useState<string[]>(readFolds)
  const [foldedOpen, setFoldedOpen] = useState(false)
  const [split, setSplit] = useState(readSplit)
  const listRef = useRef<HTMLDivElement>(null)
  const splitRef = useRef<HTMLDivElement>(null)
  const scrollTop = useRef(0)

  const folded = useMemo(() => new Set(foldedIds), [foldedIds])
  const activeSessions = sessions.filter((item) => !folded.has(item.id))
  const foldedSessions = sessions.filter((item) => folded.has(item.id))

  useEffect(() => {
    if (!menu) return
    const close = () => setMenu(undefined)
    window.addEventListener('click', close)
    window.addEventListener('scroll', close, true)
    return () => {
      window.removeEventListener('click', close)
      window.removeEventListener('scroll', close, true)
    }
  }, [menu])

  useLayoutEffect(() => {
    if (listRef.current) listRef.current.scrollTop = scrollTop.current
  }, [selectedSessionID, runs.length])

  const persistFolds = (ids: string[]) => {
    setFoldedIds(ids)
    writeFolds(ids)
  }

  const openMenu = (
    kind: Menu['kind'],
    id: string,
    event: MouseEvent,
  ) => {
    event.preventDefault()
    event.stopPropagation()
    setMenu({ kind, id, x: event.clientX, y: event.clientY })
  }

  const togglePick = (id: string) => {
    setPicked((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const foldPicked = () => {
    const extra = [...picked].filter((id) => sessions.some((item) => item.id === id))
    if (extra.length === 0) return
    persistFolds([...new Set([...foldedIds, ...extra])])
    if (selectedSessionID && extra.includes(selectedSessionID)) {
      const next = activeSessions.find((item) => !extra.includes(item.id))
      if (next) onSelectSession(next.id)
    }
    setPicked(new Set())
  }

  const unfoldPicked = () => {
    persistFolds(foldedIds.filter((id) => !picked.has(id)))
    setPicked(new Set())
  }

  const startSplit = (edge: 'ab' | 'bc') => (event: MouseEvent) => {
    event.preventDefault()
    const box = splitRef.current
    if (!box) return
    const startY = event.clientY
    const start = { ...split }
    const height = box.clientHeight || 1
    const move = (next: globalThis.MouseEvent) => {
      const dy = ((next.clientY - startY) / height) * 100
      if (edge === 'ab') {
        const a = Math.min(78, Math.max(14, start.a + dy))
        const rest = 100 - a
        const bShare = foldedOpen ? start.b / Math.max(1, start.b + start.c) : 0
        const b = foldedOpen ? Math.min(70, Math.max(12, rest * bShare)) : start.b
        setSplit({ a, b, c: rest - b })
      } else {
        const c = Math.min(78, Math.max(14, start.c - dy))
        const rest = 100 - c
        const aShare = start.a / Math.max(1, start.a + start.b)
        const a = foldedOpen ? Math.min(70, Math.max(12, rest * aShare)) : rest - 0
        const b = foldedOpen ? rest - a : start.b
        setSplit({ a: foldedOpen ? a : rest, b, c })
      }
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      setSplit((value) => {
        localStorage.setItem(SPLIT_KEY, JSON.stringify(value))
        return value
      })
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  const renderSession = (session: Session, area: 'active' | 'folded') => {
    const title = titles[session.id] || '新对话'
    const checked = picked.has(session.id)
    if (managing) {
      return (
        <label
          key={session.id}
          className={`flex w-full cursor-pointer items-start gap-2 rounded-md px-2 py-2 text-left text-sm hover:bg-sidebar-accent ${
            checked ? 'bg-sidebar-accent' : ''
          }`}
        >
          <input
            type="checkbox"
            className="mt-1"
            checked={checked}
            onChange={() => togglePick(session.id)}
          />
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium">{title}</span>
            <span className="text-xs text-muted-foreground">
              {formatTime(session.created_at)}
            </span>
          </span>
        </label>
      )
    }
    return (
      <button
        type="button"
        key={session.id}
        aria-label={`Session ${session.id}`}
        title={title}
        className={`flex w-full items-start gap-2 rounded-md px-2 py-2 text-left text-sm text-sidebar-foreground hover:bg-sidebar-accent ${
          selectedSessionID === session.id ? 'bg-sidebar-accent' : ''
        }`}
        onClick={() => onSelectSession(session.id)}
        onContextMenu={(event) => openMenu('session', session.id, event)}
      >
        <span className="mt-1 size-2 shrink-0 rounded-full bg-primary/70" />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{title}</span>
          <span className="text-xs text-muted-foreground">
            {formatTime(session.created_at)}
          </span>
        </span>
        {area === 'folded' && (
          <span className="text-[10px] text-muted-foreground">已折叠</span>
        )}
      </button>
    )
  }

  const pickedActive = [...picked].filter((id) =>
    activeSessions.some((item) => item.id === id),
  )
  const pickedFolded = [...picked].filter((id) =>
    foldedSessions.some((item) => item.id === id),
  )

  return (
    <div className="flex h-full min-h-0 flex-col bg-sidebar text-sidebar-foreground">
      <div className="flex items-center justify-between gap-2 border-b border-sidebar-border px-3 py-3">
        <button
          type="button"
          className="min-w-0 text-left hover:opacity-80"
          title="打开 Workspace 文件夹"
          onClick={onOpenWorkspace}
        >
          <p className="text-[10px] font-semibold tracking-[0.14em] text-muted-foreground">
            WORKSPACE
          </p>
          <h2 className="flex items-center gap-1 text-sm font-semibold">
            <FolderOpen className="size-3.5" />
            打开文件夹
          </h2>
        </button>
        <div className="flex items-center gap-1">
          <Button size="sm" onClick={onNewSession} disabled={creating}>
            <Plus />
            新对话
          </Button>
          <Button
            size="sm"
            variant={managing ? 'default' : 'outline'}
            onClick={() => {
              setManaging((value) => !value)
              setPicked(new Set())
            }}
          >
            {managing ? '完成' : '管理'}
          </Button>
          {onCollapse && (
            <Button
              variant="ghost"
              size="icon"
              aria-label="收起对话列表"
              onClick={onCollapse}
            >
              <PanelLeftClose />
            </Button>
          )}
        </div>
      </div>
      {managing && (
        <div className="flex flex-wrap gap-1 border-b border-sidebar-border px-3 py-2">
          <Button
            size="sm"
            variant="destructive"
            disabled={picked.size === 0}
            onClick={() =>
              setPending({
                kind: 'batch',
                ids: [...picked],
                title: `${picked.size} 条对话`,
              })
            }
          >
            删除
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={pickedActive.length === 0}
            onClick={foldPicked}
          >
            折叠
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={pickedFolded.length === 0}
            onClick={unfoldPicked}
          >
            拉出
          </Button>
        </div>
      )}
      <div ref={splitRef} className="flex min-h-0 flex-1 flex-col">
        <section
          className="flex min-h-0 flex-col"
          style={{ flex: `${split.a} ${split.a} 0%` }}
        >
          <p className="px-3 pt-2 text-[10px] font-semibold tracking-[0.14em] text-muted-foreground">
            对话
          </p>
          <div
            ref={listRef}
            className="min-h-0 flex-1 overflow-y-auto p-2"
            onScroll={(event) => {
              scrollTop.current = event.currentTarget.scrollTop
            }}
          >
            {activeSessions.length === 0 && (
              <p className="px-2 py-6 text-sm text-muted-foreground">
                还没有 Session。发送第一条消息，或点「新对话」。
              </p>
            )}
            <div className="grid gap-1">
              {activeSessions.map((session) => renderSession(session, 'active'))}
            </div>
          </div>
        </section>
        <button
          type="button"
          aria-label="调节对话与折叠区高度"
          className="h-1 shrink-0 cursor-row-resize bg-border hover:bg-primary"
          onMouseDown={startSplit('ab')}
        />
        <section
          className="flex min-h-0 flex-col border-y border-sidebar-border"
          style={
            foldedOpen
              ? { flex: `${split.b} ${split.b} 0%` }
              : { flex: '0 0 auto' }
          }
        >
          <button
            type="button"
            className="flex w-full items-center gap-1 px-3 py-2 text-left text-[10px] font-semibold tracking-[0.14em] text-muted-foreground hover:bg-sidebar-accent"
            onClick={() => setFoldedOpen((value) => !value)}
          >
            {foldedOpen ? (
              <ChevronDown className="size-3" />
            ) : (
              <ChevronRight className="size-3" />
            )}
            折叠会话
            <span className="font-normal">({foldedSessions.length})</span>
          </button>
          {foldedOpen && (
            <div className="min-h-0 flex-1 overflow-y-auto p-2">
              {foldedSessions.length === 0 && (
                <p className="px-2 py-3 text-sm text-muted-foreground">
                  管理里选中对话后点「折叠」，会出现在这里。
                </p>
              )}
              <div className="grid gap-1">
                {foldedSessions.map((session) => renderSession(session, 'folded'))}
              </div>
            </div>
          )}
        </section>
        <button
          type="button"
          aria-label="调节折叠区与 Run 高度"
          className="h-1 shrink-0 cursor-row-resize bg-border hover:bg-primary"
          onMouseDown={startSplit('bc')}
        />
        <section
          className="flex min-h-0 flex-col"
          style={{ flex: `${split.c} ${split.c} 0%` }}
        >
          <p className="px-3 pt-2 text-[10px] font-semibold tracking-[0.14em] text-muted-foreground">
            RUNS
          </p>
          <div className="min-h-0 flex-1 overflow-y-auto p-2">
            {!selectedSessionID && (
              <p className="px-2 py-4 text-sm text-muted-foreground">
                先选一条对话，再看该对话下的 Run。
              </p>
            )}
            {selectedSessionID && runs.length === 0 && (
              <p className="px-2 py-4 text-sm text-muted-foreground">
                这条对话还没有 Run。
              </p>
            )}
            {selectedSessionID && (
              <div className="grid gap-1">
                {runs.map((run) => {
                  const title = runTitles[run.id] || '未命名回合'
                  return (
                    <button
                      type="button"
                      key={run.id}
                      title={title}
                      className={`flex w-full flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left text-xs text-sidebar-foreground hover:bg-sidebar-accent ${
                        selectedRunID === run.id ? 'bg-sidebar-accent' : ''
                      }`}
                      onClick={() => onSelectRun(selectedSessionID, run.id)}
                      onContextMenu={(event) => openMenu('run', run.id, event)}
                    >
                      <span className="w-full truncate font-medium">{title}</span>
                      <span className="text-[10px] text-muted-foreground">
                        {run.status ?? 'unknown'} · {formatTime(run.created_at)}
                      </span>
                    </button>
                  )
                })}
              </div>
            )}
          </div>
        </section>
      </div>
      {menu && (
        <div
          className="fixed z-50 min-w-28 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
          style={{ left: menu.x, top: menu.y }}
          onClick={(event) => event.stopPropagation()}
        >
          <button
            type="button"
            className="block w-full rounded-sm px-2 py-1.5 text-left text-sm text-destructive hover:bg-accent"
            onClick={() =>
              setPending(
                menu.kind === 'session'
                  ? {
                      kind: 'session',
                      id: menu.id,
                      title: titles[menu.id] || '新对话',
                    }
                  : {
                      kind: 'run',
                      id: menu.id,
                      title: runTitles[menu.id] || '未命名回合',
                    },
              )
            }
          >
            删除
          </button>
        </div>
      )}
      {pending && (
        <ConfirmDialog
          title={
            pending.kind === 'batch'
              ? '删除所选对话'
              : pending.kind === 'session'
                ? '删除对话'
                : '删除 Run'
          }
          message={
            pending.kind === 'batch'
              ? `确定删除 ${pending.title}？将从数据库删除对应 Session、Run、事件和记忆索引，不可恢复。`
              : pending.kind === 'session'
                ? `确定删除「${pending.title}」？将从数据库删除该对话下的 Session、Run、事件和记忆索引，不可恢复。`
                : `确定删除「${pending.title}」？将从数据库删除该 Run 的事件和记忆索引，不可恢复。`
          }
          confirmLabel="确认删除"
          onCancel={() => setPending(undefined)}
          onConfirm={() => {
            if (pending.kind === 'batch') {
              for (const id of pending.ids) onDeleteSession(id)
              persistFolds(foldedIds.filter((id) => !pending.ids.includes(id)))
              setPicked(new Set())
            } else if (pending.kind === 'session') onDeleteSession(pending.id)
            else onDeleteRun(pending.id)
            setPending(undefined)
            setMenu(undefined)
          }}
        />
      )}
    </div>
  )
}
