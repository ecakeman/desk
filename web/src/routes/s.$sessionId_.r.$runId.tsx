import { createFileRoute } from '@tanstack/react-router'

import { Workspace } from '@/features/workspace/workspace'

export const Route = createFileRoute('/s/$sessionId_/r/$runId')({
  component: Workspace,
})
