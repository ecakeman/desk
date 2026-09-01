import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ThemeSwitch } from '@/components/theme-switch'
import { ChatPane } from '@/features/workspace/chat'
import { InspectorPane, type InspectorTab } from '@/features/workspace/inspector'
import { SessionNav, titlesByRun, titlesBySession } from '@/features/workspace/session-nav'
import { TimelinePane } from '@/features/workspace/timeline'
import { useIsMobile } from '@/hooks/use-mobile'
import { useRunStream } from '@/hooks/use-run-stream'
import { api } from '@/lib/api'
import {
  filterTimeline,
  findPendingApproval,
  latestRuntimeMeta,
  mergeDeltaEvents,
  upsertEvents,
} from '@/lib/events'
import { errorMessage, isBusy, statusVariant } from '@/lib/format'
import { transcriptFromEvents } from '@/lib/transcript'
import type { DeskEvent, JsonValue } from '@/lib/types'
import { Badge } from '@/components/ui/badge'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { Bot, Menu, PanelLeftOpen, PanelRightOpen, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState, type MouseEvent } from 'react'

type MobilePane = 'navigation' | 'chat' | 'inspector'
type CenterMode = 'chat' | 'audit'

function sortByCreated<T extends { created_at?: string }>(items: T[]): T[] {
  return [...items].sort(
    (a, b) =>
      new Date(b.created_at ?? 0).valueOf() -
      new Date(a.created_at ?? 0).valueOf(),
  )
}

const LAST_RUN_KEY = 'desk-last-run'

function readLastRuns(): Record<string, string> {
  try {
    return JSON.parse(sessionStorage.getItem(LAST_RUN_KEY) || '{}') as Record<
      string,
      string
    >
  } catch {
    return {}
  }
}

function rememberRun(sessionID: string, runID: string) {
  const all = readLastRuns()
  all[sessionID] = runID
  sessionStorage.setItem(LAST_RUN_KEY, JSON.stringify(all))
}

function readPane(key: string, fallback: number) {
  const raw = localStorage.getItem(key)
  const n = raw ? Number(raw) : fallback
  return Number.isFinite(n) ? n : fallback
}

function onPaneDragStart(
  event: MouseEvent,
  side: 'left' | 'right',
  setLeftWidth: (value: number | ((width: number) => number)) => void,
  setRightWidth: (value: number | ((width: number) => number)) => void,
) {
  event.preventDefault()
  const move = (next: globalThis.MouseEvent) => {
    if (side === 'left') {
      setLeftWidth(Math.min(520, Math.max(200, next.clientX)))
    } else {
      setRightWidth(Math.min(640, Math.max(240, window.innerWidth - next.clientX)))
    }
  }
  const up = () => {
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    setLeftWidth((width) => {
      localStorage.setItem('desk-pane-left', String(width))
      return width
    })
    setRightWidth((width) => {
      localStorage.setItem('desk-pane-right', String(width))
      return width
    })
  }
  window.addEventListener('mousemove', move)
  window.addEventListener('mouseup', up)
}

