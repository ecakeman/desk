export type JsonPrimitive = string | number | boolean | null
export type JsonValue =
  | JsonPrimitive
  | JsonValue[]
  | { [key: string]: JsonValue }

export type JsonObject = { [key: string]: JsonValue }

export interface Session {
  id: string
  status?: string
  title?: string
  created_at?: string
  [key: string]: JsonValue | undefined
}

export interface Run {
  id: string
  session_id?: string
  status?: string
  workspace_dir?: string
  created_at?: string
  updated_at?: string
  model?: string
  phase?: string
  [key: string]: JsonValue | undefined
}

export interface DeskEvent {
  run_id?: string
  seq: number
  type: string
  payload: JsonValue
}

export interface TimelineEntry {
  key: string
  seq: number
  endSeq: number
  type: string
  payload: JsonValue
  events: DeskEvent[]
  merged: boolean
}

export interface PendingApproval {
  seq: number
  tool: string
  path?: string
  content?: string
  args: JsonObject
}

export type StreamState = 'idle' | 'connecting' | 'live' | 'retrying'
