import type { FieldStats, Issue, Message, MessageField } from '../api/types'

// The content stream (D-STREAM) as a reducer: every SSE event goes through
// applyEvent, which returns the next state. Keeping it a pure function makes
// the event order (started → deltas → validated/invalid/failed, reset on a
// repair) testable without a browser.

export type GenPhase = 'idle' | 'preparing' | 'streaming' | 'validated' | 'invalid' | 'failed'

export interface GenState {
  phase: GenPhase
  text: Message
  /** Field the latest delta wrote to, for the typing caret. */
  current: MessageField | null
  issues: Issue[]
  stats: Partial<Record<MessageField, FieldStats>>
  failure: { code: string; message: string } | null
  repairs: number
}

export const emptyMessage = (): Message => ({ sms: '', email_subject: '', email_body: '' })

export function initialGen(): GenState {
  return { phase: 'idle', text: emptyMessage(), current: null, issues: [], stats: {}, failure: null, repairs: 0 }
}

const DELTA_FIELDS: Record<string, MessageField> = {
  'sms.delta': 'sms',
  'email.subject.delta': 'email_subject',
  'email.body.delta': 'email_body',
}

/** Events that end a generation stream. */
export const TERMINAL_EVENTS = ['validated', 'invalid', 'failed']
export const GENERATION_EVENTS = ['started', 'reset', ...Object.keys(DELTA_FIELDS), ...TERMINAL_EVENTS]

export function applyEvent(s: GenState, name: string, data: unknown): GenState {
  const field = DELTA_FIELDS[name]
  if (field) {
    const text = (data as { text?: string }).text ?? ''
    return { ...s, phase: 'streaming', current: field, text: { ...s.text, [field]: s.text[field] + text } }
  }
  switch (name) {
    case 'started':
      return { ...s, phase: 'preparing' }
    case 'reset':
      // A repair attempt starts over: the streamed candidate was invalid.
      return { ...s, phase: 'preparing', current: null, text: emptyMessage(), repairs: s.repairs + 1 }
    case 'validated': {
      const d = data as { message: Message; stats: GenState['stats'] }
      return { ...s, phase: 'validated', current: null, text: d.message, stats: d.stats ?? {}, issues: [] }
    }
    case 'invalid':
      return { ...s, phase: 'invalid', current: null, issues: (data as { issues?: Issue[] }).issues ?? [] }
    case 'failed':
      return { ...s, phase: 'failed', current: null, failure: data as GenState['failure'] }
    default:
      return s
  }
}

export function isRunning(p: GenPhase): boolean {
  return p === 'preparing' || p === 'streaming'
}
