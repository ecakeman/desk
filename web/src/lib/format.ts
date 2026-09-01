import type { VariantProps } from 'class-variance-authority'

import { badgeVariants } from '@/components/ui/badge'

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : '未知错误'
}

export function shortID(id: string): string {
  return id.length > 13 ? `${id.slice(0, 7)}…${id.slice(-4)}` : id
}

export function formatTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.valueOf())
    ? value
    : new Intl.DateTimeFormat('zh-CN', {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      }).format(date)
}

export function clip(text: string, max = 48): string {
  const trimmed = text.trim().replace(/\s+/g, ' ')
  if (trimmed.length <= max) return trimmed
  return `${trimmed.slice(0, max)}…`
}

export function statusVariant(
  status?: string,
): NonNullable<VariantProps<typeof badgeVariants>['variant']> {
  switch (status) {
    case 'failed':
    case 'interrupted':
    case 'cancelled':
      return 'destructive'
    case 'waiting':
    case 'waiting_approval':
      return 'outline'
    case 'completed':
    case 'open':
      return 'default'
    default:
      return 'secondary'
  }
}

export const TERMINAL_STATUSES = new Set([
  'completed',
  'failed',
  'interrupted',
  'cancelled',
])

export function isBusy(status?: string): boolean {
  return status === 'running' || status === 'waiting_approval' || status === 'waiting'
}
