import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import type { PendingApproval } from '@/lib/types'
import { ShieldQuestion } from 'lucide-react'

export function ApprovalCard({
  pending,
  onDecide,
  deciding,
}: {
  pending: PendingApproval
  onDecide: (allow: boolean) => void
  deciding?: 'allow' | 'deny'
}) {
  return (
    <Alert className="border-amber-500/40 bg-amber-500/5">
      <ShieldQuestion />
      <AlertTitle>Waiting for approval</AlertTitle>
      <AlertDescription>
        <p className="font-mono text-xs">
          seq #{pending.seq} · {pending.tool}
        </p>
        {pending.path && (
          <p className="mt-2">
            <span className="text-[10px] font-semibold tracking-wide">PATH</span>
            <code className="mt-1 block rounded-md border bg-background px-2 py-1">
              {pending.path}
            </code>
          </p>
        )}
        {pending.content && (
          <p className="mt-2">
            <span className="text-[10px] font-semibold tracking-wide">CONTENT</span>
            <pre className="mt-1 max-h-48 overflow-auto rounded-md border bg-background px-2 py-1 whitespace-pre-wrap">
              {pending.content}
            </pre>
          </p>
        )}
        <div className="mt-3 flex gap-2">
          <Button
            variant="destructive"
            size="sm"
            disabled={deciding !== undefined}
            onClick={() => onDecide(false)}
          >
            Deny
          </Button>
          <Button
            size="sm"
            disabled={deciding !== undefined}
            onClick={() => onDecide(true)}
          >
            Allow
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  )
}
