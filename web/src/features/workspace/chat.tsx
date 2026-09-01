import { MarkdownView } from '@/components/markdown'
import { Button } from '@/components/ui/button'
import { ApprovalCard } from '@/features/workspace/approval-card'
import type { ChatBubble } from '@/lib/transcript'
import type { PendingApproval } from '@/lib/types'
import { Bot, User } from 'lucide-react'
import { useEffect, useRef, useState, type MouseEvent as ReactMouseEvent } from 'react'

export function ChatPane({
  sessionId,
  bubbles,
  busy,
  pending,
  sending,
  error,
  onSend,
  onDecide,
  onSelectBubble,
}: {
  sessionId?: string
  bubbles: ChatBubble[]
  busy: boolean
  pending?: PendingApproval
  sending?: boolean
  error?: string
  onSend: (text: string) => void
  onDecide: (allow: boolean) => void
  onSelectBubble: (bubble: ChatBubble) => void
}) {
  const [draft, setDraft] = useState('')
  const [composer, setComposer] = useState(96)
  const bottom = useRef<HTMLDivElement>(null)
  const seenSession = useRef(sessionId)

  useEffect(() => {
    if (sessionId !== seenSession.current) {
      seenSession.current = sessionId
      return
    }
    bottom.current?.scrollIntoView({ block: 'end' })
  }, [bubbles, pending, sessionId])

  const submit = () => {
    const text = draft.trim()
    if (!text || busy || sending) return
    onSend(text)
    setDraft('')
  }

  const startResize = (event: ReactMouseEvent) => {
    event.preventDefault()
    const startY = event.clientY
    const startH = composer
    const move = (next: MouseEvent) => {
      // 拖上变高，拖下变矮（输入栏在底部）
      setComposer(Math.min(360, Math.max(72, startH + (startY - next.clientY))))
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
        {bubbles.length === 0 && (
          <div className="mx-auto max-w-lg py-16 text-center text-sm text-muted-foreground">
            <p className="font-medium text-foreground">Desk 对话</p>
            <p className="mt-2">
              与 `desk chat` 同一服务。消息会写成 event，助手回复从同一时间线投影。
            </p>
          </div>
        )}
        <div className="mx-auto grid max-w-3xl gap-3">
          {bubbles.map((bubble) => (
            <div
              role="button"
              tabIndex={0}
              key={bubble.key}
              className={`max-w-[85%] cursor-pointer rounded-lg border px-3 py-2 text-left text-sm ${
                bubble.role === 'user'
                  ? 'ml-auto bg-primary text-primary-foreground'
                  : 'bg-card text-card-foreground'
              }`}
              onClick={() => onSelectBubble(bubble)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault()
                  onSelectBubble(bubble)
                }
              }}
            >
              <span className="mb-1 flex items-center gap-1 text-[10px] uppercase tracking-wide opacity-70">
                {bubble.role === 'user' ? <User className="size-3" /> : <Bot className="size-3" />}
                {bubble.role}
                {bubble.model && ` · ${bubble.model}`}
                {bubble.phase && ` · ${bubble.phase}`}
                {bubble.streaming && ' · streaming'}
              </span>
              <div className="pointer-events-none">
                <MarkdownView text={bubble.text} />
              </div>
            </div>
          ))}
          {pending && (
            <ApprovalCard pending={pending} onDecide={onDecide} />
          )}
          <div ref={bottom} />
        </div>
      </div>
      <div className="border-t p-3">
        {error && <p className="mb-2 text-sm text-destructive">{error}</p>}
        <div className="relative">
          <button
            type="button"
            aria-label="拖动调整输入框高度"
            className="absolute inset-x-0 -top-1 z-10 h-2 cursor-n-resize"
            onMouseDown={startResize}
          />
          <textarea
            aria-label="发送消息"
            value={draft}
            disabled={busy || sending}
            placeholder={
              busy ? '当前 Run 仍在进行或等待批准' : '写入消息，Cmd/Ctrl+Enter 发送'
            }
            className="w-full resize-none rounded-md border border-input bg-transparent px-3 py-2 text-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-input/30"
            style={{ height: composer }}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                event.preventDefault()
                submit()
              }
            }}
          />
        </div>
        <div className="mt-2 flex justify-end">
          <Button onClick={submit} disabled={busy || sending || !draft.trim()}>
            发送
          </Button>
        </div>
      </div>
    </div>
  )
}
