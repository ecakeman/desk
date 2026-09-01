import type {
  DeskEvent,
  JsonValue,
  Run,
  Session,
} from '@/lib/types'

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body?: JsonValue,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

const segment = (value: string | number) => encodeURIComponent(String(value))

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })

  const text = await response.text()
  let body: JsonValue | undefined
  if (text) {
    try {
      body = JSON.parse(text) as JsonValue
    } catch {
      body = text
    }
  }

  if (!response.ok) {
    const detail =
      body && typeof body === 'object' && !Array.isArray(body)
        ? body.error
        : undefined
    throw new ApiError(
      typeof detail === 'string'
        ? detail
        : `${response.status} ${response.statusText}`,
      response.status,
      body,
    )
  }

  return body as T
}

export function normalizeCollection<T>(
  value: unknown,
  keys: string[],
): T[] {
  if (Array.isArray(value)) return value as T[]
  if (value && typeof value === 'object') {
    for (const key of [...keys, 'items', 'data']) {
      const candidate = (value as Record<string, unknown>)[key]
      if (Array.isArray(candidate)) return candidate as T[]
    }
  }
  return []
}

export const api = {
  async sessions(): Promise<Session[]> {
    return normalizeCollection<Session>(
      await request<unknown>('/v1/sessions'),
      ['sessions'],
    )
  },

  async runs(sessionID: string): Promise<Run[]> {
    return normalizeCollection<Run>(
      await request<unknown>(`/v1/sessions/${segment(sessionID)}/runs`),
      ['runs'],
    )
  },

  async sessionEvents(sessionID: string): Promise<DeskEvent[]> {
    return normalizeCollection<DeskEvent>(
      await request<unknown>(`/v1/sessions/${segment(sessionID)}/events`),
      ['events'],
    )
  },

  createSession(): Promise<Session> {
    return request('/v1/sessions', { method: 'POST' })
  },

  postMessage(
    sessionID: string,
    text: string,
  ): Promise<{ run_id: string }> {
    return request(`/v1/sessions/${segment(sessionID)}/messages`, {
      method: 'POST',
      body: JSON.stringify({ text }),
    })
  },

  run(runID: string): Promise<Run> {
    return request(`/v1/runs/${segment(runID)}`)
  },

  stm(runID: string): Promise<JsonValue> {
    return request(`/v1/runs/${segment(runID)}/stm`)
  },

  event(runID: string, seq: number): Promise<DeskEvent> {
    return request(`/v1/runs/${segment(runID)}/events/${segment(seq)}`)
  },

  decide(runID: string, seq: number, allow: boolean): Promise<{ ok: boolean }> {
    return request(`/v1/runs/${segment(runID)}/decisions`, {
      method: 'POST',
      body: JSON.stringify({ seq, allow }),
    })
  },

  cancel(runID: string): Promise<{ ok: boolean }> {
    return request(`/v1/runs/${segment(runID)}/cancel`, {
      method: 'POST',
      body: '{}',
    })
  },

  deleteSession(sessionID: string): Promise<{ ok: boolean }> {
    return request(`/v1/sessions/${segment(sessionID)}`, {
      method: 'DELETE',
    })
  },

  deleteAllSessions(): Promise<{ ok: boolean }> {
    return request('/v1/sessions', { method: 'DELETE' })
  },

  openWorkspace(): Promise<{ ok: boolean }> {
    return request('/v1/workspace/open', { method: 'POST' })
  },

  deleteRun(runID: string): Promise<{ ok: boolean }> {
    return request(`/v1/runs/${segment(runID)}`, {
      method: 'DELETE',
    })
  },
}
