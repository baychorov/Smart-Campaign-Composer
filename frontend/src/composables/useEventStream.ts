import { getCurrentScope, onScopeDispose, ref } from 'vue'

// One SSE connection (D-FE, D-STREAM). EventSource reconnects by itself after
// a network drop and sends Last-Event-ID, so the server resumes where it
// stopped. Two things it does not do, handled here:
//  - It also reconnects after the server closes a finished stream; the
//    connection is closed as soon as a terminal event arrives.
//  - It does not tell a dropped connection from a refused one (404). A
//    refused one ends with readyState CLOSED and is reported as 'error'.

export type StreamState = 'idle' | 'connecting' | 'open' | 'closed' | 'error'

/** The parts of EventSource used here, so tests can pass a fake. */
export interface EventSourceLike {
  readyState: number
  onopen: ((ev: Event) => unknown) | null
  onerror: ((ev: Event) => unknown) | null
  addEventListener(type: string, listener: (ev: MessageEvent) => void): void
  close(): void
}

export type EventSourceFactory = (url: string) => EventSourceLike

const CLOSED = 2 // EventSource.CLOSED

export interface StreamOptions {
  /** Event names to listen to; data is parsed as JSON. */
  events: string[]
  /** Events after which the stream is finished. */
  terminal: string[]
  onEvent: (name: string, data: unknown) => void
  createSource?: EventSourceFactory
}

export function useEventStream(opts: StreamOptions) {
  const state = ref<StreamState>('idle')
  const create = opts.createSource ?? ((url: string) => new EventSource(url))
  let source: EventSourceLike | null = null

  function close(final: StreamState = 'closed') {
    source?.close()
    source = null
    if (state.value !== 'idle') state.value = final
  }

  function connect(url: string) {
    close()
    const es = create(url)
    source = es
    state.value = 'connecting'
    es.onopen = () => {
      if (source === es) state.value = 'open'
    }
    es.onerror = () => {
      if (source !== es) return
      // CONNECTING means the browser is retrying; CLOSED means it gave up.
      if (es.readyState === CLOSED) close('error')
      else state.value = 'connecting'
    }
    for (const name of opts.events) {
      es.addEventListener(name, (ev: MessageEvent) => {
        if (source !== es) return // a late event of a closed stream
        let data: unknown = null
        try {
          data = JSON.parse(ev.data as string)
        } catch {
          // Malformed data is passed on as null; the handler decides.
        }
        opts.onEvent(name, data)
        if (opts.terminal.includes(name)) close()
      })
    }
  }

  // Close the connection when the component that opened it goes away.
  if (getCurrentScope()) onScopeDispose(() => close())

  return { state, connect, close }
}