export function Workspace() {
  const params = useParams({ strict: false }) as {
    sessionId?: string
    runId?: string
  }
  const sessionId = params.sessionId
  const runId = params.runId
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const mobile = useIsMobile()

  const [live, setLive] = useState<DeskEvent[]>([])
  const [center, setCenter] = useState<CenterMode>('chat')
  const [mobilePane, setMobilePane] = useState<MobilePane>('chat')
  const [navOpen, setNavOpen] = useState(false)
  const [tab, setTab] = useState<InspectorTab>('event')
  const [selectedEvent, setSelectedEvent] = useState<DeskEvent>()
  const [typeFilter, setTypeFilter] = useState('all')
  const [search, setSearch] = useState('')
  const [sendError, setSendError] = useState<string>()
  const [detailError, setDetailError] = useState<string>()
  const [leftCollapsed, setLeftCollapsed] = useState(false)
  const [rightCollapsed, setRightCollapsed] = useState(false)
  const [leftWidth, setLeftWidth] = useState(() => readPane('desk-pane-left', 288))
  const [rightWidth, setRightWidth] = useState(() => readPane('desk-pane-right', 384))

  const sessionsQuery = useQuery({
    queryKey: ['sessions'],
    queryFn: api.sessions,
  })
  const runsQuery = useQuery({
    queryKey: ['runs', sessionId],
    queryFn: () => api.runs(sessionId!),
    enabled: Boolean(sessionId),
  })
  const eventsQuery = useQuery({
    queryKey: ['session-events', sessionId],
    queryFn: () => api.sessionEvents(sessionId!),
    enabled: Boolean(sessionId),
  })
  const runQuery = useQuery({
    queryKey: ['run', runId],
    queryFn: () => api.run(runId!),
    enabled: Boolean(runId),
    refetchInterval: (query) =>
      isBusy(query.state.data?.status) ? 1500 : false,
  })
  const stmQuery = useQuery({
    queryKey: ['stm', runId],
    queryFn: () => api.stm(runId!),
    enabled: Boolean(runId),
  })

  const sessions = useMemo(
    () => sortByCreated(sessionsQuery.data ?? []),
    [sessionsQuery.data],
  )
  const runs = useMemo(
    () => sortByCreated(runsQuery.data ?? []),
    [runsQuery.data],
  )

  useEffect(() => {
    setLive([])
  }, [sessionId])

  useEffect(() => {
    if (!sessionId || runs.length === 0) return
    const remembered = readLastRuns()[sessionId]
    const validRemembered = runs.find((item) => item.id === remembered)
    if (!runId) {
      const pick = validRemembered ?? runs[0]
      void navigate({
        to: '/s/$sessionId/r/$runId',
        params: { sessionId, runId: pick.id },
        replace: true,
      })
      return
    }
    // 记住的 / URL 里的 run 已删时，落到该 Session 最新 Run，避免空白定位
    if (!runs.some((item) => item.id === runId)) {
      const pick = validRemembered ?? runs[0]
      void navigate({
        to: '/s/$sessionId/r/$runId',
        params: { sessionId, runId: pick.id },
        replace: true,
      })
    }
  }, [navigate, runId, runs, sessionId])

  const receiveEvent = useCallback((event: DeskEvent) => {
    setLive((current) =>
      upsertEvents(current, { ...event, run_id: event.run_id ?? runId }),
    )
    void queryClient.invalidateQueries({ queryKey: ['run', runId] })
    void queryClient.invalidateQueries({ queryKey: ['stm', runId] })
    if (
      event.type === 'run.completed' ||
      event.type === 'run.failed' ||
      event.type === 'run.interrupted'
    ) {
      void queryClient.invalidateQueries({ queryKey: ['runs', sessionId] })
      void queryClient.invalidateQueries({
        queryKey: ['session-events', sessionId],
      })
    }
  }, [queryClient, runId, sessionId])

  const streamState = useRunStream(
    runId && isBusy(runQuery.data?.status) ? runId : undefined,
    receiveEvent,
  )

  const events = useMemo(
    () => upsertEvents(eventsQuery.data ?? [], live),
    [eventsQuery.data, live],
  )
  const runEvents = useMemo(
    () => events.filter((event) => event.run_id === runId),
    [events, runId],
  )
  const titles = useMemo(
    () => titlesBySession(sessions, runs, events),
    [events, runs, sessions],
  )
  const runTitles = useMemo(() => titlesByRun(runs, events), [events, runs])
  const bubbles = useMemo(() => transcriptFromEvents(events), [events])
  const entries = useMemo(() => mergeDeltaEvents(runEvents), [runEvents])
  const filtered = useMemo(
    () => filterTimeline(entries, typeFilter, search),
    [entries, search, typeFilter],
  )
  const pending = useMemo(
    () => findPendingApproval(runEvents, runQuery.data?.status),
    [runEvents, runQuery.data?.status],
  )
  const meta = useMemo(() => latestRuntimeMeta(runEvents), [runEvents])
  const model = runQuery.data?.model ?? meta.model
  const phase = runQuery.data?.phase ?? meta.phase

  const goSession = (id: string) => {
    setNavOpen(false)
    setMobilePane('chat')
    // 一步到位：避免 /s/:id → 再自动跳 /r/:runId 造成「切换又定位」闪动
    const remembered = readLastRuns()[id]
    if (remembered) {
      void navigate({
        to: '/s/$sessionId/r/$runId',
        params: { sessionId: id, runId: remembered },
      })
      return
    }
    void navigate({ to: '/s/$sessionId', params: { sessionId: id } })
  }
  const goRun = (sid: string, rid: string) => {
    setNavOpen(false)
    rememberRun(sid, rid)
    void navigate({
      to: '/s/$sessionId/r/$runId',
      params: { sessionId: sid, runId: rid },
    })
  }

  const createSession = useMutation({
    mutationFn: api.createSession,
    onSuccess: (session) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      goSession(session.id)
    },
  })

  const send = useMutation({
    mutationFn: async (text: string) => {
      let sid = sessionId
      if (!sid) {
        const created = await api.createSession()
        sid = created.id
      }
      const posted = await api.postMessage(sid, text)
      return { sessionId: sid, runId: posted.run_id }
    },
    onSuccess: (result) => {
      setSendError(undefined)
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      void queryClient.invalidateQueries({ queryKey: ['runs', result.sessionId] })
      void queryClient.invalidateQueries({
        queryKey: ['session-events', result.sessionId],
      })
      goRun(result.sessionId, result.runId)
    },
    onError: (error) => setSendError(errorMessage(error)),
  })

  const selectedStatus =
    runQuery.data?.status ?? runs.find((item) => item.id === runId)?.status
  const chatBusy = send.isPending || isBusy(selectedStatus)

  const decide = useMutation({
    mutationFn: (allow: boolean) => {
      if (!runId || !pending) throw new Error('no_pending')
      return api.decide(runId, pending.seq, allow)
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['run', runId] })
      void queryClient.invalidateQueries({ queryKey: ['session-events', sessionId] })
    },
  })

  const cancel = useMutation({
    mutationFn: () => api.cancel(runId!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['run', runId] })
    },
  })

  const removeSession = useMutation({
    mutationFn: api.deleteSession,
    onSuccess: (_ok, id) => {
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
      if (sessionId === id) {
        const next = sessions.find((item) => item.id !== id)
        if (next) goSession(next.id)
        else void navigate({ to: '/' })
      }
    },
  })

  const removeRun = useMutation({
    mutationFn: api.deleteRun,
    onSuccess: (_ok, id) => {
      void queryClient.invalidateQueries({ queryKey: ['runs', sessionId] })
      void queryClient.invalidateQueries({ queryKey: ['session-events', sessionId] })
      if (runId === id && sessionId) goSession(sessionId)
    },
  })

  const openEvent = async (runID: string, seq: number, fallback?: DeskEvent) => {
    setTab('event')
    setMobilePane('inspector')
    if (fallback) setSelectedEvent(fallback)
    try {
      setSelectedEvent(await api.event(runID, seq))
      setDetailError(undefined)
    } catch (error) {
      setDetailError(`事件详情：${errorMessage(error)}`)
    }
  }

  const nav = (
    <SessionNav
      sessions={sessions}
      runs={runs}
      titles={titles}
      runTitles={runTitles}
      selectedSessionID={sessionId}
      selectedRunID={runId}
      creating={createSession.isPending}
      onNewSession={() => createSession.mutate()}
      onSelectSession={goSession}
      onSelectRun={goRun}
      onDeleteSession={(id) => removeSession.mutate(id)}
      onDeleteRun={(id) => removeRun.mutate(id)}
      onOpenWorkspace={() => void api.openWorkspace()}
      onCollapse={mobile ? undefined : () => setLeftCollapsed(true)}
    />
  )

  const inspector = (
    <InspectorPane
      run={runQuery.data}
      runID={runId}
      model={model}
      phase={phase}
      streamState={streamState}
      pending={pending}
      tab={tab}
      selectedEvent={selectedEvent}
      stm={stmQuery.data as JsonValue | undefined}
      events={runEvents}
      error={detailError}
      onTab={setTab}
      onCancel={() => runId && cancel.mutate()}
      onDecide={(allow) => decide.mutate(allow)}
      onOpenRef={(reference) => void openEvent(reference.runID, reference.seq)}
      onSelectEvent={(event) => {
        if (event.run_id) void openEvent(event.run_id, event.seq, event)
        else setSelectedEvent(event)
      }}
      onCollapse={mobile ? undefined : () => setRightCollapsed(true)}
    />
  )

  return (
    <div className="flex h-svh flex-col bg-background">
      <header className="flex h-14 items-center gap-2 border-b px-3">
        <Button
          variant="ghost"
          size="icon"
          className="lg:hidden"
          aria-label="打开 Session 列表"
          onClick={() => setNavOpen(true)}
        >
          <Menu />
        </Button>
        <div className="flex items-center gap-2">
          <div className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <Bot className="size-4" />
          </div>
          <div>
            <p className="text-sm font-semibold leading-none">Desk</p>
            <p className="text-xs text-muted-foreground">Agent Control Plane</p>
          </div>
        </div>
        <div className="ms-auto flex items-center gap-3 text-xs">
          {runQuery.data && (
            <span className="flex flex-col gap-0.5">
              <span className="text-[10px] text-muted-foreground">状态</span>
              <Badge variant={statusVariant(runQuery.data.status)}>
                {runQuery.data.status}
              </Badge>
            </span>
          )}
          {model && (
            <span className="flex flex-col gap-0.5">
              <span className="text-[10px] text-muted-foreground">模型</span>
              <span className="text-foreground">{model}</span>
            </span>
          )}
          {phase && (
            <span className="flex flex-col gap-0.5">
              <span className="text-[10px] text-muted-foreground">阶段</span>
              <span className="text-foreground">{phase}</span>
            </span>
          )}
          <Button
            variant="ghost"
            size="icon"
            aria-label="刷新 Session"
            onClick={() => void sessionsQuery.refetch()}
          >
            <RefreshCw className={sessionsQuery.isFetching ? 'animate-spin' : ''} />
          </Button>
          <ThemeSwitch />
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {mobile ? null : leftCollapsed ? (
          <button
            type="button"
            aria-label="展开对话列表"
            className="hidden w-8 shrink-0 items-start justify-center border-e pt-3 text-muted-foreground hover:bg-accent lg:flex"
            onClick={() => setLeftCollapsed(false)}
          >
            <PanelLeftOpen className="size-4" />
          </button>
        ) : (
          <>
            <aside
              className="hidden h-full min-h-0 shrink-0 lg:block"
              style={{ width: leftWidth }}
            >
              {nav}
            </aside>
            <button
              type="button"
              aria-label="调节左侧栏宽度"
              className="hidden w-1 shrink-0 cursor-col-resize bg-border hover:bg-primary lg:block"
              onMouseDown={(event) => onPaneDragStart(event, 'left', setLeftWidth, setRightWidth)}
            />
          </>
        )}
        <main
          className={`min-w-0 flex-1 flex-col ${
            !mobile || mobilePane === 'chat' ? 'flex' : 'hidden'
          }`}
        >
          <div className="flex items-center justify-between border-b px-3 py-2">
            <Tabs value={center} onValueChange={(value) => setCenter(value as CenterMode)}>
              <TabsList>
                <TabsTrigger value="chat">对话</TabsTrigger>
                <TabsTrigger value="audit">审计</TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
          <div className="flex min-h-0 flex-1 flex-col">
            {center === 'chat' ? (
              <ChatPane
                sessionId={sessionId}
                bubbles={bubbles}
                busy={chatBusy}
                pending={pending}
                sending={send.isPending}
                error={
                  sendError ??
                  (sessionsQuery.error
                    ? errorMessage(sessionsQuery.error)
                    : undefined)
                }
                onSend={(text) => send.mutate(text)}
                onDecide={(allow) => decide.mutate(allow)}
                onSelectBubble={(bubble) => {
                  if (!sessionId || !bubble.runID) return
                  goRun(sessionId, bubble.runID)
                  if (bubble.seq) void openEvent(bubble.runID, bubble.seq)
                }}
              />
            ) : (
              <TimelinePane
                entries={filtered}
                types={[...new Set(entries.map((entry) => entry.type))].sort()}
                typeFilter={typeFilter}
                search={search}
                selectedSeq={selectedEvent?.seq}
                onSearch={setSearch}
                onFilter={setTypeFilter}
                onSelect={(entry) => {
                  const event = entry.events.at(-1)
                  if (event?.run_id) void openEvent(event.run_id, entry.endSeq, event)
                }}
              />
            )}
          </div>
        </main>
        {mobile ? (
          <aside
            className={`min-h-0 w-full shrink-0 flex-col border-s ${
              mobilePane === 'inspector' ? 'flex' : 'hidden'
            }`}
          >
            {inspector}
          </aside>
        ) : rightCollapsed ? (
          <button
            type="button"
            aria-label="展开 Inspector"
            className="flex w-8 shrink-0 items-start justify-center border-s pt-3 text-muted-foreground hover:bg-accent"
            onClick={() => setRightCollapsed(false)}
          >
            <PanelRightOpen className="size-4" />
          </button>
        ) : (
          <>
            <button
              type="button"
              aria-label="调节右侧栏宽度"
              className="w-1 shrink-0 cursor-col-resize bg-border hover:bg-primary"
              onMouseDown={(event) => onPaneDragStart(event, 'right', setLeftWidth, setRightWidth)}
            />
            <aside
              className="flex h-full min-h-0 shrink-0 flex-col"
              style={{ width: rightWidth }}
            >
              {inspector}
            </aside>
          </>
        )}
      </div>

      <nav className="grid grid-cols-3 border-t md:hidden" aria-label="移动端面板">
        <Button variant="ghost" onClick={() => { setNavOpen(true); setMobilePane('navigation') }}>
          Session
        </Button>
        <Button variant="ghost" onClick={() => setMobilePane('chat')}>
          对话
        </Button>
        <Button variant="ghost" onClick={() => setMobilePane('inspector')}>
          Inspector
        </Button>
      </nav>

      <Sheet open={navOpen} onOpenChange={setNavOpen}>
        <SheetContent side="left" className="w-80 p-0">
          <SheetHeader className="sr-only">
            <SheetTitle>Session 列表</SheetTitle>
          </SheetHeader>
          {nav}
        </SheetContent>
      </Sheet>
    </div>
  )
}
