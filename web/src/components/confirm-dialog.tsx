import { Button } from '@/components/ui/button'

export function ConfirmDialog({
  title,
  message,
  confirmLabel = '删除',
  onConfirm,
  onCancel,
}: {
  title: string
  message: string
  confirmLabel?: string
  onConfirm: () => void
  onCancel: () => void
}) {
  return (
    <div
      className="fixed inset-0 z-[80] flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onCancel}
    >
      <div
        role="alertdialog"
        aria-labelledby="desk-confirm-title"
        aria-describedby="desk-confirm-desc"
        className="w-full max-w-md rounded-lg border bg-background p-4 text-foreground shadow-lg"
        onClick={(event) => event.stopPropagation()}
      >
        <h2 id="desk-confirm-title" className="text-sm font-semibold">
          {title}
        </h2>
        <p id="desk-confirm-desc" className="mt-2 text-sm text-muted-foreground">
          {message}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel}>
            取消
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}
